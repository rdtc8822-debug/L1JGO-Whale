package system

import (
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/core/event"
	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

// CombatSystem 處理佇列中的攻擊請求（Phase 2）。
// Handler 解析封包後呼叫 QueueAttack()；本系統依序派送至 processMeleeAttack / processRangedAttack。
// 事件發射（EntityKilled）在 handleNpcDeath 內處理。
type CombatSystem struct {
	deps     *handler.Deps
	requests []handler.AttackRequest
}

func NewCombatSystem(deps *handler.Deps) *CombatSystem {
	return &CombatSystem{deps: deps}
}

func (s *CombatSystem) Phase() coresys.Phase { return coresys.PhaseUpdate }

// QueueAttack implements handler.CombatQueue.
func (s *CombatSystem) QueueAttack(req handler.AttackRequest) {
	s.requests = append(s.requests, req)
}

// HandleNpcDeath implements handler.CombatQueue — 供 handler 內其他檔案呼叫。
func (s *CombatSystem) HandleNpcDeath(npc *world.NpcInfo, killer *world.PlayerInfo, nearby []*world.PlayerInfo) *handler.NpcKillResult {
	return handleNpcDeath(npc, killer, nearby, s.deps)
}

// AddExp implements handler.CombatQueue — 供 handler 內其他檔案呼叫。
func (s *CombatSystem) AddExp(player *world.PlayerInfo, expGain int32) {
	addExp(player, expGain, s.deps)
}

func (s *CombatSystem) Update(_ time.Duration) {
	for _, req := range s.requests {
		if req.IsMelee {
			s.processMeleeAttack(req.AttackerSessionID, req.TargetID)
		} else {
			s.processRangedAttack(req.AttackerSessionID, req.TargetID)
		}
	}
	s.requests = s.requests[:0]
}

// ==================== 近戰攻擊 ====================

// processMeleeAttack 對目標施加近戰攻擊。
func (s *CombatSystem) processMeleeAttack(sessID uint64, targetID int32) *handler.NpcKillResult {
	ws := s.deps.World
	player := ws.GetBySession(sessID)
	if player == nil || player.Dead {
		return nil
	}

	// 麻痺/暈眩/凍結/睡眠時無法攻擊
	if player.Paralyzed || player.Sleeped {
		return nil
	}

	// 絕對屏障：攻擊時自動解除（Java: C_Attack.java 第 164-169 行）
	if player.AbsoluteBarrier && s.deps.Skill != nil {
		s.deps.Skill.CancelAbsoluteBarrier(player)
	}

	// 隱身：攻擊時自動解除（Java: L1BuffUtil.cancelInvisibility）
	if player.Invisible && s.deps.Skill != nil {
		s.deps.Skill.CancelInvisibility(player)
	}

	// 查找目標 — 可能是 NPC 或玩家
	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		// 不是 NPC — 檢查是否為玩家（PvP）
		targetPlayer := ws.GetByCharID(targetID)
		if targetPlayer != nil && !targetPlayer.Dead && targetPlayer.CharID != player.CharID {
			s.deps.PvP.HandlePvPAttack(player, targetPlayer)
		}
		return nil
	}

	// 非戰鬥 NPC（商人等）：只播放攻擊動畫，不造成傷害
	// Java: L1MerchantInstance.onAction() 只呼叫 attack.action()
	if !isAttackableNpc(npc.Impl) {
		player.Heading = CalcHeading(player.X, player.Y, npc.X, npc.Y)
		nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		for _, viewer := range nearby {
			handler.SendAttackPacket(viewer.Session, player.CharID, npc.ID, 0, player.Heading)
		}
		return nil
	}

	// 距離檢查（切比雪夫 <= 2，近戰容差）
	dx := player.X - npc.X
	dy := player.Y - npc.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	dist := dx
	if dy > dist {
		dist = dy
	}
	if dist > 2 {
		return nil
	}

	// 面向目標
	player.Heading = CalcHeading(player.X, player.Y, npc.X, npc.Y)

	// 從裝備武器取得傷害
	weaponDmg := 4 // 空手傷害
	targetSize := npc.Size
	if targetSize == "" {
		targetSize = "small"
	}
	if wpn := player.Equip.Weapon(); wpn != nil {
		if info := s.deps.Items.Get(wpn.ItemID); info != nil {
			if targetSize == "large" && info.DmgLarge > 0 {
				weaponDmg = info.DmgLarge
			} else if info.DmgSmall > 0 {
				weaponDmg = info.DmgSmall
			}
		}
	}

	// 呼叫 Lua 戰鬥公式 — 裝備屬性已套用至 player 欄位
	ctx := scripting.CombatContext{
		AttackerLevel:  int(player.Level),
		AttackerSTR:    int(player.Str),
		AttackerDEX:    int(player.Dex),
		AttackerWeapon: weaponDmg,
		AttackerHitMod: int(player.HitMod),
		AttackerDmgMod: int(player.DmgMod),
		TargetAC:        int(npc.AC),
		TargetLevel:     int(npc.Level),
		TargetMR:        int(npc.MR),
		TargetClassType: -1, // NPC 沒有職業
	}
	result := s.deps.Scripting.CalcMeleeAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	// 取附近玩家用於廣播
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// 武器技能觸發（命中時機率觸發額外傷害 + GFX）
	if damage > 0 {
		if wpn := player.Equip.Weapon(); wpn != nil {
			procDmg := processWeaponSkillProc(player, npc, wpn.ItemID, nearby, s.deps)
			damage += procDmg
		}
	}

	// 廣播攻擊動畫
	for _, viewer := range nearby {
		handler.SendAttackPacket(viewer.Session, player.CharID, npc.ID, damage, player.Heading)
	}

	if damage > 0 {
		// 扣血
		npc.HP -= damage
		if npc.HP < 0 {
			npc.HP = 0
		}

		// 受傷時解除睡眠（Java: NPC 被攻擊時 sleep 解除）
		if npc.Sleeped {
			BreakNpcSleep(npc, ws)
		}

		// 武器耐久損耗（Java: L1Attack.damageNpcWeaponDurability）
		handler.DamageWeaponDurability(player.Session, player, s.deps)

		// 受傷累加仇恨（Java: L1HateList.add）
		AddHate(npc, sessID, damage)

		// 廣播 HP 條更新
		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		for _, viewer := range nearby {
			handler.SendHpMeter(viewer.Session, npc.ID, hpRatio)
		}

		// 檢查死亡
		if npc.HP <= 0 {
			return handleNpcDeath(npc, player, nearby, s.deps)
		}
	}
	return nil
}

// ==================== 遠程攻擊 ====================

// processRangedAttack 對目標施加遠程攻擊。
func (s *CombatSystem) processRangedAttack(sessID uint64, targetID int32) *handler.NpcKillResult {
	ws := s.deps.World
	player := ws.GetBySession(sessID)
	if player == nil || player.Dead {
		return nil
	}

	// 麻痺/暈眩/凍結/睡眠時無法攻擊
	if player.Paralyzed || player.Sleeped {
		return nil
	}

	// 絕對屏障：攻擊時自動解除
	if player.AbsoluteBarrier && s.deps.Skill != nil {
		s.deps.Skill.CancelAbsoluteBarrier(player)
	}

	// 隱身：攻擊時自動解除
	if player.Invisible && s.deps.Skill != nil {
		s.deps.Skill.CancelInvisibility(player)
	}

	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		// 不是 NPC — 檢查是否為玩家（PvP 遠程）
		targetPlayer := ws.GetByCharID(targetID)
		if targetPlayer != nil && !targetPlayer.Dead && targetPlayer.CharID != player.CharID {
			s.deps.PvP.HandlePvPFarAttack(player, targetPlayer)
		}
		return nil
	}

	// 非戰鬥 NPC（商人等）：只播放攻擊動畫，不造成傷害
	if !isAttackableNpc(npc.Impl) {
		player.Heading = CalcHeading(player.X, player.Y, npc.X, npc.Y)
		handler.SendArrowAttackPacket(player.Session, player.CharID, npc.ID, 0, player.Heading,
			player.X, player.Y, npc.X, npc.Y)
		nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		for _, viewer := range nearby {
			if viewer.SessionID == sessID {
				continue
			}
			handler.SendArrowAttackPacket(viewer.Session, player.CharID, npc.ID, 0, player.Heading,
				player.X, player.Y, npc.X, npc.Y)
		}
		return nil
	}

	// 距離檢查（切比雪夫 <= 10，遠程）
	dx := player.X - npc.X
	dy := player.Y - npc.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	dist := dx
	if dy > dist {
		dist = dy
	}
	if dist > 10 {
		return nil
	}

	player.Heading = CalcHeading(player.X, player.Y, npc.X, npc.Y)

	// 從背包找到並消耗箭矢
	arrow := FindArrow(player, s.deps)
	if arrow == nil {
		handler.SendGlobalChat(player.Session, 9, "\\f3沒有箭矢。")
		return nil
	}

	// 消耗 1 支箭矢
	arrowRemoved := player.Inv.RemoveItem(arrow.ObjectID, 1)
	if arrowRemoved {
		handler.SendRemoveInventoryItem(player.Session, arrow.ObjectID)
	} else {
		handler.SendItemCountUpdate(player.Session, arrow)
	}

	// 箭矢傷害加成
	arrowDmg := 0
	if arrowInfo := s.deps.Items.Get(arrow.ItemID); arrowInfo != nil {
		arrowDmg = arrowInfo.DmgSmall
	}

	// 從裝備弓取得傷害
	bowDmg := 1
	targetSize := npc.Size
	if targetSize == "" {
		targetSize = "small"
	}
	if wpn := player.Equip.Weapon(); wpn != nil {
		if info := s.deps.Items.Get(wpn.ItemID); info != nil {
			if targetSize == "large" && info.DmgLarge > 0 {
				bowDmg = info.DmgLarge
			} else if info.DmgSmall > 0 {
				bowDmg = info.DmgSmall
			}
		}
	}

	// 裝備屬性已套用至 player 欄位
	ctx := scripting.RangedCombatContext{
		AttackerLevel:     int(player.Level),
		AttackerSTR:       int(player.Str),
		AttackerDEX:       int(player.Dex),
		AttackerBowDmg:    bowDmg,
		AttackerArrowDmg:  arrowDmg,
		AttackerBowHitMod: int(player.BowHitMod),
		AttackerBowDmgMod: int(player.BowDmgMod),
		TargetAC:          int(npc.AC),
		TargetLevel:       int(npc.Level),
		TargetMR:          int(npc.MR),
		TargetClassType:   -1, // NPC 沒有職業
	}
	result := s.deps.Scripting.CalcRangedAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// 武器技能觸發（命中時機率觸發額外傷害 + GFX）
	if damage > 0 {
		if wpn := player.Equip.Weapon(); wpn != nil {
			procDmg := processWeaponSkillProc(player, npc, wpn.ItemID, nearby, s.deps)
			damage += procDmg
		}
	}

	// 廣播遠程攻擊動畫（含箭矢投射物）
	handler.SendArrowAttackPacket(player.Session, player.CharID, npc.ID, damage, player.Heading,
		player.X, player.Y, npc.X, npc.Y)
	for _, viewer := range nearby {
		if viewer.SessionID == sessID {
			continue // 已發給自己
		}
		handler.SendArrowAttackPacket(viewer.Session, player.CharID, npc.ID, damage, player.Heading,
			player.X, player.Y, npc.X, npc.Y)
	}

	if damage > 0 {
		npc.HP -= damage
		if npc.HP < 0 {
			npc.HP = 0
		}

		// 受傷時解除睡眠
		if npc.Sleeped {
			BreakNpcSleep(npc, ws)
		}

		// 武器耐久損耗（遠程也會磨損武器）
		handler.DamageWeaponDurability(player.Session, player, s.deps)

		// 受傷累加仇恨
		AddHate(npc, sessID, damage)

		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		for _, viewer := range nearby {
			handler.SendHpMeter(viewer.Session, npc.ID, hpRatio)
		}

		if npc.HP <= 0 {
			return handleNpcDeath(npc, player, nearby, s.deps)
		}
	}
	return nil
}

// ==================== NPC 死亡處理 ====================

// handleNpcDeath 處理 NPC 死亡：動畫、經驗、重生計時。
// 回傳 NpcKillResult 供 CombatSystem 發出事件。
func handleNpcDeath(npc *world.NpcInfo, killer *world.PlayerInfo, nearby []*world.PlayerInfo, deps *handler.Deps) *handler.NpcKillResult {
	npc.Dead = true

	// 從 NPC AOI 網格移除（死亡 NPC 不阻擋）
	deps.World.NpcDied(npc)

	// 清除格子碰撞
	if deps.MapData != nil {
		deps.MapData.SetImpassable(npc.MapID, npc.X, npc.Y, false)
	}

	// 廣播死亡動畫 + 屍體狀態
	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, npc.ID, 8)    // 播放死亡動畫
		handler.SendNpcDeadPack(viewer.Session, npc)         // 設定屍體姿態（HP%=0xFF）
	}

	// 延遲移除（Java: NPC_DELETION_TIME = 10 秒 = 50 ticks）
	npc.DeleteTimer = 50

	// 守衛：無經驗、無善惡、無掉落（Java: L1GuardInstance 無獎勵邏輯）
	expGain := int32(0)
	if npc.Impl != "L1Guard" {
		// 計算基礎經驗（套用伺服器經驗倍率）
		baseExp := npc.Exp
		if deps.Config.Rates.ExpRate > 0 {
			baseExp = int32(float64(baseExp) * deps.Config.Rates.ExpRate)
		}

		// 按仇恨比例分配經驗（Java: CalcExp.calcExp）
		totalHate := GetTotalHate(npc)
		if totalHate > 0 && len(npc.HateList) > 1 && baseExp > 0 {
			// 多人打怪：按傷害比例分配
			for sid, hate := range npc.HateList {
				p := deps.World.GetBySession(sid)
				if p == nil || p.Dead {
					continue
				}
				share := baseExp * hate / totalHate
				if share > 0 {
					addExp(p, share, deps)
				}
			}
			expGain = baseExp
		} else {
			// 單人或無仇恨列表：全部給 killer（向下相容）
			expGain = baseExp
			if expGain > 0 {
				addExp(killer, expGain, deps)
			}
		}

		// 給予 killer 的寵物經驗（同地圖）
		for _, pet := range deps.World.GetPetsByOwner(killer.CharID) {
			if !pet.Dead && pet.MapID == killer.MapID {
				petExp := npc.Exp
				if deps.Config.Rates.PetExpRate > 0 {
					petExp = int32(float64(petExp) * deps.Config.Rates.PetExpRate)
				}
				if petExp > 0 && deps.PetLife != nil {
					deps.PetLife.AddPetExp(pet, petExp)
					handler.SendPetHpMeter(killer.Session, pet.ID, pet.HP, pet.MaxHP)
				}
			}
		}

		// 善惡值只給 killer（最高仇恨者）
		deps.PvP.AddLawfulFromNpc(killer, npc.Lawful)

		// 掉落物只給 killer
		handler.GiveDrops(killer, npc.NpcID, deps)
	}

	// 清空仇恨列表（防止殘留影響重生）
	ClearHateList(npc)

	// 設定重生計時器（ticks: delay_seconds * 5，200ms tick）
	if npc.RespawnDelay > 0 {
		npc.RespawnTimer = npc.RespawnDelay * 5
	}

	deps.Log.Info(fmt.Sprintf("NPC 被擊殺  擊殺者=%s  NPC=%s  經驗=%d", killer.Name, npc.Name, expGain))

	killResult := &handler.NpcKillResult{
		KillerSessionID: killer.SessionID,
		KillerCharID:    killer.CharID,
		NpcID:           npc.ID,
		NpcTemplateID:   npc.NpcID,
		ExpGained:       expGain,
		MapID:           npc.MapID,
		X:               npc.X,
		Y:               npc.Y,
	}

	// 發出 EntityKilled 事件（下一 tick 可讀取）
	if deps.Bus != nil {
		event.Emit(deps.Bus, event.EntityKilled{
			KillerSessionID: killResult.KillerSessionID,
			KillerCharID:    killResult.KillerCharID,
			NpcID:           killResult.NpcID,
			NpcTemplateID:   killResult.NpcTemplateID,
			ExpGained:       killResult.ExpGained,
			MapID:           killResult.MapID,
			X:               killResult.X,
			Y:               killResult.Y,
		})
	}

	return killResult
}

// ==================== 經驗值與升級 ====================

const (
	bonusStatMinLevel int16 = 51  // 開始獲得獎勵屬性的最低等級
	maxTotalStats     int16 = 210 // 6 項基本屬性總和上限
)

// addExp 增加經驗值並檢查升級。
// 升級 HP/MP 公式在 Lua（scripts/core/levelup.lua）。
// 經驗值表在 Lua（scripts/core/tables.lua）。
func addExp(player *world.PlayerInfo, expGain int32, deps *handler.Deps) {
	player.Exp += expGain

	newLevel := deps.Scripting.LevelFromExp(int(player.Exp))
	leveledUp := false
	for int16(newLevel) > player.Level && player.Level < 99 {
		player.Level++
		leveledUp = true

		// 透過 Lua 擲骰每級 HP/MP 成長
		result := deps.Scripting.CalcLevelUp(int(player.ClassType), int(player.Con), int(player.Wis))
		player.MaxHP += int16(result.HP)
		player.MaxMP += int16(result.MP)
		player.HP = player.MaxHP // 升級時滿血
		player.MP = player.MaxMP
	}

	// 發送經驗值更新
	handler.SendExpUpdate(player.Session, player.Level, player.Exp)

	if leveledUp {
		player.Dirty = true
		// 發送完整狀態更新（客戶端偵測等級變化後自動播放升級特效）
		handler.SendPlayerStatus(player.Session, player)

		// 51 級以上顯示加點對話框
		if player.Level >= bonusStatMinLevel {
			available := player.Level - 50 - player.BonusStats
			totalStats := player.Str + player.Dex + player.Con + player.Wis + player.Intel + player.Cha
			if available > 0 && totalStats < maxTotalStats {
				handler.SendRaiseAttrDialog(player.Session, player.CharID)
			}
		}

		deps.Log.Info(fmt.Sprintf("玩家升級  角色=%s  等級=%d  經驗=%d  最大HP=%d  最大MP=%d", player.Name, player.Level, player.Exp, player.MaxHP, player.MaxMP))
	}
}

// ==================== 戰鬥工具函式 ====================

// 方向偏移查找表（8 方向）
var combatHeadingDX = [8]int32{0, 1, 1, 1, 0, -1, -1, -1}
var combatHeadingDY = [8]int32{-1, -1, 0, 1, 1, 1, 0, -1}

// CalcHeading 計算從 (sx,sy) 到 (tx,ty) 的朝向方向。
func CalcHeading(sx, sy, tx, ty int32) int16 {
	ddx := tx - sx
	ddy := ty - sy
	if ddx > 0 {
		ddx = 1
	} else if ddx < 0 {
		ddx = -1
	}
	if ddy > 0 {
		ddy = 1
	} else if ddy < 0 {
		ddy = -1
	}
	for i := int16(0); i < 8; i++ {
		if combatHeadingDX[i] == ddx && combatHeadingDY[i] == ddy {
			return i
		}
	}
	return 0
}

// FindArrow 在玩家背包中找到第一支可用的箭矢。
func FindArrow(player *world.PlayerInfo, deps *handler.Deps) *world.InvItem {
	for _, item := range player.Inv.Items {
		info := deps.Items.Get(item.ItemID)
		if info != nil && info.ItemType == "arrow" && item.Count > 0 {
			return item
		}
	}
	return nil
}

// isAttackableNpc 判斷 NPC 是否可被攻擊（會受到傷害）。
// Java: L1MonsterInstance/L1GuardInstance 有完整 onAction（命中/傷害/commit），
// L1MerchantInstance 等非戰鬥 NPC 只播放動畫。
func isAttackableNpc(impl string) bool {
	switch impl {
	case "L1Monster", "L1Guard", "L1Guardian", "L1Scarecrow":
		return true
	}
	return false
}

// BreakNpcSleep 受傷時解除 NPC 睡眠（Java: NPC 受到傷害時 sleep 被打斷）。
func BreakNpcSleep(npc *world.NpcInfo, ws *world.State) {
	npc.Sleeped = false
	npc.RemoveDebuff(62)  // 沉睡之霧
	npc.RemoveDebuff(66)  // 沉睡之霧（內部 ID）
	npc.RemoveDebuff(103) // 暗黑盲咒
}
