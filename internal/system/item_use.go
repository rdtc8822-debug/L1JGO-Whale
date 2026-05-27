package system

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// ItemUseSystem 處理物品使用邏輯（消耗品、衝裝、鑑定、技能書、傳送卷軸、掉落系統）。
type ItemUseSystem struct {
	deps *handler.Deps
}

// NewItemUseSystem 建立 ItemUseSystem。
func NewItemUseSystem(deps *handler.Deps) *ItemUseSystem {
	return &ItemUseSystem{deps: deps}
}

const dragonEyeBuffDurationSec = 600

type dragonEyeItemEffect struct {
	skillID int32
	gfxID   int32
}

var dragonEyeItems = map[int32]dragonEyeItemEffect{
	47017: {skillID: 6684, gfxID: 7671}, // 地龍之魔眼
	47018: {skillID: 6685, gfxID: 7672}, // 水龍之魔眼
	47019: {skillID: 6686, gfxID: 7673}, // 風龍之魔眼
	47020: {skillID: 6683, gfxID: 7674}, // 火龍之魔眼
	47021: {skillID: 6688, gfxID: 7675}, // 誕生之魔眼
	47022: {skillID: 6689, gfxID: 7676}, // 形象之魔眼
	47023: {skillID: 6687, gfxID: 7678}, // 生命之魔眼
}

func applyPlayerPotionHealingModifiersLikeJava(player *world.PlayerInfo, heal int32) int32 {
	if player == nil || heal == 0 {
		return heal
	}
	if player.HasBuff(skillPolluteWater) {
		heal >>= 1
	}
	if player.HasBuff(mobSkillPolluteWater) {
		heal >>= 1
	}
	if player.HasBuff(mobSkillPotionTurnToDamage) {
		heal *= -1
	}
	return heal
}

func isPlayerItemUseBlocked(sess *net.Session, player *world.PlayerInfo) bool {
	if player.Paralyzed || player.Sleeped {
		handler.SendParalysis(sess, handler.TeleportUnlock)
		return true
	}
	return false
}

func (s *ItemUseSystem) itemUseViewers(player *world.PlayerInfo, excludeSession uint64) []*world.PlayerInfo {
	if s == nil || s.deps == nil || s.deps.World == nil || player == nil {
		return nil
	}
	return s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, excludeSession, player.ShowID)
}

func (s *ItemUseSystem) itemUseLineOfSightBlockedLikeJava(player *world.PlayerInfo, x, y int32) bool {
	if s == nil || s.deps == nil || s.deps.MapData == nil || player == nil {
		return false
	}
	return !s.deps.MapData.HasLineOfSight(player.MapID, player.X, player.Y, x, y)
}

// ---------- 消耗品使用（藥水、食物） ----------

// UseConsumable 處理消耗品使用。回傳 true 表示物品已被消耗。
// 藥水效果定義在 Lua (scripts/item/potions.lua)。
func (s *ItemUseSystem) UseConsumable(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) bool {
	if isPlayerItemUseBlocked(sess, player) {
		return false
	}

	if s.useDragonEye(sess, player, invItem, itemInfo) {
		return true
	}

	consumed := false

	pot := s.deps.Scripting.GetPotionEffect(int(invItem.ItemID))
	if pot != nil {
		// DECAY_POTION check (Java: skill 71) — 封鎖所有可飲用藥水。
		// Message 698: "喉嚨灼熱，無法喝東西"
		if player.HasBuff(handler.SkillDecayPotion) {
			handler.SendServerMessage(sess, 698)
			return false
		}

		switch pot.Type {
		case "heal":
			// Java ref: Potion.UseHeallingPotion — 總是消耗、總是播放音效/訊息。
			// 高斯隨機 ±20%: healHp *= (gaussian/5 + 1)
			if pot.Amount > 0 {
				healAmt := int32(float64(pot.Amount) * (rand.NormFloat64()/5.0 + 1.0))
				if healAmt < 1 {
					healAmt = 1
				}
				// Java UserAddHp：173/4012 讓回復量減半，4011 讓治療轉為傷害。
				healAmt = applyPlayerPotionHealingModifiersLikeJava(player, healAmt)
				applyPlayerHealDeltaLikeJava(s.deps, player, healAmt)
				gfx := int32(pot.GfxID)
				if gfx == 0 {
					gfx = 189 // 預設小藍光
				}
				s.BroadcastEffect(sess, player, gfx)
				if healAmt > 0 {
					handler.SendServerMessage(sess, 77) // "你覺得舒服多了"
				}
				consumed = true
			}

		case "mana":
			// Java ref: Potion.UseMpPotion — 總是消耗、總是播放音效/訊息。
			if pot.Amount > 0 {
				mpAmt := pot.Amount
				if pot.Range > 0 {
					mpAmt = pot.Amount + rand.Intn(pot.Range)
				}
				if player.MP < player.MaxMP {
					player.MP += int32(mpAmt)
					if player.MP > player.MaxMP {
						player.MP = player.MaxMP
					}
					sendMpUpdate(sess, player)
				}
				s.BroadcastEffect(sess, player, 190)
				handler.SendServerMessage(sess, 338) // "你的 魔力 漸漸恢復"
				consumed = true
			}

		case "haste":
			if pot.Duration > 0 {
				s.ApplyHaste(sess, player, pot.Duration, int32(pot.GfxID))
				consumed = true
			}

		case "brave":
			// 職業限制來自 Lua: "knight","elf","crown","notDKIL","DKIL"
			if pot.Duration > 0 {
				braveType := byte(pot.BraveType)
				classOK := checkBraveClassRestrict(player.ClassType, pot.ClassRestrict)
				if classOK {
					s.applyBrave(sess, player, pot.Duration, braveType, int32(pot.GfxID))
				} else {
					handler.SendServerMessage(sess, 79) // "沒有任何事情發生"
				}
				consumed = true // 無論職業是否匹配都消耗
			}

		case "wisdom":
			// Java: 慎重藥水僅限法師使用。
			if pot.Duration > 0 {
				if player.ClassType == 3 { // Wizard only
					s.applyWisdom(sess, player, pot.Duration, int16(pot.SP), int32(pot.GfxID))
					consumed = true
				} else {
					handler.SendServerMessage(sess, 79) // "沒有任何事情發生"
					// 不消耗（匹配 Java 行為）
				}
			}

		case "blue_potion":
			if pot.Duration > 0 {
				s.applyBluePotion(sess, player, pot.Duration, int32(pot.GfxID))
				consumed = true
			}

		case "eva_breath":
			// Java: Potion.useBlessOfEva — 持續時間疊加，上限 7200 秒。
			if pot.Duration > 0 {
				s.applyEvaBreath(sess, player, pot.Duration, int32(pot.GfxID))
				consumed = true
			}

		case "third_speed":
			// Java: Potion.ThirdSpeed — STATUS_THIRD_SPEED (1027)
			if pot.Duration > 0 {
				s.applyThirdSpeed(sess, player, pot.Duration, int32(pot.GfxID))
				consumed = true
			}

		case "blind":
			// Java: Potion.useBlindPotion — 自我施加 CURSE_BLIND。
			if pot.Duration > 0 {
				s.applyBlindPotion(sess, player, pot.Duration)
				consumed = true
			}

		case "cure_poison":
			// 移除中毒 debuff。
			handler.RemoveBuffAndRevert(player, 35, s.deps) // skill 35 = POISON
			consumed = true
			gfx := int32(pot.GfxID)
			if gfx == 0 {
				gfx = 192
			}
			s.BroadcastEffect(sess, player, gfx)
		}
	} else if elixirStat := elixirStatField(invItem.ItemID); elixirStat != nil {
		// 萬能藥（40033-40038）：永久 +1 對應屬性（Java: PanaceaStr/Con/Dex/Int/Wis/Cha）
		consumed = s.useElixir(sess, player, invItem.ItemID, elixirStat(player))
	} else if itemInfo.FoodVolume > 0 {
		// Java: foodvolume1 = item.getFoodVolume() / 10; if <= 0 then 5
		addFood := int16(itemInfo.FoodVolume / 10)
		if addFood <= 0 {
			addFood = 5
		}
		maxFood := int16(s.deps.Config.Gameplay.MaxFoodSatiety)
		if player.Food >= maxFood {
			handler.SendFoodUpdate(sess, player.Food)
		} else {
			player.Food += addFood
			if player.Food > maxFood {
				player.Food = maxFood
			}
			// 飽食度達 225 時記錄生存吶喊計時（Java: set_h_time）
			if player.Food >= 225 {
				player.FoodFullTime = time.Now().Unix()
			}
			handler.SendFoodUpdate(sess, player.Food)
			player.Dirty = true
		}
		// 料理 buff（Java: L1Cooking.useCookingItem）
		if cb, ok := cookingBuffMap[invItem.ItemID]; ok {
			s.applyCookingBuff(sess, player, cb)
		}
		consumed = true
	} else {
		s.deps.Log.Debug("unhandled etcitem use",
			zap.Int32("item_id", invItem.ItemID),
			zap.String("use_type", itemInfo.UseType),
		)
	}

	if consumed {
		removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
		if removed {
			handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, invItem)
		}
		handler.SendWeightUpdate(sess, player)
	}
	return consumed
}

func (s *ItemUseSystem) useDragonEye(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) bool {
	if player == nil || invItem == nil {
		return false
	}
	eff, ok := dragonEyeItems[invItem.ItemID]
	if !ok {
		return false
	}

	now := time.Now()
	if itemInfo != nil && itemInfo.DelayEffect > 0 && invItem.LastUsedAt > 0 {
		waitSec := int64(itemInfo.DelayEffect) - (now.Unix() - invItem.LastUsedAt)
		if waitSec > 0 {
			sendItemDelayEffectMessage(sess, invItem.Name, waitSec)
			return false
		}
	}

	skill := data.SkillInfo{SkillID: eff.skillID, BuffDuration: dragonEyeBuffDurationSec}
	if s.deps != nil && s.deps.Skills != nil {
		if base := s.deps.Skills.Get(eff.skillID); base != nil {
			skill = *base
			if skill.BuffDuration <= 0 {
				skill.BuffDuration = dragonEyeBuffDurationSec
			}
		}
	}

	skillSys := &SkillSystem{deps: s.deps}
	skillSys.applyBuffEffect(player, &skill)
	s.BroadcastEffect(sess, player, eff.gfxID)
	invItem.LastUsedAt = now.Unix()
	player.Dirty = true
	return true
}

func sendItemDelayEffectMessage(sess *net.Session, itemName string, waitSec int64) {
	if sess == nil {
		return
	}
	if itemName == "" {
		itemName = "item"
	}
	if waitSec > 60 {
		handler.SendServerMessageArgs(sess, 1139, fmt.Sprintf("%s %d", itemName, waitSec/60))
		return
	}
	handler.SendSystemMessage(sess, fmt.Sprintf("%s %d秒後可以使用。", itemName, waitSec))
}

// UseResurrectionScroll 處理復活卷軸。Java: Scroll_Resurrection / Reactivating_Reel。
// 封包額外欄位為目標物件 ID；有效死亡目標會先消耗卷軸，再依目標類型處理。
func (s *ItemUseSystem) UseResurrectionScroll(sess *net.Session, player *world.PlayerInfo, scroll *world.InvItem, targetObjID int32) bool {
	if player == nil || scroll == nil || targetObjID == 0 {
		return false
	}
	if targetObjID == player.CharID {
		return false
	}

	if target := s.deps.World.GetByCharID(targetObjID); target != nil {
		if !target.Dead {
			return false
		}
		s.consumeUsedItem(sess, player, scroll)
		if s.deps.World.IsPlayerAt(target.X, target.Y, target.MapID, target.SessionID) {
			handler.SendServerMessage(sess, 592)
			return true
		}
		if s.deps.MapData != nil {
			if mi := s.deps.MapData.GetInfo(player.MapID); mi != nil && !mi.Resurrection {
				return true
			}
		}
		target.PendingResCaster = player.CharID
		if scroll.Bless != 0 {
			target.PendingResSkill = 61
			handler.SendYesNoDialog(target.Session, 321)
		} else {
			target.PendingResSkill = 75
			handler.SendYesNoDialog(target.Session, 322)
		}
		return true
	}

	skillSys := &SkillSystem{deps: s.deps}
	if pet := s.deps.World.GetPet(targetObjID); pet != nil {
		if !pet.Dead {
			return false
		}
		s.consumeUsedItem(sess, player, scroll)
		skill := &data.SkillInfo{SkillID: 61}
		if skillSys.resurrectPetWithHP(sess, player, skill, pet, pet.MaxHP/4) {
			return true
		}
		return true
	}
	if npc := s.deps.World.GetNpc(targetObjID); npc != nil {
		if !npc.Dead {
			return false
		}
		s.consumeUsedItem(sess, player, scroll)
		_ = skillSys.resurrectNpcWithHP(npc, npc.MaxHP/4)
		return true
	}
	return false
}

// UseDissolution 處理溶解劑。Java: Dissolution.execute / ResolventTable。
func (s *ItemUseSystem) UseDissolution(sess *net.Session, player *world.PlayerInfo, resolvent *world.InvItem, targetObjID int32) bool {
	return s.UseDissolutionWithRoll(sess, player, resolvent, targetObjID, rand.Intn(100)+1)
}

// UseDissolutionWithRoll 以指定 roll 執行溶解，供測試確認 yiwei 機率邊界。
func (s *ItemUseSystem) UseDissolutionWithRoll(sess *net.Session, player *world.PlayerInfo, resolvent *world.InvItem, targetObjID int32, roll int) bool {
	if player == nil || player.Inv == nil || resolvent == nil {
		handler.SendServerMessage(sess, 79) // 沒有任何事情發生。
		return false
	}
	target := player.Inv.FindByObjectID(targetObjID)
	if target == nil {
		return false
	}

	targetInfo := s.deps.Items.Get(target.ItemID)
	if targetInfo == nil {
		return false
	}

	if targetInfo.Category == data.CategoryWeapon || targetInfo.Category == data.CategoryArmor {
		if target.EnchantLvl != 0 || target.Equipped {
			handler.SendServerMessage(sess, 1161) // 無法溶解。
			return false
		}
	}

	crystalCount := calcDissolutionCrystalCount(s.deps.Resolvents.CrystalCount(target.ItemID), roll)
	if crystalCount == 0 {
		handler.SendServerMessage(sess, 1161) // 無法溶解。
		return false
	}

	const crystalItemID int32 = 41246
	crystalInfo := s.deps.Items.Get(crystalItemID)
	if s.deps.ItemCreate != nil {
		if _, ok := s.deps.ItemCreate.GiveItem(sess, player, crystalItemID, crystalCount); !ok {
			return false
		}
	} else if crystalInfo != nil {
		existing := player.Inv.FindByItemID(crystalItemID)
		wasExisting := existing != nil && crystalInfo.Stackable
		crystal := player.Inv.AddItem(crystalItemID, crystalCount, crystalInfo.Name,
			crystalInfo.InvGfx, crystalInfo.Weight, crystalInfo.Stackable, byte(crystalInfo.Bless))
		crystal.UseType = crystalInfo.UseTypeID
		crystal.Identified = true
		if wasExisting {
			handler.SendItemCountUpdate(sess, crystal)
		} else {
			handler.SendAddItem(sess, crystal, crystalInfo)
		}
	}

	targetRemoved := player.Inv.RemoveItem(target.ObjectID, 1)
	if targetRemoved {
		handler.SendRemoveInventoryItem(sess, target.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, target)
	}

	resolventRemoved := player.Inv.RemoveItem(resolvent.ObjectID, 1)
	if resolventRemoved {
		handler.SendRemoveInventoryItem(sess, resolvent.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, resolvent)
	}
	handler.SendWeightUpdate(sess, player)
	player.Dirty = true
	return true
}

func calcDissolutionCrystalCount(base int32, roll int) int32 {
	if base <= 0 {
		return 0
	}
	if roll <= 50 {
		return base
	}
	if roll <= 90 {
		return base * 3 / 2
	}
	return base * 2
}

// UseWhetstone 處理磨刀石修復耐久。
// Java ref: item_etcitem.Hone — 目標不存在不消耗；目標存在但無效則送 79 並消耗。
func (s *ItemUseSystem) UseWhetstone(sess *net.Session, player *world.PlayerInfo, stone *world.InvItem, targetObjID int32) bool {
	if player == nil || player.Inv == nil || stone == nil {
		return false
	}
	target := player.Inv.FindByObjectID(targetObjID)
	if target == nil {
		return false
	}

	targetInfo := s.deps.Items.Get(target.ItemID)
	if targetInfo != nil && targetInfo.Category != data.CategoryEtcItem && target.Durability > 0 {
		target.Durability--
		if target.Durability < 0 {
			target.Durability = 0
		}
		syncEquippedFlagFromSlots(player, target)
		handler.SendItemStatusUpdate(sess, target, targetInfo)
		if target.Durability == 0 {
			handler.SendServerMessageArgs(sess, 464, itemLogName(target))
		} else {
			handler.SendServerMessageArgs(sess, 463, itemLogName(target))
		}
	} else {
		handler.SendServerMessage(sess, 79)
	}

	s.consumeUsedItem(sess, player, stone)
	player.Dirty = true
	return true
}

func (s *ItemUseSystem) consumeUsedItem(sess *net.Session, player *world.PlayerInfo, item *world.InvItem) {
	if player == nil || player.Inv == nil || item == nil {
		return
	}
	removed := player.Inv.RemoveItem(item.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, item.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, item)
	}
	handler.SendWeightUpdate(sess, player)
}

// ---------- 衝裝卷軸 ----------

// EnchantItem 處理武器/防具衝裝卷軸使用。
// C_USE_ITEM 接續資料: [D targetObjectID]
// Java ref: Enchant.java — scrollOfEnchantWeapon / scrollOfEnchantArmor
func (s *ItemUseSystem) EnchantItem(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, scroll *world.InvItem, scrollInfo *data.ItemInfo) {
	targetObjID := r.ReadD()

	target := player.Inv.FindByObjectID(targetObjID)
	if target == nil {
		return
	}

	targetInfo := s.deps.Items.Get(target.ItemID)
	if targetInfo == nil {
		return
	}

	// 封印物品不可衝裝 (Java: getBless() >= 128)
	if target.Bless >= 128 {
		handler.SendServerMessage(sess, 79) // "沒有任何事情發生。"
		return
	}

	// 驗證卷軸對應正確類別
	if scrollInfo.UseType == "dai" && targetInfo.Category != data.CategoryWeapon {
		return
	}
	if scrollInfo.UseType == "zel" && targetInfo.Category != data.CategoryArmor {
		return
	}

	// Lua 衝裝分類
	category := 1 // weapon
	if targetInfo.Category == data.CategoryArmor {
		category = 2
	}

	// 呼叫 Lua 衝裝公式
	result := s.deps.Scripting.CalcEnchant(scripting.EnchantContext{
		ScrollBless:  enchantScrollBless(scroll.ItemID, int(scroll.Bless)),
		EnchantLvl:   int(target.EnchantLvl),
		SafeEnchant:  targetInfo.SafeEnchant,
		Category:     category,
		WeaponChance: s.deps.Config.Enchant.WeaponChance,
		ArmorChance:  s.deps.Config.Enchant.ArmorChance,
	})

	// 消耗卷軸
	scrollRemoved := player.Inv.RemoveItem(scroll.ObjectID, 1)
	if scrollRemoved {
		handler.SendRemoveInventoryItem(sess, scroll.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, scroll)
	}
	handler.SendWeightUpdate(sess, player)

	// 光色: $245=藍(武器), $252=銀(防具), $246=黑(詛咒)
	lightColor := "$245"
	if targetInfo.Category == data.CategoryArmor {
		lightColor = "$252"
	}
	itemLogName := handler.BuildViewName(target, targetInfo)

	switch result.Result {
	case "success":
		target.EnchantLvl += int8(result.Amount)
		handler.SendItemStatusUpdate(sess, target, targetInfo)
		handler.SendItemNameUpdate(sess, target, targetInfo)
		sendEffectOnPlayer(sess, player.CharID, 2583) // 衝裝成功 GFX

		// S_ServerMessage 161: "%0%s 發出 %1 光芒變成 %2"
		resultDesc := "$247" // 更明亮 (+1)
		if result.Amount >= 2 {
			resultDesc = "$248" // 更加閃耀 (+2, +3)
		}
		handler.SendServerMessageArgs(sess, 161, itemLogName, lightColor, resultDesc)

		// 若已裝備則重算屬性
		if target.Equipped && s.deps.Equip != nil {
			s.deps.Equip.RecalcEquipStats(sess, player)
		}

		s.deps.Log.Info(fmt.Sprintf("衝裝成功  角色=%s  道具=%s  衝裝等級=%d", player.Name, targetInfo.Name, target.EnchantLvl))

	case "nochange":
		// S_ServerMessage 160: "%0%s 發出強烈 %1 光芒但 %2"
		handler.SendServerMessageArgs(sess, 160, itemLogName, lightColor, "$248")
		s.deps.Log.Info(fmt.Sprintf("衝裝無變化  角色=%s  道具=%s", player.Name, targetInfo.Name))

	case "break":
		// 裝備碎裂
		breakColor := lightColor
		if target.EnchantLvl < 0 {
			breakColor = "$246" // 詛咒物品用黑色
		}
		handler.SendServerMessageArgs(sess, 164, itemLogName, breakColor)

		if target.Equipped && s.deps.Equip != nil {
			slot := s.deps.Equip.FindEquippedSlot(player, target)
			if slot != world.SlotNone {
				s.deps.Equip.UnequipSlot(sess, player, slot)
			}
		}
		player.Inv.RemoveItem(target.ObjectID, target.Count)
		handler.SendRemoveInventoryItem(sess, target.ObjectID)
		handler.SendWeightUpdate(sess, player)

		s.deps.Log.Info(fmt.Sprintf("衝裝碎裂  角色=%s  道具=%s", player.Name, targetInfo.Name))

	case "minus":
		// 詛咒卷軸: -N
		target.EnchantLvl -= int8(result.Amount)
		handler.SendItemStatusUpdate(sess, target, targetInfo)
		handler.SendItemNameUpdate(sess, target, targetInfo)

		handler.SendServerMessageArgs(sess, 161, itemLogName, "$246", "$247")

		if target.Equipped && s.deps.Equip != nil {
			s.deps.Equip.RecalcEquipStats(sess, player)
		}

		s.deps.Log.Info(fmt.Sprintf("衝裝降級  角色=%s  道具=%s  衝裝等級=%d", player.Name, targetInfo.Name, target.EnchantLvl))
	}
}

// ---------- 鑑定卷軸 ----------

// IdentifyItem 處理鑑定卷軸使用。
// C_USE_ITEM 接續資料: [D targetObjectID]
func (s *ItemUseSystem) IdentifyItem(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, scroll *world.InvItem) {
	targetObjID := r.ReadD()

	target := player.Inv.FindByObjectID(targetObjID)
	if target == nil {
		return
	}

	targetInfo := s.deps.Items.Get(target.ItemID)
	if targetInfo == nil {
		return
	}

	// 設定鑑定旗標
	target.Identified = true

	// 發送完整狀態位元組更新（武器/防具屬性可見）
	handler.SendItemStatusUpdate(sess, target, targetInfo)

	// 發送祝福顏色更新
	handler.SendItemColor(sess, target.ObjectID, target.Bless)

	// 發送鑑定描述彈窗
	handler.SendIdentifyDesc(sess, target, targetInfo)

	// 消耗卷軸
	removed := player.Inv.RemoveItem(scroll.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, scroll.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, scroll)
	}
	handler.SendWeightUpdate(sess, player)
}

// ---------- 技能書 ----------

// spellBookPrefixes 技能書名稱前綴對照。
// Java 透過物品名稱 "魔法書(技能名)" → 技能名 來解析。
var spellBookPrefixes = []string{
	"魔法書(",    // Wizard / common
	"技術書(",    // Knight
	"精靈水晶(",   // Elf
	"黑暗精靈水晶(", // Dark Elf
	"龍騎士書板(",  // Dragon Knight
	"記憶水晶(",   // Illusionist
}

// extractSkillName 從技能書名稱中提取技能名。
func extractSkillName(itemName string) string {
	for _, prefix := range spellBookPrefixes {
		if len(itemName) > len(prefix) && itemName[:len(prefix)] == prefix {
			inner := itemName[len(prefix):]
			if len(inner) > 0 && inner[len(inner)-1] == ')' {
				return inner[:len(inner)-1]
			}
			return inner
		}
	}
	return ""
}

// UseSpellBook 處理技能書使用。
// 從物品名稱提取技能名，驗證職業/等級，學習技能。
func (s *ItemUseSystem) UseSpellBook(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) {
	skillName := extractSkillName(itemInfo.Name)
	if skillName == "" {
		s.deps.Log.Debug("spellbook: cannot extract skill name",
			zap.String("item_name", itemInfo.Name))
		return
	}

	skill := s.deps.Skills.GetByName(skillName)
	if skill == nil {
		s.deps.Log.Debug("spellbook: skill not found",
			zap.String("skill_name", skillName))
		return
	}

	// 檢查職業/等級需求
	reqLevel := s.deps.SpellbookReqs.GetLevelReq(player.ClassType, invItem.ItemID)
	if reqLevel == 0 {
		handler.SendServerMessage(sess, 264) // 你的職業無法使用此道具。
		return
	}
	if int(player.Level) < reqLevel {
		handler.SendServerMessageArgs(sess, 318, strconv.Itoa(reqLevel)) // 等級 %0以上才可使用此道具。
		return
	}

	// 檢查是否已學會
	for _, sid := range player.KnownSpells {
		if sid == skill.SkillID {
			handler.SendServerMessage(sess, 78) // 你已經學會了。
			return
		}
	}

	// 學習技能
	player.KnownSpells = append(player.KnownSpells, skill.SkillID)
	handler.SendAddSingleSkill(sess, skill)

	// 學習特效 (GFX 224)
	handler.SendSkillEffect(sess, player.CharID, 224)
	nearby := s.itemUseViewers(player, sess.ID)
	for _, other := range nearby {
		handler.SendSkillEffect(other.Session, player.CharID, 224)
	}

	// 消耗技能書
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, invItem)
	}
	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Info(fmt.Sprintf("玩家從技能書學習技能  角色=%s  技能=%s  技能ID=%d  書籍=%s", player.Name, skill.Name, skill.SkillID, itemInfo.Name))
}

// ---------- 傳送卷軸 ----------

// UseTeleportScroll 處理傳送卷軸使用。
// 封包接續: [H mapID][D bookmarkID]
// Java ref: C_ItemUSe.java lines 1572-1625, L1Teleport.teleport()
func (s *ItemUseSystem) UseTeleportScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, invItem *world.InvItem) {
	if isPlayerItemUseBlocked(sess, player) {
		return
	}

	_ = r.ReadH()           // mapID from client
	bookmarkID := r.ReadD() // bookmark ID (0 = 無書籤 → 隨機傳送)

	if player.Dead {
		return
	}

	// 取消交易
	if s.deps.Trade != nil {
		s.deps.Trade.CancelIfActive(player)
	}

	// 查找書籤
	var target *world.Bookmark
	if bookmarkID != 0 {
		for i := range player.Bookmarks {
			if player.Bookmarks[i].ID == bookmarkID {
				target = &player.Bookmarks[i]
				break
			}
		}
	}

	if target != nil {
		// 書籤傳送
		removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
		if removed {
			handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, invItem)
		}
		handler.SendWeightUpdate(sess, player)

		// 出發特效
		s.BroadcastEffect(sess, player, 169)

		handler.TeleportPlayer(sess, player, target.X, target.Y, target.MapID, 5, s.deps)

		s.deps.Log.Info(fmt.Sprintf("書籤傳送  角色=%s  書籤=%s  x=%d  y=%d  地圖=%d", player.Name, target.Name, target.X, target.Y, target.MapID))
	} else {
		// 無書籤 → 200 格內隨機傳送 (Java: randomLocation(200, true))
		removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
		if removed {
			handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, invItem)
		}
		handler.SendWeightUpdate(sess, player)

		curMap := player.MapID
		newX := player.X
		newY := player.Y
		minRX := player.X - 200
		maxRX := player.X + 200
		minRY := player.Y - 200
		maxRY := player.Y + 200
		if s.deps.MapData != nil {
			if mi := s.deps.MapData.GetInfo(curMap); mi != nil {
				if minRX < mi.StartX {
					minRX = mi.StartX
				}
				if maxRX > mi.EndX {
					maxRX = mi.EndX
				}
				if minRY < mi.StartY {
					minRY = mi.StartY
				}
				if maxRY > mi.EndY {
					maxRY = mi.EndY
				}
			}
		}
		diffX := maxRX - minRX
		diffY := maxRY - minRY
		if diffX > 0 && diffY > 0 {
			for attempt := 0; attempt < 40; attempt++ {
				rx := minRX + int32(world.RandInt(int(diffX)+1))
				ry := minRY + int32(world.RandInt(int(diffY)+1))
				if s.deps.MapData != nil && s.deps.MapData.IsInMap(curMap, rx, ry) &&
					s.deps.MapData.IsPassablePoint(curMap, rx, ry) {
					newX = rx
					newY = ry
					break
				}
			}
		}

		// 出發特效
		s.BroadcastEffect(sess, player, 169)

		handler.TeleportPlayer(sess, player, newX, newY, curMap, 5, s.deps)

		s.deps.Log.Info(fmt.Sprintf("隨機傳送  角色=%s  x=%d  y=%d", player.Name, newX, newY))
	}
}

// UseHomeScroll 處理回家卷軸使用。
// Java ref: C_ItemUSe.java lines 1503-1511, L1Teleport.teleport()
func (s *ItemUseSystem) UseHomeScroll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem) {
	if player.Dead {
		return
	}

	// 取得回家目的地（依地圖和座標找最近城鎮，非死亡重生點）
	loc := s.deps.Scripting.GetHomeScrollLocation(int(player.MapID), int(player.X), int(player.Y))
	if loc == nil {
		loc = &scripting.RespawnLocation{X: 33089, Y: 33397, Map: 4}
	}

	// 取消交易
	if s.deps.Trade != nil {
		s.deps.Trade.CancelIfActive(player)
	}

	// 消耗卷軸
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, invItem)
	}
	handler.SendWeightUpdate(sess, player)

	// 出發特效 + 延遲 2 tick（400ms）傳送，讓客戶端播完特效動畫
	// 特效在本 tick 末尾 flush 給客戶端，傳送在下一 tick 執行
	s.BroadcastEffect(sess, player, 169)
	player.ScrollTPTick = 2
	player.ScrollTPX = int32(loc.X)
	player.ScrollTPY = int32(loc.Y)
	player.ScrollTPMap = int16(loc.Map)

	s.deps.Log.Info(fmt.Sprintf("回家卷軸  角色=%s  目標=(%d,%d) 地圖=%d", player.Name, loc.X, loc.Y, loc.Map))
}

// UseFixedTeleportScroll 處理指定傳送卷軸使用。
// 這些物品在 etcitem YAML 中設定了 loc_x/loc_y/map_id。
func (s *ItemUseSystem) UseFixedTeleportScroll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) {
	if player.Dead {
		return
	}

	// 取消交易
	if s.deps.Trade != nil {
		s.deps.Trade.CancelIfActive(player)
	}

	// 消耗卷軸
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, invItem)
	}
	handler.SendWeightUpdate(sess, player)

	// 出發特效 + 延遲 2 tick（400ms）傳送，讓客戶端播完特效動畫
	s.BroadcastEffect(sess, player, 169)
	player.ScrollTPTick = 2
	player.ScrollTPX = itemInfo.LocX
	player.ScrollTPY = itemInfo.LocY
	player.ScrollTPMap = itemInfo.LocMapID

	s.deps.Log.Info(fmt.Sprintf("指定傳送  角色=%s  道具=%s  目標=(%d,%d) 地圖=%d",
		player.Name, itemInfo.Name, itemInfo.LocX, itemInfo.LocY, itemInfo.LocMapID))
}

// ---------- 掉落系統 ----------

// lootingRange 自動分配拾取範圍（Java: LOOTING_RANGE = 15 格）。
const lootingRange = 15

// GiveDrops 為擊殺的 NPC 擲骰掉落物品。
// 若擊殺者所在隊伍為自動分配模式（PartyTypeAutoShare），
// 則按仇恨比例加權隨機分配給範圍內的隊伍成員（Java: DropShare.java）。
func (s *ItemUseSystem) GiveDrops(killer *world.PlayerInfo, npc *world.NpcInfo) {
	if s.deps.Drops == nil {
		return
	}
	dropList := s.deps.Drops.Get(npc.NpcID)
	if dropList == nil {
		return
	}

	// 收集自動分配候選人
	candidates := s.collectAutoShareCandidates(killer, npc)

	dropRate := s.deps.Config.Rates.DropRate
	goldRate := s.deps.Config.Rates.GoldRate

	for _, drop := range dropList {
		chance := drop.Chance
		if drop.ItemID == world.AdenaItemID {
			if goldRate > 0 {
				chance = int(float64(chance) * goldRate)
			}
		} else {
			if dropRate > 0 {
				chance = int(float64(chance) * dropRate)
			}
		}
		if chance > 1000000 {
			chance = 1000000
		}

		roll := world.RandInt(1000000)
		if roll >= chance {
			continue
		}

		qty := int32(drop.Min)
		if drop.Max > drop.Min {
			qty = int32(drop.Min + world.RandInt(drop.Max-drop.Min+1))
		}
		if qty <= 0 {
			qty = 1
		}

		if drop.ItemID == world.AdenaItemID && goldRate > 0 {
			qty = int32(float64(qty) * goldRate)
			if qty <= 0 {
				qty = 1
			}
		}

		// 選擇接收者：自動分配 → 加權隨機；否則 → killer
		receiver := killer
		if len(candidates) > 1 {
			receiver = weightedRandomByHate(candidates, npc.HateList)
		}

		if receiver.Inv.IsFull() {
			// 接收者背包滿，退回給 killer
			if receiver.CharID != killer.CharID {
				receiver = killer
			}
			if receiver.Inv.IsFull() {
				continue // 兩者都滿，跳過
			}
		}

		s.giveDropToPlayer(receiver, drop, qty)
	}
}

// collectAutoShareCandidates 收集自動分配候選人（同隊伍、同地圖、拾取範圍內、活人）。
func (s *ItemUseSystem) collectAutoShareCandidates(killer *world.PlayerInfo, npc *world.NpcInfo) []*world.PlayerInfo {
	if killer.PartyID == 0 {
		return nil
	}
	party := s.deps.World.Parties.GetParty(killer.CharID)
	if party == nil || party.PartyType != world.PartyTypeAutoShare {
		return nil
	}

	candidates := make([]*world.PlayerInfo, 0, len(party.Members))
	for _, memberID := range party.Members {
		member := s.deps.World.GetByCharID(memberID)
		if member == nil || member.Dead || member.MapID != npc.MapID {
			continue
		}
		// 檢查與 NPC 的距離（Java: DropShare 用 LOOTING_RANGE）
		dx := member.X - npc.X
		if dx < 0 {
			dx = -dx
		}
		dy := member.Y - npc.Y
		if dy < 0 {
			dy = -dy
		}
		dist := dx
		if dy > dist {
			dist = dy
		}
		if dist <= lootingRange {
			candidates = append(candidates, member)
		}
	}
	return candidates
}

// weightedRandomByHate 按仇恨值加權隨機選擇一個玩家。
// Java: DropShare — 仇恨越高的成員獲得掉落物的機率越大。
func weightedRandomByHate(candidates []*world.PlayerInfo, hateList map[uint64]int32) *world.PlayerInfo {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	// 計算各候選人的仇恨權重
	weights := make([]int32, len(candidates))
	totalWeight := int32(0)
	for i, c := range candidates {
		hate := hateList[c.SessionID]
		if hate <= 0 {
			hate = 1 // 最低權重 1，確保在範圍內的隊員都有機會
		}
		weights[i] = hate
		totalWeight += hate
	}

	if totalWeight <= 0 {
		// fallback：均等分配
		return candidates[world.RandInt(len(candidates))]
	}

	// 加權隨機選擇
	roll := int32(world.RandInt(int(totalWeight)))
	cumulative := int32(0)
	for i, w := range weights {
		cumulative += w
		if roll < cumulative {
			return candidates[i]
		}
	}
	return candidates[len(candidates)-1]
}

// giveDropToPlayer 將掉落物品加入指定玩家背包並發送封包通知。
func (s *ItemUseSystem) giveDropToPlayer(receiver *world.PlayerInfo, drop data.DropItem, qty int32) {
	itemInfo := s.deps.Items.Get(drop.ItemID)
	if itemInfo == nil {
		return
	}

	sendDropNotice := func() {
		if drop.ItemID == world.AdenaItemID {
			handler.SendShowDrop(receiver.Session, handler.ShowDropAdena, qty)
			msg := fmt.Sprintf("獲得 %d 金幣", qty)
			handler.SendGlobalChat(receiver.Session, 9, msg)
			return
		}

		name := itemInfo.Name
		if drop.EnchantLevel > 0 {
			name = fmt.Sprintf("+%d %s", drop.EnchantLevel, name)
		}
		if qty > 1 {
			msg := fmt.Sprintf("獲得 %s (%d)", name, qty)
			handler.SendItemBoard(receiver.Session, uint16(itemInfo.InvGfx), msg)
			handler.SendGlobalChat(receiver.Session, 9, msg)
			return
		}
		msg := fmt.Sprintf("獲得 %s", name)
		handler.SendItemBoard(receiver.Session, uint16(itemInfo.InvGfx), msg)
		handler.SendGlobalChat(receiver.Session, 9, msg)
	}

	if s.deps.ItemCreate != nil {
		opts := ItemCreateOptions{}
		needsOptions := false
		if drop.EnchantLevel != 0 {
			opts.EnchantLvl = int8(drop.EnchantLevel)
			needsOptions = true
		}
		if itemInfo.Category == data.CategoryWeapon || itemInfo.Category == data.CategoryArmor {
			needsOptions = true
			opts.BeforeSend = func(item *world.InvItem) {
				item.Identified = false
			}
		}
		if needsOptions {
			if creator, ok := s.deps.ItemCreate.(interface {
				GiveItemWithOptions(sess *net.Session, player *world.PlayerInfo, itemID, count int32, opts ItemCreateOptions) (*world.InvItem, bool)
			}); ok {
				if _, ok := creator.GiveItemWithOptions(receiver.Session, receiver, drop.ItemID, qty, opts); !ok {
					return
				}
				sendDropNotice()
				return
			}
		} else {
			if _, ok := s.deps.ItemCreate.GiveItem(receiver.Session, receiver, drop.ItemID, qty); !ok {
				return
			}
			sendDropNotice()
			return
		}
	}

	stackable := itemInfo.Stackable || drop.ItemID == world.AdenaItemID
	existing := receiver.Inv.FindByItemID(drop.ItemID)
	wasExisting := existing != nil && stackable

	item := receiver.Inv.AddItem(
		drop.ItemID,
		qty,
		itemInfo.Name,
		itemInfo.InvGfx,
		itemInfo.Weight,
		stackable,
		byte(itemInfo.Bless),
	)
	item.EnchantLvl = int8(drop.EnchantLevel)
	item.UseType = itemInfo.UseTypeID
	// 怪物掉落的裝備預設未鑑定（暗名、無屬性）
	if itemInfo.Category == data.CategoryWeapon || itemInfo.Category == data.CategoryArmor {
		item.Identified = false
	}

	if wasExisting {
		handler.SendItemCountUpdate(receiver.Session, item)
	} else {
		handler.SendAddItem(receiver.Session, item)
	}
	handler.SendWeightUpdate(receiver.Session, receiver)

	// 通知玩家掉落
	sendDropNotice()
}

// ---------- 加速/勇敢效果 ----------

// ApplyHaste 套用加速效果（移動+攻擊速度）。
// Java ref: Potion.useGreenPotion → setSkillEffect(STATUS_HASTE, time*1000) + setMoveSpeed(1)
func (s *ItemUseSystem) ApplyHaste(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32) {
	// 移除衝突加速/減速 buff
	for _, conflictID := range []int32{43, 54, handler.SkillStatusHaste} {
		handler.RemoveBuffAndRevert(player, conflictID, s.deps)
	}

	buff := &world.ActiveBuff{
		SkillID:      handler.SkillStatusHaste,
		TicksLeft:    durationSec * 5,
		SetMoveSpeed: 1,
	}
	old := player.AddBuff(buff)
	if old != nil {
		s.deps.Skill.RevertBuffStats(player, old)
	}

	player.MoveSpeed = 1
	player.HasteTicks = buff.TicksLeft

	sendSpeedPacket(sess, player.CharID, 1, uint16(durationSec))
	nearby := s.itemUseViewers(player, sess.ID)
	for _, other := range nearby {
		sendSpeedPacket(other.Session, player.CharID, 1, 0)
	}
	s.BroadcastEffect(sess, player, gfxID)
}

// BroadcastEffect 向自己和附近玩家廣播特效。
func (s *ItemUseSystem) BroadcastEffect(sess *net.Session, player *world.PlayerInfo, gfxID int32) {
	sendEffectOnPlayer(sess, player.CharID, gfxID)
	nearby := s.itemUseViewers(player, sess.ID)
	for _, other := range nearby {
		sendEffectOnPlayer(other.Session, player.CharID, gfxID)
	}
}

// ---------- 內部 buff 方法 ----------

// applyBrave 套用勇敢藥水效果。
// Java ref: Potion.buff_brave → setSkillEffect(skillId, time*1000) + setBraveSpeed(type)
func (s *ItemUseSystem) applyBrave(sess *net.Session, player *world.PlayerInfo, durationSec int, braveType byte, gfxID int32) {
	for _, conflictID := range []int32{
		handler.SkillStatusBrave, handler.SkillStatusElfBrave,
		42,  // HOLY_WALK
		150, // MOVING_ACCELERATION
		101, // WIND_WALK
		52,  // BLOODLUST
	} {
		handler.RemoveBuffAndRevert(player, conflictID, s.deps)
	}

	skillID := handler.SkillStatusBrave
	if braveType == 3 {
		skillID = handler.SkillStatusElfBrave
	}

	buff := &world.ActiveBuff{
		SkillID:       skillID,
		TicksLeft:     durationSec * 5,
		SetBraveSpeed: braveType,
	}
	old := player.AddBuff(buff)
	if old != nil {
		s.deps.Skill.RevertBuffStats(player, old)
	}

	player.BraveSpeed = braveType
	player.BraveTicks = buff.TicksLeft

	sendBravePacket(sess, player.CharID, braveType, uint16(durationSec))
	nearby := s.itemUseViewers(player, sess.ID)
	for _, other := range nearby {
		sendBravePacket(other.Session, player.CharID, braveType, 0)
	}
	handler.BroadcastVisualUpdate(sess, player, s.deps)
	s.BroadcastEffect(sess, player, gfxID)
}

// applyWisdom 套用慎重藥水效果（SP 加成）。
// Java ref: Potion.useWisdomPotion → addSp(2) + setSkillEffect(STATUS_WISDOM_POTION, time*1000)
func (s *ItemUseSystem) applyWisdom(sess *net.Session, player *world.PlayerInfo, durationSec int, sp int16, gfxID int32) {
	alreadyHas := player.HasBuff(handler.SkillStatusWisdomPotion)
	if alreadyHas {
		handler.RemoveBuffAndRevert(player, handler.SkillStatusWisdomPotion, s.deps)
	}

	buff := &world.ActiveBuff{
		SkillID:   handler.SkillStatusWisdomPotion,
		TicksLeft: durationSec * 5,
		DeltaSP:   sp,
	}
	old := player.AddBuff(buff)
	if old != nil {
		s.deps.Skill.RevertBuffStats(player, old)
	}

	player.SP += sp
	player.WisdomSP = sp
	player.WisdomTicks = buff.TicksLeft

	handler.SendWisdomPotionIcon(sess, uint16(durationSec))
	handler.SendPlayerStatus(sess, player)
	s.BroadcastEffect(sess, player, gfxID)
}

// applyBluePotion 套用藍色藥水效果（MP 回復加速）。
// Java ref: Potion.useBluePotion → setSkillEffect(STATUS_BLUE_POTION, time*1000)
func (s *ItemUseSystem) applyBluePotion(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32) {
	handler.RemoveBuffAndRevert(player, handler.SkillStatusBluePotion, s.deps)

	buff := &world.ActiveBuff{
		SkillID:   handler.SkillStatusBluePotion,
		TicksLeft: durationSec * 5,
	}
	player.AddBuff(buff)

	handler.SendBluePotionIcon(sess, uint16(durationSec))
	handler.SendServerMessage(sess, 1007) // "你感覺到魔力恢復速度加快"
	s.BroadcastEffect(sess, player, gfxID)
}

// applyEvaBreath 套用水中呼吸效果。
// Java ref: Potion.useBlessOfEva — 持續時間疊加，上限 7200 秒。
func (s *ItemUseSystem) applyEvaBreath(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32) {
	totalSec := durationSec
	existing := player.GetBuff(handler.SkillStatusUnderwaterBreath)
	if existing != nil {
		remainingSec := existing.TicksLeft / 5
		totalSec += remainingSec
		if totalSec > 7200 {
			totalSec = 7200
		}
		handler.RemoveBuffAndRevert(player, handler.SkillStatusUnderwaterBreath, s.deps)
	}

	buff := &world.ActiveBuff{
		SkillID:   handler.SkillStatusUnderwaterBreath,
		TicksLeft: totalSec * 5,
	}
	player.AddBuff(buff)

	sendEvaBreathIcon(sess, player.CharID, uint16(totalSec))
	s.BroadcastEffect(sess, player, gfxID)
}

// applyThirdSpeed 套用三段加速效果。
// Java ref: Potion.ThirdSpeed → STATUS_THIRD_SPEED (1027)
func (s *ItemUseSystem) applyThirdSpeed(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32) {
	handler.RemoveBuffAndRevert(player, handler.SkillStatusThirdSpeed, s.deps)

	buff := &world.ActiveBuff{
		SkillID:   handler.SkillStatusThirdSpeed,
		TicksLeft: durationSec * 5,
	}
	player.AddBuff(buff)

	sendLiquorPacket(sess, 8)             // 1.15x 角色大小視覺
	handler.SendServerMessage(sess, 1065) // "將發生神秘的奇蹟力量"
	s.BroadcastEffect(sess, player, gfxID)
}

// applyBlindPotion 套用自我施加的致盲詛咒。
// Java ref: Potion.useBlindPotion → CURSE_BLIND。
func (s *ItemUseSystem) applyBlindPotion(sess *net.Session, player *world.PlayerInfo, durationSec int) {
	handler.RemoveBuffAndRevert(player, handler.SkillCurseBlind, s.deps)

	buff := &world.ActiveBuff{
		SkillID:   handler.SkillCurseBlind,
		TicksLeft: durationSec * 5,
	}
	player.AddBuff(buff)

	sendCurseBlindPacket(sess, 1)
}

// ---------- 內部輔助函式 ----------

// checkBraveClassRestrict 檢查玩家職業是否符合勇敢藥水限制。
func checkBraveClassRestrict(classType int16, restrict string) bool {
	switch restrict {
	case "knight":
		return classType == 1
	case "elf":
		return classType == 2
	case "crown":
		return classType == 0
	case "notDKIL":
		return classType != 5 && classType != 6
	case "DKIL":
		return classType == 5 || classType == 6
	default:
		return true
	}
}

// enchantScrollBless 回傳卷軸的祝福分類（0=祝福, 1=普通, 2=詛咒，對齊 Java/DB 慣例）。
// 值來自物品實例的 Bless 欄位（DB character_items.bless），直接使用。
func enchantScrollBless(_ int32, bless int) int {
	return bless
}

// ---------- 萬能藥（Elixir） ----------

const (
	maxElixirStat  int16 = 45 // 單項屬性上限（Java: ConfigAlt.POWERMEDICINE 預設 45）
	maxElixirUsage int16 = 20 // 萬能藥總使用次數上限（Java: ConfigAlt.MEDICINE 預設 20）
)

// elixirStatField 回傳萬能藥 itemID 對應的屬性指標取得函式。
// 非萬能藥時回傳 nil。
func elixirStatField(itemID int32) func(*world.PlayerInfo) *int16 {
	switch itemID {
	case 40033:
		return func(p *world.PlayerInfo) *int16 { return &p.Str }
	case 40034:
		return func(p *world.PlayerInfo) *int16 { return &p.Con }
	case 40035:
		return func(p *world.PlayerInfo) *int16 { return &p.Dex }
	case 40036:
		return func(p *world.PlayerInfo) *int16 { return &p.Intel }
	case 40037:
		return func(p *world.PlayerInfo) *int16 { return &p.Wis }
	case 40038:
		return func(p *world.PlayerInfo) *int16 { return &p.Cha }
	default:
		return nil
	}
}

// useElixir 使用萬能藥：永久 +1 對應屬性。
func (s *ItemUseSystem) useElixir(sess *net.Session, player *world.PlayerInfo, itemID int32, stat *int16) bool {
	// 屬性已達上限
	if *stat >= maxElixirStat {
		handler.SendServerMessage(sess, 79) // 沒有任何事情發生
		return false
	}
	// 使用次數上限
	if player.ElixirStats >= maxElixirUsage {
		handler.SendServerMessage(sess, 79)
		return false
	}

	*stat++
	player.ElixirStats++
	player.Dirty = true

	handler.SendPlayerStatus(sess, player)
	handler.SendAbilityScores(sess, player)

	s.deps.Log.Info(fmt.Sprintf("萬能藥使用  角色=%s  物品=%d  已用=%d/%d",
		player.Name, itemID, player.ElixirStats, maxElixirUsage))

	return true
}

// ---------- 領域專用封包 ----------

func sendHpUpdate(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteD(player.HP)
	w.WriteD(player.MaxHP)
	sess.Send(w.Bytes())
}

func sendMpUpdate(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MANA_POINT)
	w.WriteD(player.MP)
	w.WriteD(player.MaxMP)
	sess.Send(w.Bytes())
}

func sendEffectOnPlayer(sess *net.Session, charID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(charID)
	w.WriteH(uint16(gfxID))
	sess.Send(w.Bytes())
}

// sendSpeedPacket sends S_SkillHaste (opcode 255) — 一段加速。
func sendSpeedPacket(sess *net.Session, charID int32, speedType byte, duration uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SPEED)
	w.WriteD(charID)
	w.WriteC(speedType)
	w.WriteH(duration)
	sess.Send(w.Bytes())
}

// sendBravePacket sends S_SkillBrave (opcode 67) — 二段加速。
func sendBravePacket(sess *net.Session, charID int32, braveType byte, duration uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SKILLBRAVE)
	w.WriteD(charID)
	w.WriteC(braveType)
	w.WriteH(duration)
	w.WriteH(0) // padding — Java S_SkillBrave 固定尾碼
	sess.Send(w.Bytes())
}

// sendEvaBreathIcon sends S_SkillIconBlessOfEva (S_PacketBox sub 44)。
func sendEvaBreathIcon(sess *net.Session, charID int32, timeSec uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(44)
	w.WriteD(charID)
	w.WriteH(timeSec)
	sess.Send(w.Bytes())
}

// sendLiquorPacket sends S_DRUNKEN (opcode 103) — 角色大小變化。
func sendLiquorPacket(sess *net.Session, liquorType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_DRUNKEN)
	w.WriteC(liquorType)
	sess.Send(w.Bytes())
}

// sendCurseBlindPacket sends S_CurseBlind (S_PacketBox sub 45)。
func sendCurseBlindPacket(sess *net.Session, blindType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(45)
	w.WriteC(blindType)
	sess.Send(w.Bytes())
}

// ConsumeBoxItem 消耗 1 個寶箱物品。
func (s *ItemUseSystem) ConsumeBoxItem(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem) {
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, invItem)
	}
	player.Dirty = true
}

// GiveBoxReward 給予開箱獎勵物品。
func (s *ItemUseSystem) GiveBoxReward(sess *net.Session, player *world.PlayerInfo, getItemID int32, minCount, maxCount int32, bless, enchant int8, broadcast bool) {
	itemInfo := s.deps.Items.Get(getItemID)
	if itemInfo == nil {
		s.deps.Log.Warn("開箱物品不存在", zap.Int32("itemID", getItemID))
		return
	}

	// 決定數量
	count := minCount
	if maxCount > minCount {
		count = minCount + rand.Int31n(maxCount-minCount+1)
	}
	if count < 1 {
		count = 1
	}

	// 決定祝福狀態：-1=使用模板預設值
	itemBless := byte(itemInfo.Bless)
	if bless >= 0 {
		itemBless = byte(bless)
	}

	if s.deps.ItemCreate != nil {
		opts := ItemCreateOptions{}
		if enchant > 0 {
			opts.EnchantLvl = int8(enchant)
		}
		if bless >= 0 {
			opts.BlessSet = true
			opts.Bless = itemBless
		}
		if creator, ok := s.deps.ItemCreate.(interface {
			GiveItemWithOptions(sess *net.Session, player *world.PlayerInfo, itemID, count int32, opts ItemCreateOptions) (*world.InvItem, bool)
		}); ok {
			if _, ok := creator.GiveItemWithOptions(sess, player, getItemID, count, opts); !ok {
				return
			}
			if broadcast {
				s.broadcastBoxDrop(player.Name, itemInfo.Name, count)
			}
			return
		}
		if bless < 0 && enchant <= 0 {
			if _, ok := s.deps.ItemCreate.GiveItem(sess, player, getItemID, count); !ok {
				return
			}
			if broadcast {
				s.broadcastBoxDrop(player.Name, itemInfo.Name, count)
			}
			return
		}
	}

	stackable := itemInfo.Stackable || getItemID == world.AdenaItemID

	// 檢查是否已有同物品可堆疊
	existing := player.Inv.FindByItemID(getItemID)
	wasExisting := existing != nil && stackable

	invItem := player.Inv.AddItem(getItemID, count, itemInfo.Name, itemInfo.InvGfx,
		itemInfo.Weight, stackable, itemBless)
	invItem.UseType = itemInfo.UseTypeID
	invItem.Identified = true
	if enchant > 0 {
		invItem.EnchantLvl = int8(enchant)
	}

	if wasExisting {
		handler.SendItemCountUpdate(sess, invItem)
	} else {
		handler.SendAddItem(sess, invItem)
	}
	handler.SendWeightUpdate(sess, player)

	// 全服公告
	if broadcast {
		s.broadcastBoxDrop(player.Name, itemInfo.Name, count)
	}
}

// broadcastBoxDrop 全服公告開箱獲得物品。
func (s *ItemUseSystem) broadcastBoxDrop(playerName, itemName string, count int32) {
	displayName := itemName
	if count > 1 {
		displayName = fmt.Sprintf("%s(%d)", itemName, count)
	}
	s.deps.World.AllPlayers(func(p *world.PlayerInfo) {
		if p.Session != nil {
			handler.SendServerMessageArgs(p.Session, 166, playerName, displayName)
		}
	})
}

// ActivateVIP 啟用 VIP 物品效果（同 type 互斥）。
func (s *ItemUseSystem) ActivateVIP(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, vip *data.ItemVIP) {
	if player.ActiveVIP == nil {
		player.ActiveVIP = make(map[int]int32)
	}

	// 同 type 互斥：移除舊的 VIP 效果
	if oldObjID, ok := player.ActiveVIP[vip.Type]; ok {
		oldItem := player.Inv.FindByObjectID(oldObjID)
		if oldItem != nil {
			if oldVIP := s.deps.ItemVIPs.Get(oldItem.ItemID); oldVIP != nil {
				s.revertVIPStats(player, oldVIP)
			}
		}
		delete(player.ActiveVIP, vip.Type)
	}

	// 套用新 VIP 屬性
	s.applyVIPStats(player, vip)
	player.ActiveVIP[vip.Type] = invItem.ObjectID
	player.Dirty = true

	// 發送屬性更新封包
	s.sendVIPStatusUpdates(sess, player, vip)

	// 播放特效
	if vip.GfxID > 0 {
		handler.SendSkillEffect(sess, player.CharID, vip.GfxID)
	}

	handler.SendSystemMessage(sess, "VIP 效果已啟用。")
}

// applyVIPStats 套用 VIP 屬性加成到 PlayerInfo。
func (s *ItemUseSystem) applyVIPStats(p *world.PlayerInfo, vip *data.ItemVIP) {
	p.Str += vip.AddStr
	p.Dex += vip.AddDex
	p.Con += vip.AddCon
	p.Intel += vip.AddInt
	p.Wis += vip.AddWis
	p.Cha += vip.AddCha
	p.AC -= vip.AddAC
	p.MaxHP += vip.AddHP
	p.MaxMP += vip.AddMP
	p.HPR += vip.AddHPR
	p.MPR += vip.AddMPR
	p.DmgMod += vip.AddDmg
	p.HitMod += vip.AddHit
	p.BowDmgMod += vip.AddBowDmg
	p.BowHitMod += vip.AddBowHit
	p.MR += vip.AddMR
	p.SP += vip.AddSP
	p.FireRes += vip.AddFire
	p.WaterRes += vip.AddWater
	p.WindRes += vip.AddWind
	p.EarthRes += vip.AddEarth
	p.RegistStun += vip.AddStun
	p.RegistStone += vip.AddStone
	p.RegistSleep += vip.AddSleep
	p.RegistFreeze += vip.AddFreeze
	p.RegistSustain += vip.AddSustain
	p.RegistBlind += vip.AddBlind
}

// revertVIPStats 移除 VIP 屬性加成。
func (s *ItemUseSystem) revertVIPStats(p *world.PlayerInfo, vip *data.ItemVIP) {
	p.Str -= vip.AddStr
	p.Dex -= vip.AddDex
	p.Con -= vip.AddCon
	p.Intel -= vip.AddInt
	p.Wis -= vip.AddWis
	p.Cha -= vip.AddCha
	p.AC += vip.AddAC
	p.MaxHP -= vip.AddHP
	p.MaxMP -= vip.AddMP
	p.HPR -= vip.AddHPR
	p.MPR -= vip.AddMPR
	p.DmgMod -= vip.AddDmg
	p.HitMod -= vip.AddHit
	p.BowDmgMod -= vip.AddBowDmg
	p.BowHitMod -= vip.AddBowHit
	p.MR -= vip.AddMR
	p.SP -= vip.AddSP
	p.FireRes -= vip.AddFire
	p.WaterRes -= vip.AddWater
	p.WindRes -= vip.AddWind
	p.EarthRes -= vip.AddEarth
	p.RegistStun -= vip.AddStun
	p.RegistStone -= vip.AddStone
	p.RegistSleep -= vip.AddSleep
	p.RegistFreeze -= vip.AddFreeze
	p.RegistSustain -= vip.AddSustain
	p.RegistBlind -= vip.AddBlind
}

// sendVIPStatusUpdates 根據 VIP 屬性變化發送對應的更新封包。
func (s *ItemUseSystem) sendVIPStatusUpdates(sess *net.Session, p *world.PlayerInfo, vip *data.ItemVIP) {
	// 六維 + HP/MP/AC → S_OwnCharStatus
	if vip.AddStr != 0 || vip.AddDex != 0 || vip.AddCon != 0 ||
		vip.AddInt != 0 || vip.AddWis != 0 || vip.AddCha != 0 ||
		vip.AddHP != 0 || vip.AddMP != 0 || vip.AddAC != 0 {
		handler.SendPlayerStatus(sess, p)
	}

	// AC + 元素抗性 → S_OwnCharAttrDef
	if vip.AddAC != 0 || vip.AddFire != 0 || vip.AddWater != 0 ||
		vip.AddWind != 0 || vip.AddEarth != 0 {
		handler.SendAbilityScores(sess, p)
	}

	// SP + MR → S_SPMR
	if vip.AddSP != 0 || vip.AddMR != 0 {
		handler.SendMagicStatus(sess, byte(p.SP), uint16(p.MR))
	}

	// HP/MP 上限變化
	if vip.AddHP != 0 {
		handler.SendHpUpdate(sess, p)
	}
	if vip.AddMP != 0 {
		handler.SendMpUpdate(sess, p)
	}
}

// ---------- 魔杖使用 ----------

// 創造怪物魔杖隨機召喚怪物列表（Java: Create_Monster_Magic_Wand.java）
var wandMonsterIDs = [...]int32{
	45008, 45140, 45016, 45021, 45025,
	45033, 45099, 45147, 45123, 45130,
	45046, 45092, 45138, 45098, 45127,
	45143, 45149, 45171, 45040, 45155,
	45192, 45173, 45213, 45079, 45144,
}

// UseWand 處理魔杖使用邏輯。根據 itemID 分派到對應的魔杖效果。
// targetObjID/targetX/targetY 僅在 spell_long 類魔杖（烏木、楓木）時有值。
func (s *ItemUseSystem) UseWand(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem,
	targetObjID int32, targetX, targetY int16) {

	s.deps.Log.Info("UseWand",
		zap.Int32("item_id", invItem.ItemID),
		zap.Int16("charge", invItem.ChargeCount),
		zap.Int32("target", targetObjID),
	)

	// 充能次數檢查
	if invItem.ChargeCount <= 0 {
		handler.SendServerMessage(sess, 79) // 沒有任何事情發生
		return
	}

	switch invItem.ItemID {
	case 40006, 140006: // 創造怪物魔杖（Java: Create_Monster_Magic_Wand）
		s.useCreateMonsterWand(sess, player, invItem)
	case 40007: // 烏木魔杖 — 閃電（Java: Lightning_Magic_Wand）
		s.useLightningWand(sess, player, invItem, targetObjID, targetX, targetY)
	case 40008, 140008: // 楓木魔杖 — 變身（Java: Poly_Magic_Wand）
		s.usePolyMorphWand(sess, player, invItem, targetObjID)
	default:
		handler.SendServerMessage(sess, 79)
	}
}

// useCreateMonsterWand 創造怪物魔杖 — 在玩家位置隨機召喚一隻怪物。
func (s *ItemUseSystem) useCreateMonsterWand(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem) {
	// 廣播魔杖使用動作（Java: ACTION_Wand = 17）
	nearby := s.itemUseViewers(player, 0)
	actionData := handler.BuildActionGfx(player.CharID, 17)
	handler.BroadcastToPlayers(nearby, actionData)

	// 隨機選擇怪物
	npcID := wandMonsterIDs[rand.Intn(len(wandMonsterIDs))]
	tmpl := s.deps.Npcs.Get(npcID)
	if tmpl == nil {
		s.deps.Log.Warn("創造怪物魔杖：未知 NPC", zap.Int32("npc_id", npcID))
		handler.SendServerMessage(sess, 79)
		return
	}

	// 在玩家位置附近生成怪物
	spawnX := player.X + int32(rand.Intn(3)) - 1
	spawnY := player.Y + int32(rand.Intn(3)) - 1

	// 取得動作速度
	atkSpeed := tmpl.AtkSpeed
	moveSpeed := tmpl.PassiveSpeed
	if s.deps.SprTable != nil {
		gfx := int(tmpl.GfxID)
		if tmpl.AtkSpeed != 0 {
			if v := s.deps.SprTable.GetAttackSpeed(gfx, data.ActAttack); v > 0 {
				atkSpeed = int16(v)
			}
		}
		if tmpl.PassiveSpeed != 0 {
			if v := s.deps.SprTable.GetMoveSpeed(gfx, data.ActWalk); v > 0 {
				moveSpeed = int16(v)
			}
		}
	}

	mob := &world.NpcInfo{
		ID:                world.NextNpcID(),
		NpcID:             tmpl.NpcID,
		Impl:              tmpl.Impl,
		GfxID:             tmpl.GfxID,
		Name:              tmpl.Name,
		NameID:            tmpl.NameID,
		Level:             tmpl.Level,
		X:                 spawnX,
		Y:                 spawnY,
		MapID:             player.MapID,
		Heading:           int16(rand.Intn(8)),
		HP:                tmpl.HP,
		MaxHP:             tmpl.HP,
		MP:                tmpl.MP,
		MaxMP:             tmpl.MP,
		AC:                tmpl.AC,
		STR:               tmpl.STR,
		DEX:               tmpl.DEX,
		Intel:             tmpl.INT,
		Exp:               tmpl.Exp,
		Lawful:            tmpl.Lawful,
		Size:              tmpl.Size,
		MR:                tmpl.MR,
		Undead:            tmpl.Undead,
		UndeadType:        tmpl.UndeadType,
		TurnUndeadable:    tmpl.EffectiveTurnUndeadable(),
		TurnUndeadableSet: true,
		Hard:              tmpl.Hard,
		CantResurrect:     tmpl.CantResurrect,
		Agro:              tmpl.Agro,
		Family:            tmpl.Family,
		AgroFamily:        tmpl.AgroFamily,
		AtkDmg:            int32(tmpl.Level) + int32(tmpl.STR)/3,
		Ranged:            tmpl.Ranged,
		AtkSpeed:          atkSpeed,
		AtkMagicSpeed:     tmpl.AtkMagicSpeed,
		SubMagicSpeed:     tmpl.SubMagicSpeed,
		MoveSpeed:         moveSpeed,
		PoisonAtk:         tmpl.PoisonAtk,
		FireRes:           tmpl.FireRes,
		WaterRes:          tmpl.WaterRes,
		WindRes:           tmpl.WindRes,
		EarthRes:          tmpl.EarthRes,
		WeakAttr:          tmpl.WeakAttr,
		WeaponRequired:    tmpl.WeaponRequired,
		SpawnX:            spawnX,
		SpawnY:            spawnY,
		SpawnMapID:        player.MapID,
	}

	s.deps.World.AddNpc(mob)
	if s.deps.MapData != nil {
		s.deps.MapData.SetImpassable(mob.MapID, mob.X, mob.Y, true)
	}

	// 通知附近玩家顯示新 NPC
	for _, viewer := range nearby {
		sendNpcPack(viewer.Session, mob)
	}

	// 扣減充能次數
	invItem.ChargeCount--

	if invItem.ChargeCount <= 0 {
		// 次數用盡 → 刪除物品（Java: Create_Monster_Magic_Wand deleteItem）
		player.Inv.RemoveItem(invItem.ObjectID, 1)
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	}
	// 注意：不發送 S_AddItem 更新充能 — S_AddItem 會在客戶端新增重複物品

	handler.SendWeightUpdate(sess, player)
	player.Dirty = true
}

// ==================== 烏木魔杖（閃電）====================

// useLightningWand 烏木魔杖 — 對目標施放閃電傷害。
// Java: Lightning_Magic_Wand.java — dmg = rand(-5..5) + INT，最低 1。
func (s *ItemUseSystem) useLightningWand(sess *net.Session, player *world.PlayerInfo,
	invItem *world.InvItem, targetObjID int32, targetX, targetY int16) {

	const lightningGfx = 6598 // 閃電動畫 GFX ID

	nearby := s.itemUseViewers(player, 0)
	sendWandAction := func() {
		actionData := handler.BuildActionGfx(player.CharID, 17)
		handler.BroadcastToPlayers(nearby, actionData)
	}
	defer func() {
		// Java execute 即使 doWandAction 因目標/LOS 早退，仍會扣一次 chargeCount。
		invItem.ChargeCount--
		player.Dirty = true
	}()

	// 傷害公式（Java: random.nextInt(11) - 5 + INT）
	dmg := int32(rand.Intn(11)-5) + int32(player.Intel)
	if dmg < 1 {
		dmg = 1
	}

	// 查找目標 — NPC 或玩家
	npc := s.deps.World.GetNpc(targetObjID)
	if npc != nil && !npc.Dead {
		if s.itemUseLineOfSightBlockedLikeJava(player, npc.X, npc.Y) {
			return
		}
		// 對 NPC 施放閃電
		sendWandAction()
		effectData := handler.BuildSkillEffect(npc.ID, lightningGfx)
		handler.BroadcastToPlayers(nearby, effectData)

		damageBlocked := npcDamageBlockedBySkm0LikeJava(npc)
		if !damageBlocked {
			npc.HP -= dmg
			if npc.HP < 0 {
				npc.HP = 0
			}

			// 累加仇恨
			AddPlayerHateLikeJava(s.deps.World, npc, player, dmg)
		}

		// 受傷動畫
		dmgData := handler.BuildActionGfx(npc.ID, 2) // ACTION_Damage = 2
		handler.BroadcastToPlayers(nearby, dmgData)

		if !damageBlocked {
			// 血量更新
			hpRatio := int16(0)
			if npc.MaxHP > 0 {
				hpRatio = int16((npc.HP * 100) / npc.MaxHP)
			}
			for _, viewer := range nearby {
				handler.SendHpMeter(viewer.Session, npc.ID, hpRatio)
			}

			// 死亡檢查
			if npc.HP <= 0 {
				s.deps.Combat.HandleNpcDeath(npc, player, nearby)
			}
		}
	} else {
		// 嘗試查找玩家目標（Java: findObject 通用查找 → L1PcInstance 分支）
		targetPlayer := s.deps.World.GetByCharID(targetObjID)
		if targetPlayer != nil && targetPlayer.CharID != player.CharID && !targetPlayer.Dead {
			if s.itemUseLineOfSightBlockedLikeJava(player, targetPlayer.X, targetPlayer.Y) {
				return
			}
			// 安全區檢查
			if s.deps.MapData != nil && s.deps.MapData.IsSafetyZone(targetPlayer.MapID, targetPlayer.X, targetPlayer.Y) {
				// 安全區內不可攻擊
			} else {
				sendWandAction()
				// 閃電效果
				effectData := handler.BuildSkillEffect(targetPlayer.CharID, lightningGfx)
				handler.BroadcastToPlayers(nearby, effectData)

				// 扣 HP
				newHP := targetPlayer.HP - dmg
				if newHP < 1 {
					newHP = 1 // 魔杖不致死（簡化處理）
				}
				targetPlayer.HP = newHP
				handler.SendHpUpdate(targetPlayer.Session, targetPlayer)

				// 受傷動畫
				dmgData := handler.BuildActionGfx(targetPlayer.CharID, 2)
				handler.BroadcastToPlayers(nearby, dmgData)

				targetPlayer.Dirty = true
			}
		}
		// else: 目標不存在 → 3.80C 客戶端自行播放投射物動畫（無需伺服器處理）
	}
}

// ==================== 楓木魔杖（變身）====================

// 變身魔杖隨機怪物 GFX 列表（Java: Poly_Magic_Wand.java polyId[]）
var wandPolyGfxIDs = [...]int32{
	29, 945, 947, 979, 1037, 1039,
	3860, 3861, 3862, 3863, 3864, 3865,
	3904, 3906,
	95, 146, 2374, 2376, 2377, 2378,
	3866, 3867, 3868, 3869, 3870, 3871,
	3872, 3873, 3874, 3875, 3876,
}

// 禁止變身的 Boss NPC ID（Java: Poly_Magic_Wand.polyAction）
var polyBannedNpcIDs = map[int32]bool{
	45464: true, 45473: true, 45488: true, 45497: true, // 四色龍
	45458: true, // 德雷克
	45752: true, // 炎魔
	45492: true, // 庫曼
	46035: true, // 殭屍王
	99006: true, // 牛鬼
}

// 禁止使用變身魔杖的水域地圖（Java: Poly_Magic_Wand.execute）
var polyBannedMaps = map[int16]bool{
	63: true, 552: true, 555: true, 557: true, 558: true, 779: true,
}

// usePolyMorphWand 楓木魔杖 — 對目標施放變身。
// Java: Poly_Magic_Wand.java — 成功率 = 3*(攻方LV-目標LV) + 100 - 目標MR。
func (s *ItemUseSystem) usePolyMorphWand(sess *net.Session, player *world.PlayerInfo,
	invItem *world.InvItem, targetObjID int32) {

	// 地圖限制（水域地圖不可使用）
	if polyBannedMaps[player.MapID] {
		handler.SendServerMessage(sess, 563) // 這裡無法使用。
		return
	}

	// 廣播魔杖使用動作（Java: ACTION_Wand = 17）
	nearby := s.itemUseViewers(player, 0)
	actionData := handler.BuildActionGfx(player.CharID, 17)
	handler.BroadcastToPlayers(nearby, actionData)

	// 隨機選擇變身 GFX
	polyGfx := wandPolyGfxIDs[rand.Intn(len(wandPolyGfxIDs))]

	// 查找目標
	npc := s.deps.World.GetNpc(targetObjID)
	if npc != nil && !npc.Dead {
		// 對 NPC 施放變身
		if npc.Level >= 50 {
			handler.SendServerMessage(sess, 79)
			return
		}
		if polyBannedNpcIDs[npc.NpcID] {
			handler.SendServerMessage(sess, 79)
			return
		}

		// 成功率（Java: probability = 3*(攻LV-防LV) + 100 - 防MR）
		prob := 3*int(player.Level-npc.Level) + 100 - int(npc.MR)
		if rand.Intn(100)+1 > prob {
			handler.SendServerMessage(sess, 79) // 失敗
		} else {
			// 變身成功 — NPC 改變外觀
			npc.GfxID = polyGfx

			// 廣播外觀變化
			for _, viewer := range nearby {
				handler.SendNpcPack(viewer.Session, npc)
			}
		}
	} else {
		// 對玩家施放變身（只能對自己）
		targetPlayer := s.deps.World.GetByCharID(targetObjID)
		if targetPlayer == nil || targetPlayer.CharID != player.CharID {
			handler.SendServerMessage(sess, 79)
			return
		}

		// 使用變身系統
		if s.deps.Polymorph != nil {
			s.deps.Polymorph.DoPoly(player, polyGfx, 1800, data.PolyCauseMagic) // 30 分鐘
		}
	}

	// 扣減充能次數（Java: 只更新 chargeCount，不刪除物品）
	// 注意：不發送 S_AddItem — 會在客戶端新增重複物品。充能數僅伺服器端追蹤。
	invItem.ChargeCount--
	player.Dirty = true
}

// ========================================================================
//  料理 Buff 系統
// ========================================================================

// cookingBuff 定義單一料理的 buff 效果。
// Java: L1Cooking.eatCooking() — 根據 type 決定屬性加成。
type cookingBuff struct {
	SkillID    int32 // buff 追蹤用 skill ID（3000-3051）
	Duration   int   // 持續時間（秒）
	DeltaAC    int16
	DeltaMaxHP int32
	DeltaMaxMP int32
	DeltaMR    int16
	DeltaSP    int16
	DeltaHPR   int16
	DeltaMPR   int16
	DeltaFire  int16
	DeltaWater int16
	DeltaWind  int16
	DeltaEarth int16
	CookType   int // 料理 type（用於客戶端圖示封包）
}

// cookingBuffMap 料理物品 ID → buff 效果映射。
// Java: L1Cooking.useCookingItem() switch + eatCooking()。
// Lv1 料理持續 900 秒，Lv4 持續 1800 秒。僅收錄有 buff 效果的料理。
var cookingBuffMap = map[int32]cookingBuff{
	// --- Lv1 普通 ---
	41277: {SkillID: 3000, Duration: 900, DeltaFire: 10, DeltaWater: 10, DeltaWind: 10, DeltaEarth: 10, CookType: 0},
	41278: {SkillID: 3001, Duration: 900, DeltaMaxHP: 30, CookType: 1},
	41280: {SkillID: 3003, Duration: 900, DeltaAC: -1, CookType: 3},
	41281: {SkillID: 3004, Duration: 900, DeltaMaxMP: 20, CookType: 4},
	41283: {SkillID: 3006, Duration: 900, DeltaMR: 5, CookType: 6},
	// --- Lv1 特別 ---
	41285: {SkillID: 3008, Duration: 900, DeltaFire: 10, DeltaWater: 10, DeltaWind: 10, DeltaEarth: 10, CookType: 0},
	41286: {SkillID: 3009, Duration: 900, DeltaMaxHP: 30, CookType: 1},
	41288: {SkillID: 3011, Duration: 900, DeltaAC: -1, CookType: 3},
	41289: {SkillID: 3012, Duration: 900, DeltaMaxMP: 20, CookType: 4},
	41291: {SkillID: 3014, Duration: 900, DeltaMR: 5, CookType: 6},
	// --- Lv2 普通 ---
	49050: {SkillID: 3017, Duration: 900, DeltaMaxHP: 30, DeltaMaxMP: 30, CookType: 17},
	49051: {SkillID: 3018, Duration: 900, DeltaAC: -2, CookType: 18},
	49054: {SkillID: 3021, Duration: 900, DeltaMR: 10, CookType: 21},
	49055: {SkillID: 3022, Duration: 900, DeltaSP: 1, CookType: 22},
	// --- Lv2 特別 ---
	49058: {SkillID: 3025, Duration: 900, DeltaMaxHP: 30, DeltaMaxMP: 30, CookType: 17},
	49059: {SkillID: 3026, Duration: 900, DeltaAC: -2, CookType: 18},
	49062: {SkillID: 3029, Duration: 900, DeltaMR: 10, CookType: 21},
	49063: {SkillID: 3030, Duration: 900, DeltaSP: 1, CookType: 22},
	// --- Lv3 普通 ---
	49245: {SkillID: 3033, Duration: 900, DeltaMaxHP: 50, DeltaMaxMP: 50, CookType: 38},
	49247: {SkillID: 3035, Duration: 900, DeltaAC: -3, CookType: 40},
	49248: {SkillID: 3036, Duration: 900, DeltaMR: 15, DeltaFire: 10, DeltaWater: 10, DeltaWind: 10, DeltaEarth: 10, CookType: 41},
	49249: {SkillID: 3037, Duration: 900, DeltaSP: 2, CookType: 42},
	49250: {SkillID: 3038, Duration: 900, DeltaMaxHP: 30, CookType: 43},
	// --- Lv3 特別 ---
	49253: {SkillID: 3041, Duration: 900, DeltaMaxHP: 50, DeltaMaxMP: 50, CookType: 38},
	49255: {SkillID: 3043, Duration: 900, DeltaAC: -3, CookType: 40},
	49256: {SkillID: 3044, Duration: 900, DeltaMR: 15, DeltaFire: 10, DeltaWater: 10, DeltaWind: 10, DeltaEarth: 10, CookType: 41},
	49257: {SkillID: 3045, Duration: 900, DeltaSP: 2, CookType: 42},
	49258: {SkillID: 3046, Duration: 900, DeltaMaxHP: 30, CookType: 43},
	// --- Lv4 ---
	49825: {SkillID: 3048, Duration: 1800, DeltaMR: 10, DeltaFire: 10, DeltaWater: 10, DeltaWind: 10, DeltaEarth: 10, DeltaHPR: 2, DeltaMPR: 2, CookType: 157},
	49826: {SkillID: 3049, Duration: 1800, DeltaMR: 10, DeltaFire: 10, DeltaWater: 10, DeltaWind: 10, DeltaEarth: 10, DeltaHPR: 2, DeltaMPR: 2, CookType: 158},
	49827: {SkillID: 3050, Duration: 1800, DeltaSP: 2, DeltaMR: 10, DeltaFire: 10, DeltaWater: 10, DeltaWind: 10, DeltaEarth: 10, DeltaHPR: 2, DeltaMPR: 3, CookType: 159},
}

// applyCookingBuff 套用料理 buff。
// Java: L1Cooking.eatCooking() — 移除舊料理 buff → 套用新 buff → 發送屬性更新封包 + 圖示。
func (s *ItemUseSystem) applyCookingBuff(sess *net.Session, player *world.PlayerInfo, cb cookingBuff) {
	// 移除舊料理 buff（同時只能有一個普通料理 buff）
	if player.CookingID != 0 {
		handler.RemoveBuffAndRevert(player, player.CookingID, s.deps)
	}

	// 建立新 buff
	buff := &world.ActiveBuff{
		SkillID:       cb.SkillID,
		TicksLeft:     cb.Duration * 5, // 秒 → ticks（5 ticks/秒）
		DeltaAC:       cb.DeltaAC,
		DeltaMaxHP:    cb.DeltaMaxHP,
		DeltaMaxMP:    cb.DeltaMaxMP,
		DeltaMR:       cb.DeltaMR,
		DeltaSP:       cb.DeltaSP,
		DeltaHPR:      cb.DeltaHPR,
		DeltaMPR:      cb.DeltaMPR,
		DeltaFireRes:  cb.DeltaFire,
		DeltaWaterRes: cb.DeltaWater,
		DeltaWindRes:  cb.DeltaWind,
		DeltaEarthRes: cb.DeltaEarth,
	}

	old := player.AddBuff(buff)
	if old != nil {
		s.deps.Skill.RevertBuffStats(player, old)
	}

	// 套用屬性加成
	player.AC += cb.DeltaAC
	player.MaxHP += cb.DeltaMaxHP
	player.MaxMP += cb.DeltaMaxMP
	player.MR += cb.DeltaMR
	player.SP += cb.DeltaSP
	player.HPR += cb.DeltaHPR
	player.MPR += cb.DeltaMPR
	player.FireRes += cb.DeltaFire
	player.WaterRes += cb.DeltaWater
	player.WindRes += cb.DeltaWind
	player.EarthRes += cb.DeltaEarth

	player.CookingID = cb.SkillID
	player.Dirty = true

	// 發送屬性更新封包
	if cb.DeltaMaxHP != 0 {
		handler.SendHpUpdate(sess, player)
	}
	if cb.DeltaMaxMP != 0 {
		handler.SendMpUpdate(sess, player)
	}
	if cb.DeltaAC != 0 || cb.DeltaFire != 0 || cb.DeltaWater != 0 || cb.DeltaWind != 0 || cb.DeltaEarth != 0 {
		handler.SendAbilityScores(sess, player)
	}
	if cb.DeltaSP != 0 || cb.DeltaMR != 0 {
		handler.SendMagicStatus(sess, byte(player.SP), uint16(player.MR))
	}

	// 發送料理圖示
	handler.SendCookingIcon(sess, player, cb.CookType, int16(cb.Duration))
}
