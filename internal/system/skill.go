package system

import (
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// 技能相關訊息 ID
const (
	skillMsgNotEnoughMP uint16 = 278 // "因魔力不足而無法使用魔法。"
	skillMsgNotEnoughHP uint16 = 279 // "因體力不足而無法使用魔法。"
	skillMsgCastFail    uint16 = 280 // "施展魔法失敗。"
)

// calcMagicLevel 計算職業魔法等級（Go 側鏡像，與 Lua class_feature.lua 一致）。
// 用於建構 SkillDamageContext，避免每次技能傷害計算都跨 Lua 呼叫。
func calcMagicLevel(classType, level int) int {
	if level <= 0 {
		return 0
	}
	switch classType {
	case 0: // 王族
		return min(2, level/10)
	case 1: // 騎士
		return level / 50
	case 2: // 精靈
		return min(6, level/8)
	case 3: // 法師
		return min(13, level/4)
	case 4: // 黑暗精靈
		return min(2, level/12)
	case 5: // 龍騎士
		return min(4, level/9)
	case 6: // 幻術師
		return min(10, level/6)
	default:
		return 0
	}
}

// calcMagicBonusLikeJava 對齊 Java L1Character.getMagicBonus()。
func calcMagicBonusLikeJava(intel int) int {
	switch {
	case intel <= 5:
		return -2
	case intel <= 8:
		return -1
	case intel <= 11:
		return 0
	case intel <= 14:
		return 1
	case intel <= 17:
		return 2
	case intel <= 24:
		return intel - 15
	case intel <= 35:
		return 10
	case intel <= 42:
		return 11
	case intel <= 49:
		return 12
	default:
		return 13
	}
}

// calcPlayerTrueSPLikeJava 對齊 Java getTrueSp(): getMagicLevel() + getMagicBonus()。
func calcPlayerTrueSPLikeJava(player *world.PlayerInfo) int {
	if player == nil {
		return 0
	}
	return calcMagicLevel(int(player.ClassType), int(player.Level)) + calcMagicBonusLikeJava(int(player.Intel))
}

// calcPlayerSPLikeJava 對齊 Java getSp() 的主要路徑：true SP + 裝備/buff 額外 SP。
func calcPlayerSPLikeJava(player *world.PlayerInfo) int {
	if player == nil {
		return 0
	}
	return calcPlayerTrueSPLikeJava(player) + int(player.SP)
}

func calcPlayerBaseStrLikeJava(player *world.PlayerInfo) int {
	if player == nil {
		return 0
	}
	base := int(player.Str) - player.EquipBonuses.AddStr
	for _, buff := range player.ActiveBuffs {
		if buff == nil {
			continue
		}
		base -= int(buff.DeltaStr)
	}
	if base < 0 {
		return 0
	}
	return base
}

func calcPlayerBaseDexLikeJava(player *world.PlayerInfo) int {
	if player == nil {
		return 0
	}
	base := int(player.Dex) - player.EquipBonuses.AddDex
	for _, buff := range player.ActiveBuffs {
		if buff == nil {
			continue
		}
		base -= int(buff.DeltaDex)
	}
	if base < 0 {
		return 0
	}
	return base
}

func calcPlayerBaseIntLikeJava(player *world.PlayerInfo) int {
	if player == nil {
		return 0
	}
	base := int(player.Intel) - player.EquipBonuses.AddInt
	for _, buff := range player.ActiveBuffs {
		if buff == nil {
			continue
		}
		base -= int(buff.DeltaIntel)
	}
	if base < 0 {
		return 0
	}
	return base
}

func calcPlayerErLikeYiwei(player *world.PlayerInfo) int16 {
	if player == nil {
		return 0
	}
	if player.HasBuff(174) {
		return 0
	}
	er := 0
	switch player.ClassType {
	case 1, 4, 7: // Knight, Dark Elf, Warrior
		er += int(player.Level) / 4
	case 0, 2: // Crown, Elf
		er += int(player.Level) / 6
	case 3: // Wizard
		er += int(player.Level) / 10
	case 5: // Dragon Knight
		er += int(player.Level) / 5
	case 6: // Illusionist
		er += int(player.Level) / 9
	}
	dex := int(player.Dex)
	if dex <= 7 {
		er += 3
	} else {
		er += (dex-8)/2 + 4
	}
	er += int(player.Dodge)
	return int16(er)
}

// SkillSystem processes queued skill requests in Phase 2.
// 管理技能執行、buff 套用/到期、NPC debuff。
type SkillSystem struct {
	deps                    *handler.Deps
	requests                []handler.SkillRequest
	suppressCastFailMessage bool
	braveAvatarElapsed      time.Duration
}

// NewSkillSystem 建立 SkillSystem。
func NewSkillSystem(deps *handler.Deps) *SkillSystem {
	return &SkillSystem{deps: deps}
}

func (s *SkillSystem) sendCastFail(sess *net.Session) {
	if s != nil && s.suppressCastFailMessage {
		return
	}
	handler.SendServerMessage(sess, skillMsgCastFail)
}

// Phase 回傳系統執行階段。
func (s *SkillSystem) Phase() coresys.Phase { return coresys.PhaseUpdate }

// QueueSkill implements handler.SkillManager.
func (s *SkillSystem) QueueSkill(req handler.SkillRequest) {
	s.requests = append(s.requests, req)
}

// Update 處理所有排隊的技能請求。
func (s *SkillSystem) Update(dt time.Duration) {
	s.braveAvatarElapsed += dt
	if s.braveAvatarElapsed >= braveAvatarInterval {
		s.braveAvatarElapsed = 0
		s.updateBraveAvatarAura()
	}
	for _, req := range s.requests {
		s.processSkill(req)
	}
	s.requests = s.requests[:0]
}

// CancelAllBuffs implements handler.SkillManager.
func (s *SkillSystem) CancelAllBuffs(target *world.PlayerInfo) {
	s.cancelAllBuffs(target)
}

// ClearAllBuffsOnDeath implements handler.SkillManager — 死亡時清除所有 buff（含不可取消的）。
func (s *SkillSystem) ClearAllBuffsOnDeath(target *world.PlayerInfo) {
	if target.ActiveBuffs == nil {
		return
	}
	for skillID, buff := range target.ActiveBuffs {
		s.revertBuffStats(target, buff)
		delete(target.ActiveBuffs, skillID)
		s.cancelBuffIcon(target, skillID)

		if skillID == handler.SkillShapeChange && s.deps.Polymorph != nil {
			s.deps.Polymorph.UndoPoly(target)
		}
		if buff.SetMoveSpeed > 0 {
			target.MoveSpeed = 0
			target.HasteTicks = 0
			s.sendSpeedToAll(target, 0, 0)
		}
		if buff.SetBraveSpeed > 0 {
			target.BraveSpeed = 0
			s.sendBraveToAll(target, 0, 0)
		}
	}
	handler.SendPlayerStatus(target.Session, target)
}

// GMClearAllStatuses implements handler.SkillManager — GM 強制清除所有狀態：
// 全部 buff（含不可取消的覺醒類）+ 中毒 + 詛咒 + 控制狀態旗標 + 完整客戶端通知。
// 比 ClearAllBuffsOnDeath 完整：後者僅供死亡使用，依賴死亡封包覆蓋畫面狀態，
// 因此沒送凍結/麻痺/隱身解除封包；GM 清 buff 沒有死亡封包做收尾，必須自行通知客戶端。
func (s *SkillSystem) GMClearAllStatuses(target *world.PlayerInfo) {
	needFreezeRemove := false
	needStunRemove := false
	needParalysisRemove := false
	needSleepRemove := false
	needBindRemove := false
	needInvisRemove := false

	if target.ActiveBuffs != nil {
		// 先快照 key 避免 range-while-mutating 的隱晦行為
		ids := make([]int32, 0, len(target.ActiveBuffs))
		for id := range target.ActiveBuffs {
			ids = append(ids, id)
		}
		for _, skillID := range ids {
			buff, ok := target.ActiveBuffs[skillID]
			if !ok {
				continue
			}
			s.revertBuffStats(target, buff)
			delete(target.ActiveBuffs, skillID)
			s.cancelBuffIcon(target, skillID)

			if skillID == handler.SkillShapeChange && s.deps.Polymorph != nil {
				s.deps.Polymorph.UndoPoly(target)
			}
			if buff.SetMoveSpeed > 0 {
				target.MoveSpeed = 0
				target.HasteTicks = 0
				s.sendSpeedToAll(target, 0, 0)
			}
			if buff.SetBraveSpeed > 0 {
				target.BraveSpeed = 0
				s.sendBraveToAll(target, 0, 0)
			}

			if buff.SetParalyzed {
				switch skillID {
				case 87, 508:
					needStunRemove = true
				case 157, 50, 80, 30, 194:
					needFreezeRemove = true
				case 192:
					needBindRemove = true
				default:
					needParalysisRemove = true
				}
			}
			if buff.SetSleeped {
				needSleepRemove = true
			}
			if buff.SetInvisible {
				needInvisRemove = true
			}
		}
	}

	// 雜項計時器歸零
	target.HasteTicks = 0
	target.BraveTicks = 0
	if target.WisdomTicks > 0 {
		target.WisdomTicks = 0
		target.SP -= target.WisdomSP
		target.WisdomSP = 0
	}

	// 客戶端控制狀態解除通知
	if needFreezeRemove {
		handler.SendParalysis(target.Session, handler.FreezeRemove)
		broadcastPlayerPoison(target, 0, s.deps)
	}
	if needStunRemove {
		handler.SendParalysis(target.Session, handler.StunRemove)
	}
	if needParalysisRemove {
		handler.SendParalysis(target.Session, handler.ParalysisRemove)
	}
	if needSleepRemove {
		handler.SendParalysis(target.Session, handler.SleepRemove)
	}
	if needBindRemove {
		handler.SendParalysis(target.Session, handler.BindRemove)
	}
	if needInvisRemove {
		handler.SendInvisible(target.Session, target.CharID, false)
		s.sendPlayerReappearToVisible(target)
	}

	// 中毒/詛咒（獨立於 ActiveBuffs 的系統）
	CurePoison(target, s.deps)
	CureCurseParalysis(target, s.deps)

	// 殘留旗標兜底（若上述邏輯漏掉某種來源）
	target.AbsoluteBarrier = false
	target.Silenced = false
	if !shouldStayParalyzed(target, false, false) {
		target.Paralyzed = false
	}

	handler.SendPlayerStatus(target.Session, target)
}

// RemoveBuffAndRevert implements handler.SkillManager.
func (s *SkillSystem) RemoveBuffAndRevert(target *world.PlayerInfo, skillID int32) {
	s.removeBuffAndRevert(target, skillID)
}

func (s *SkillSystem) sendYiweiPostCastStatusRefresh(targets ...*world.PlayerInfo) {
	for _, target := range targets {
		if target == nil || target.Session == nil {
			continue
		}
		handler.SendMagicStatus(target.Session, byte(calcPlayerSPLikeJava(target)), uint16(target.MR))
		handler.SendPlayerStatus(target.Session, target)
		handler.SendUpdateER(target.Session, calcPlayerErLikeYiwei(target))
	}
}

// TickPlayerBuffs implements handler.SkillManager.
func (s *SkillSystem) TickPlayerBuffs(p *world.PlayerInfo) {
	s.tickPlayerBuffs(p)
}

// ========================================================================
//  技能處理主流程
// ========================================================================

// processSkill 驗證並執行技能請求。由 Update() 在 Phase 2 呼叫。
func (s *SkillSystem) processSkill(req handler.SkillRequest) {
	skillID := req.SkillID
	targetID := s.resolveSkillRequestTargetID(req)
	player := s.deps.World.GetBySession(req.SessionID)
	if player == nil || player.Dead {
		return
	}
	sess := player.Session

	skill := s.deps.Skills.Get(skillID)
	if skill == nil {
		s.failTeleportSkill(sess, skillID)
		s.deps.Log.Debug("unknown skill", zap.Int32("skill_id", skillID))
		return
	}

	s.deps.Log.Debug("C_UseSpell",
		zap.String("player", player.Name),
		zap.Int32("skill_id", skillID),
		zap.String("skill", skill.Name),
		zap.String("target_type", skill.Target),
		zap.Int32("target", targetID),
		zap.Int32("target_x", req.TargetX),
		zap.Int32("target_y", req.TargetY),
		zap.Int32("bookmark_id", req.BookmarkID),
		zap.Int32("summon_id", req.SummonID),
		zap.String("target_name", req.TargetName),
	)

	// --- 驗證 ---

	// Java C_UseSkill 先檢查變形是否可施法，再判斷隱身技能限制。
	if player.PolyID != 0 && s.deps.Polys != nil {
		poly := s.deps.Polys.GetByID(player.PolyID)
		if poly != nil && !poly.CanUseSkill {
			handler.SendServerMessage(sess, 285) // 此狀態下不能使用魔法
			s.failTeleportSkill(sess, skillID)
			return
		}
	}

	// Java C_UseSkill 會先以不可施法狀態回覆 285，再判斷隱身技能限制。
	if player.Paralyzed || player.Sleeped || (player.Silenced && !isCastableWhileSilenced(skillID)) {
		handler.SendServerMessage(sess, 285)
		s.failTeleportSkill(sess, skillID)
		return
	}

	if player.Invisible && skillID == 87 {
		handler.SendServerMessage(sess, 1003)
		return
	}

	// 隱身：施法時自動解除（Java: L1BuffUtil.cancelInvisibility 在 C_UseSkill）
	if player.Invisible {
		s.cancelInvisibility(player)
	}

	// 檢查是否已學會此法術
	if !s.playerKnowsSpell(player, skillID) {
		return
	}

	// 全域施法冷卻
	now := time.Now()
	if now.Before(player.SkillDelayUntil) {
		s.failTeleportSkill(sess, skillID)
		return
	}

	// 絕對屏障：合法施法時自動解除（Java: C_UseSkill 在合法性檢查後解除）
	if player.AbsoluteBarrier {
		s.cancelAbsoluteBarrier(player)
	}
	s.removeBuffAndRevert(player, 32)

	// HP 消耗檢查
	if skillID == 108 && player.HP <= 100 {
		handler.SendServerMessage(sess, skillMsgNotEnoughHP)
		s.failTeleportSkill(sess, skillID)
		return
	}
	if skill.HpConsume > 0 && player.HP <= int32(skill.HpConsume) {
		handler.SendServerMessage(sess, skillMsgNotEnoughHP)
		s.failTeleportSkill(sess, skillID)
		return
	}

	// MP 消耗檢查
	mpConsume := adjustedSkillMPConsume(player, skill)
	if mpConsume > 0 && player.MP < int32(mpConsume) {
		handler.SendServerMessage(sess, skillMsgNotEnoughMP)
		s.failTeleportSkill(sess, skillID)
		return
	}

	if skillID == 147 && player.ElfAttr == 0 {
		s.sendCastFail(sess)
		s.failTeleportSkill(sess, skillID)
		return
	}

	// --- 材料消耗檢查（Java: isItemConsume）---
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		needItemID := int32(skill.ItemConsumeID)
		slot := player.Inv.FindByItemID(needItemID)
		if slot == nil || slot.Count < int32(skill.ItemConsumeCount) {
			haveCount := int32(0)
			if slot != nil {
				haveCount = slot.Count
			}
			var invIDs []int32
			for i, it := range player.Inv.Items {
				if i >= 10 {
					break
				}
				invIDs = append(invIDs, it.ItemID)
			}
			s.deps.Log.Warn("skill blocked: insufficient materials",
				zap.Int32("skill_id", skillID),
				zap.String("skill_name", skill.Name),
				zap.Int32("need_item_id", needItemID),
				zap.Int("need_count", skill.ItemConsumeCount),
				zap.Bool("slot_found", slot != nil),
				zap.Int32("have_count", haveCount),
				zap.Int("inv_size", player.Inv.Size()),
				zap.Int32s("inv_first10", invIDs))
			handler.SendServerMessage(sess, 299) // 施放魔法所需材料不足。
			s.failTeleportSkill(sess, skillID)
			return
		}
	}

	// --- 傳送技能：在消耗 MP 前特殊路由 ---
	if skillID == 87 && s.shockStunInvalidTargetBeforeConsume(player, skill, targetID) {
		return
	}

	if skillID == 5 || skillID == 69 {
		// Owner: skill_teleport.go
		s.executeTeleportSpell(sess, player, skill, req.BookmarkID)
		return
	}

	// 131 TELEPORT_TO_MATHER：3.80C 路徑只保留 4000(束縛) / 192(奪命之雷) 與
	// `pc.getMap().isEscapable()` 檢查；225-230 戰士技能屬後續版本，不套入此目標版本。
	// 在 MP 消耗前返回（與 skill 5/69 模式一致）。
	if skillID == 131 && s.teleportToMatherBlockedBeforeConsume(sess, player) {
		return
	}

	// 132 TRIPLE_ARROW：Java `TRIPLE_ARROW.start()` 第 32-33 行 `getCurrentWeapon() != 20 → return 0`
	// 嚴格要求裝備弓（visual byte = 20）；不裝備時整段 skipped。在 MP 消耗前返回（Go pre-consume 模式）。
	if skillID == 132 && player.CurrentWeapon != 20 {
		return
	}

	// --- 召喚技能：委派 SummonSystem（資源消耗在內部驗證後處理）---
	if s.deps.Summon != nil {
		switch skillID {
		case 51:
			s.deps.Summon.ExecuteSummonMonster(sess, player, skill, req.SummonID)
			return
		case 154, 162:
			s.deps.Summon.ExecuteElementalSummon(sess, player, skill)
			return
		case 36:
			s.deps.Summon.ExecuteTamingMonster(sess, player, skill, targetID)
			return
		case 41:
			s.deps.Summon.ExecuteCreateZombie(sess, player, skill, targetID)
			return
		case 145:
			s.deps.Summon.ExecuteReturnToNature(sess, player, skill, targetID)
			return
		}
	}

	// --- 血盟目標技能：Java 以角色名稱找線上玩家，可跨地圖，不走一般目標距離檢查 ---
	switch skillID {
	case 116, 118:
		// Owner: skill_clan.go
		s.executeClanTargetSkill(sess, player, skill, targetID, req.TargetName, true)
		return
	}

	// --- 技能專屬前置驗證（Java C_UseSkill 行為，需在資源消耗前執行）---
	// Java `C_UseSkill.java:154-169` FIRE_BLESS 須裝備劍類武器（type 1/2/3/5/6/7 = sword/dagger/tohandsword/spear/blunt/staff），
	// 不符合 → 送 msg 3435「使用魔法: 失敗(未成功), 需要裝備劍武器」+ return（不消耗 MP）。
	if skillID == 155 && !fireBlessWeaponAllowed(player, s.deps.Items) {
		handler.SendServerMessage(sess, 3435)
		return
	}

	// --- 消耗資源（MP、HP、材料）---
	if isGroundTargetSkill(skillID) {
		if isCubeSkill(skillID) && s.hasNearbySameCube(player, skillID) {
			handler.SendServerMessage(sess, 1412)
			return
		}
		s.consumeSkillResources(sess, player, skill)
		// Owner: skill_ground_effect.go
		s.executeGroundTargetSkill(sess, player, skill, req.TargetX, req.TargetY)
		return
	}

	if skillID != skillFinalBurn {
		if mpConsume > 0 {
			player.MP -= int32(mpConsume)
			sendMpUpdate(sess, player)
		}
		if skill.HpConsume > 0 {
			player.HP -= int32(skill.HpConsume)
			sendHpUpdate(sess, player)
		}
		if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
			slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
			if slot != nil {
				removed := player.Inv.RemoveItem(slot.ObjectID, int32(skill.ItemConsumeCount))
				if removed {
					handler.SendRemoveInventoryItem(sess, slot.ObjectID)
				} else {
					handler.SendItemCountUpdate(sess, slot)
				}
				handler.SendWeightUpdate(sess, player)
			}
		}
	}

	// --- 設定全域冷卻 ---
	delay := skill.ReuseDelay
	if delay <= 0 {
		delay = 1000
	}
	player.SkillDelayUntil = now.Add(time.Duration(delay) * time.Millisecond)

	// --- 復活技能：特殊路由 ---
	if s.isResurrectionSkill(skill) {
		s.executeResurrection(sess, player, skill, targetID)
		return
	}

	// --- 物品強化技能：targetID = 背包物品 ObjectID ---
	// Java: 這些技能將 target_id 解釋為物品 ObjectID，不是角色/NPC ID
	switch skillID {
	case 21: // BLESSED_ARMOR（鎧甲護持）— 鎧甲 AC-3
		s.executeArmorEnchant(sess, player, skill, targetID)
		return
	case 48:
		s.executeBlessWeaponEnchant(sess, player, skill, targetID)
		return
	case 12, 107: // ENCHANT_WEAPON / SHADOW_FANG：武器附魔
		s.executeTargetedWeaponEnchant(sess, player, skill, targetID)
		return
	case 73: // CREATE_MAGICAL_WEAPON（創造魔法武器）— 武器強化 +1
		s.executeCreateMagicalWeapon(sess, player, skill, targetID)
		return
	case 100: // BRING_STONE（提煉魔石）— 魔石升級鏈
		s.executeBringStone(sess, player, skill, targetID)
		return
	}

	// --- 依目標類型執行 ---
	switch skill.Target {
	case "attack":
		// Owner: skill_damage.go
		s.executeAttackSkill(sess, player, skill, targetID)
		if skillID == skillFinalBurn {
			s.consumeFinalBurnResources(sess, player)
		}
	case "buff":
		// Owner: skill_buff.go
		s.executeBuffSkill(sess, player, skill, targetID, req.Text)
	default:
		// Owner: skill_self.go
		s.executeSelfSkill(sess, player, skill)
	}
}

func adjustedSkillMPConsume(player *world.PlayerInfo, skill *data.SkillInfo) int {
	if skill == nil {
		return 0
	}
	mp := skill.MpConsume
	if (skill.SkillID == 12 || skill.SkillID == 13) && playerHasEquippedItem(player, 20015) {
		mp >>= 1
	}
	if skill.SkillID == 87 && player != nil && player.Intel > 12 {
		mp -= int(player.Intel - 12)
		if mp < 0 {
			mp = 0
		}
	}
	return mp
}

func playerHasEquippedItem(player *world.PlayerInfo, itemID int32) bool {
	if player == nil || player.Inv == nil {
		return false
	}
	for _, item := range player.Inv.Items {
		if item != nil && item.ItemID == itemID && item.Equipped {
			return true
		}
	}
	return false
}

func (s *SkillSystem) failTeleportSkill(sess *net.Session, skillID int32) {
	switch skillID {
	case 5, 69, 131:
		handler.SendParalysis(sess, handler.TeleportUnlock)
	}
}

func (s *SkillSystem) resolveSkillRequestTargetID(req handler.SkillRequest) int32 {
	if req.TargetID != 0 || req.TargetName == "" || s.deps == nil || s.deps.World == nil {
		return req.TargetID
	}
	target := s.deps.World.GetByName(req.TargetName)
	if target == nil {
		return 0
	}
	return target.CharID
}

func isCastableWhileSilenced(skillID int32) bool {
	switch skillID {
	case 87, 88, 89, 90, 91, 187:
		return true
	default:
		return false
	}
}

// consumeSkillResources 扣除 MP/HP/材料並設定冷卻。
func (s *SkillSystem) consumeSkillResources(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo) {
	if skill.MpConsume > 0 {
		player.MP -= int32(skill.MpConsume)
		sendMpUpdate(sess, player)
	}
	if skill.HpConsume > 0 {
		player.HP -= int32(skill.HpConsume)
		sendHpUpdate(sess, player)
	}
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot != nil {
			removed := player.Inv.RemoveItem(slot.ObjectID, int32(skill.ItemConsumeCount))
			if removed {
				handler.SendRemoveInventoryItem(sess, slot.ObjectID)
			} else {
				handler.SendItemCountUpdate(sess, slot)
			}
			handler.SendWeightUpdate(sess, player)
		}
	}
	delay := skill.ReuseDelay
	if delay <= 0 {
		delay = 1000
	}
	player.SkillDelayUntil = time.Now().Add(time.Duration(delay) * time.Millisecond)
}

func (s *SkillSystem) consumeFinalBurnResources(sess *net.Session, player *world.PlayerInfo) {
	if player.HP != 100 {
		player.HP = 100
		sendHpUpdate(sess, player)
	}
	if player.MP != 1 {
		player.MP = 1
		sendMpUpdate(sess, player)
	}
}

// ========================================================================
//  工具函式
// ========================================================================

// playerKnowsSpell 檢查玩家是否已學會指定法術。
func (s *SkillSystem) playerKnowsSpell(player *world.PlayerInfo, skillID int32) bool {
	for _, sid := range player.KnownSpells {
		if sid == skillID {
			return true
		}
	}
	return false
}

// chebyshevDist 計算兩點間的切比雪夫距離。
func chebyshevDist(x1, y1, x2, y2 int32) int32 {
	dx := x1 - x2
	dy := y1 - y2
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return dy
	}
	return dx
}
