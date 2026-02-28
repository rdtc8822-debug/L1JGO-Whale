package system

import (
	"fmt"
	"math/rand"
	"strconv"

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

// ---------- 消耗品使用（藥水、食物） ----------

// UseConsumable 處理消耗品使用。
// 藥水效果定義在 Lua (scripts/item/potions.lua)。
func (s *ItemUseSystem) UseConsumable(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) {
	consumed := false

	pot := s.deps.Scripting.GetPotionEffect(int(invItem.ItemID))
	if pot != nil {
		// DECAY_POTION check (Java: skill 71) — 封鎖所有可飲用藥水。
		// Message 698: "喉嚨灼熱，無法喝東西"
		if player.HasBuff(handler.SkillDecayPotion) {
			handler.SendServerMessage(sess, 698)
			return
		}

		switch pot.Type {
		case "heal":
			// Java ref: Potion.UseHeallingPotion — 總是消耗、總是播放音效/訊息。
			// 高斯隨機 ±20%: healHp *= (gaussian/5 + 1)
			if pot.Amount > 0 {
				healAmt := float64(pot.Amount) * (rand.NormFloat64()/5.0 + 1.0)
				if healAmt < 1 {
					healAmt = 1
				}
				if player.HP < player.MaxHP {
					player.HP += int16(healAmt)
					if player.HP > player.MaxHP {
						player.HP = player.MaxHP
					}
					sendHpUpdate(sess, player)
				}
				gfx := int32(pot.GfxID)
				if gfx == 0 {
					gfx = 189 // 預設小藍光
				}
				s.BroadcastEffect(sess, player, gfx)
				handler.SendServerMessage(sess, 77) // "你覺得舒服多了"
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
					player.MP += int16(mpAmt)
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
			handler.SendFoodUpdate(sess, player.Food)
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
	"魔法書(",       // Wizard / common
	"技術書(",       // Knight
	"精靈水晶(",     // Elf
	"黑暗精靈水晶(", // Dark Elf
	"龍騎士書板(",   // Dragon Knight
	"記憶水晶(",     // Illusionist
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
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
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
		sendEffectOnPlayer(sess, player.CharID, 169)
		bkNearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		for _, viewer := range bkNearby {
			sendEffectOnPlayer(viewer.Session, player.CharID, 169)
		}

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
		sendEffectOnPlayer(sess, player.CharID, 169)
		rdNearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		for _, viewer := range rdNearby {
			sendEffectOnPlayer(viewer.Session, player.CharID, 169)
		}

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

	// 取得重生點
	loc := s.deps.Scripting.GetRespawnLocation(int(player.MapID))
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

	// 出發特效（Java: S_SkillSound(169)）
	sendEffectOnPlayer(sess, player.CharID, 169)
	oldNearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, viewer := range oldNearby {
		sendEffectOnPlayer(viewer.Session, player.CharID, 169)
	}

	// 傳送到重生點
	handler.TeleportPlayer(sess, player, int32(loc.X), int32(loc.Y), int16(loc.Map), 5, s.deps)

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

	// 出發特效
	sendEffectOnPlayer(sess, player.CharID, 169)
	oldNearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, viewer := range oldNearby {
		sendEffectOnPlayer(viewer.Session, player.CharID, 169)
	}

	// 傳送到指定目的地
	handler.TeleportPlayer(sess, player, itemInfo.LocX, itemInfo.LocY, itemInfo.LocMapID, 5, s.deps)

	s.deps.Log.Info(fmt.Sprintf("指定傳送  角色=%s  道具=%s  目標=(%d,%d) 地圖=%d",
		player.Name, itemInfo.Name, itemInfo.LocX, itemInfo.LocY, itemInfo.LocMapID))
}

// ---------- 掉落系統 ----------

// GiveDrops 為擊殺的 NPC 擲骰掉落物品並加入擊殺者背包。
func (s *ItemUseSystem) GiveDrops(killer *world.PlayerInfo, npcID int32) {
	if s.deps.Drops == nil {
		return
	}
	dropList := s.deps.Drops.Get(npcID)
	if dropList == nil {
		return
	}

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

		if killer.Inv.IsFull() {
			break
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

		itemInfo := s.deps.Items.Get(drop.ItemID)
		if itemInfo == nil {
			continue
		}

		stackable := itemInfo.Stackable || drop.ItemID == world.AdenaItemID
		existing := killer.Inv.FindByItemID(drop.ItemID)
		wasExisting := existing != nil && stackable

		item := killer.Inv.AddItem(
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
			handler.SendItemCountUpdate(killer.Session, item)
		} else {
			handler.SendAddItem(killer.Session, item)
		}
		handler.SendWeightUpdate(killer.Session, killer)

		// 通知玩家掉落
		if drop.ItemID == world.AdenaItemID {
			msg := fmt.Sprintf("獲得 %d 金幣", qty)
			handler.SendGlobalChat(killer.Session, 9, msg)
		} else {
			name := itemInfo.Name
			if drop.EnchantLevel > 0 {
				name = fmt.Sprintf("+%d %s", drop.EnchantLevel, name)
			}
			if qty > 1 {
				msg := fmt.Sprintf("獲得 %s (%d)", name, qty)
				handler.SendGlobalChat(killer.Session, 9, msg)
			} else {
				msg := fmt.Sprintf("獲得 %s", name)
				handler.SendGlobalChat(killer.Session, 9, msg)
			}
		}
	}
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
		handler.RevertBuffStats(player, old)
	}

	player.MoveSpeed = 1
	player.HasteTicks = buff.TicksLeft

	sendSpeedPacket(sess, player.CharID, 1, uint16(durationSec))
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendSpeedPacket(other.Session, player.CharID, 1, 0)
	}
	s.BroadcastEffect(sess, player, gfxID)
}

// BroadcastEffect 向自己和附近玩家廣播特效。
func (s *ItemUseSystem) BroadcastEffect(sess *net.Session, player *world.PlayerInfo, gfxID int32) {
	sendEffectOnPlayer(sess, player.CharID, gfxID)
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
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
		handler.RevertBuffStats(player, old)
	}

	player.BraveSpeed = braveType
	player.BraveTicks = buff.TicksLeft

	sendBravePacket(sess, player.CharID, braveType, uint16(durationSec))
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
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
		handler.RevertBuffStats(player, old)
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

	sendLiquorPacket(sess, 8) // 1.15x 角色大小視覺
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

// enchantScrollBless 根據物品 ID 判斷正確的祝福分類。
// 40074（防具）和 40087（武器）在 YAML 中誤標為 bless:1，實際為普通卷軸。
func enchantScrollBless(itemID int32, yamlBless int) int {
	if yamlBless == 2 {
		return 2 // 詛咒卷軸
	}
	if itemID == 40074 || itemID == 40087 {
		return 0
	}
	return yamlBless
}

// ---------- 領域專用封包 ----------

func sendHpUpdate(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteH(uint16(player.HP))
	w.WriteH(uint16(player.MaxHP))
	sess.Send(w.Bytes())
}

func sendMpUpdate(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MANA_POINT)
	w.WriteH(uint16(player.MP))
	w.WriteH(uint16(player.MaxMP))
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
