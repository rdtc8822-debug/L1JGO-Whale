package system

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

const (
	npcAbsoluteBarrierSkillID = int32(78)
	npcIceLanceSkillID        = int32(50)
	npcEarthBindSkillID       = int32(157)
	npcFreezingBlizzardID     = int32(80)
	dragonEyeFafurionSkillID  = int32(6685)
	dragonEyeLifeSkillID      = int32(6687)
	dragonEyeBirthSkillID     = int32(6688)
	dragonEyeFigureSkillID    = int32(6689)
	dragonEyeReductionGfxID   = int32(6320)
	skillTypeChange           = 2
	skillTypeRestore          = 32
	skillFinalBurn            = int32(108)
)

func npcDamageBlockedBySkm0LikeJava(npc *world.NpcInfo) bool {
	return npc != nil &&
		(npc.HasDebuff(npcAbsoluteBarrierSkillID) ||
			npc.HasDebuff(npcIceLanceSkillID) ||
			npc.HasDebuff(npcEarthBindSkillID) ||
			npc.HasDebuff(npcFreezingBlizzardID))
}

func playerDamageBlockedBySkm0LikeJava(player *world.PlayerInfo) bool {
	return player != nil &&
		(player.AbsoluteBarrier ||
			player.HasBuff(npcIceLanceSkillID) ||
			player.HasBuff(npcEarthBindSkillID) ||
			player.HasBuff(npcFreezingBlizzardID))
}

func applyDragonEyeMagicDamageReductionLikeYiwei(target *world.PlayerInfo, damage int32, roll int, nearby []*world.PlayerInfo) int32 {
	if target == nil || damage <= 0 || roll >= 10 {
		return damage
	}
	if !target.HasBuff(dragonEyeFafurionSkillID) &&
		!target.HasBuff(dragonEyeLifeSkillID) &&
		!target.HasBuff(dragonEyeBirthSkillID) &&
		!target.HasBuff(dragonEyeFigureSkillID) {
		return damage
	}
	damage /= 2
	if len(nearby) > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, dragonEyeReductionGfxID))
	}
	return damage
}

func (s *SkillSystem) applyNpcKirtasMirrorMagicDamageLikeJava(attacker *world.PlayerInfo, npc *world.NpcInfo, damage int32, nearby []*world.PlayerInfo) int32 {
	if attacker == nil || npc == nil || damage <= 0 || !npc.HasDebuff(mobSkillKirtasBarrier2) {
		return damage
	}
	if attacker.HP > 0 {
		attacker.HP -= damage
		attacker.Dirty = true
		if attacker.HP < 0 {
			attacker.HP = 0
		}
		sendHpUpdate(attacker.Session, attacker)
	}
	if len(nearby) > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(attacker.CharID, 2))
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, 4395))
	}
	if attacker.HP <= 0 && s.deps != nil && s.deps.Death != nil {
		s.deps.Death.KillPlayer(attacker)
	}
	return 0
}

func (s *SkillSystem) canSkillReachTarget(caster *world.PlayerInfo, skill *data.SkillInfo, mapID int16, x, y int32) bool {
	if caster == nil || skill == nil {
		return false
	}
	if caster.MapID != mapID {
		return false
	}
	if skill.Through || skill.Type&skillTypeChange != 0 || skill.Type&skillTypeRestore != 0 {
		return true
	}
	if s == nil || s.deps == nil || s.deps.MapData == nil {
		return true
	}
	return s.deps.MapData.HasLineOfSight(caster.MapID, caster.X, caster.Y, x, y)
}

func (s *SkillSystem) executeAttackSkillOnPlayer(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, target *world.PlayerInfo) {
	if target == nil || target.Dead || target.MapID != player.MapID || target.ShowID != player.ShowID {
		return
	}

	maxRange := int32(skill.Ranged)
	// 132 TRIPLE_ARROW：Java skillmode 透過 `cha.onAction(srcpc)` 走 L1AttackPc 弓箭射程
	// （典型 8~10 格），yaml `ranged=-1` 在 Go fallback 為 2（近戰）會讓 PvP 三重矢退化為
	// 貼身攻擊。對齊 NPC 路徑 (skill_damage.go:390-391) 的 10 格特例。
	if skill.SkillID == 132 {
		maxRange = 10
	} else if maxRange <= 0 {
		maxRange = 2
	}
	if chebyshevDist(player.X, player.Y, target.X, target.Y) > maxRange+2 {
		return
	}

	if !s.canSkillReachTarget(player, skill, target.MapID, target.X, target.Y) {
		s.sendCastFail(sess)
		return
	}

	player.Heading = CalcHeading(player.X, player.Y, target.X, target.Y)
	nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)

	if skill.SkillID == skillFoeSlayer {
		s.executeFoeSlayerOnPlayer(sess, player, skill, target, nearby)
		return
	}

	// 三重矢（132）PvP：對齊 Java `TRIPLE_ARROW.start()` for-loop 3 次 `cha.onAction(srcpc)`，
	// 走完整 L1AttackPc PvP 弓箭流程：每發獨立命中骰、武器/箭矢/DEX_DMG/buff 加成、扣箭矢。
	// 不套用 ConfigSkill.TRIPLE_ARROW_DMG=5 倍率（Go 設計刻意捨棄該倍率以維持遊戲平衡）。
	if skill.SkillID == 132 && s.deps.PvP != nil {
		for i := 0; i < 3; i++ {
			s.deps.PvP.HandlePvPFarAttack(player, target)
			if target.Dead || target.HP <= 0 {
				break
			}
		}
		casterNearby := s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, 0, player.ShowID)
		handler.BroadcastToPlayers(casterNearby, handler.BuildSkillEffect(player.CharID, 4394))
		handler.BroadcastToPlayers(casterNearby, handler.BuildSkillEffect(player.CharID, 11764))
		return
	}

	targets := []*world.PlayerInfo{target}
	if skill.Area > 0 {
		for _, other := range nearby {
			if other.CharID == player.CharID || other.CharID == target.CharID || other.Dead {
				continue
			}
			if chebyshevDist(target.X, target.Y, other.X, other.Y) <= int32(skill.Area) &&
				s.canSkillReachTarget(player, skill, other.MapID, other.X, other.Y) {
				targets = append(targets, other)
			}
		}
	}

	for _, p := range targets {
		if p.CharID != player.CharID && s.tryCounterMagic(p, skill.SkillID) {
			continue
		}
		res := s.deps.Scripting.CalcSkillDamage(s.buildPlayerSkillDamageContext(player, p, skill))
		dmg := int32(res.Damage)
		if skill.SkillID == skillJoyOfPain {
			s.applyJoyOfPainReady(player)
			dmg = 0
		} else if skill.SkillID == skillFinalBurn {
			dmg = player.MP
		} else if skill.SkillID == skillMindBreak {
			dmg = calcMindBreakDamage(player)
			applyMindBreakMPDrain(p)
		}
		hitCount := res.HitCount
		if hitCount < 1 {
			hitCount = 1
		}
		totalLeechDamage := int32(0)
		for h := 0; h < hitCount; h++ {
			if p.Dead || p.HP <= 0 {
				break
			}
			totalLeechDamage += s.applySkillDamageToPlayer(sess, player, p, skill, dmg, nearby)
		}
		if (skill.SkillID == 10 || skill.SkillID == 28) && totalLeechDamage > 0 {
			player.HP += totalLeechDamage
			if player.HP > player.MaxHP {
				player.HP = player.MaxHP
			}
			sendHpUpdate(sess, player)
		}
		s.applyIllusionistStatusAttackEffect(p, skill)
		s.applyIllusionistControlAttackEffect(player, p, skill)
		s.applyDragonKnightBindAttackEffect(player, p, skill)
		s.applyDragonKnightFreezeAttackEffect(player, p, skill)
	}

	if skill.Area > 0 {
		for _, npc := range s.deps.World.GetNearbyNpcsInShow(target.X, target.Y, target.MapID, player.ShowID) {
			if npc.Dead || chebyshevDist(target.X, target.Y, npc.X, npc.Y) > int32(skill.Area) {
				continue
			}
			if !s.canSkillReachTarget(player, skill, npc.MapID, npc.X, npc.Y) {
				continue
			}
			s.applyAreaSkillDamageToNpc(sess, player, skill, npc, nearby)
		}
	}
}

func (s *SkillSystem) buildPlayerSkillDamageContext(player, target *world.PlayerInfo, skill *data.SkillInfo) scripting.SkillDamageContext {
	return scripting.SkillDamageContext{
		SkillID:                  int(skill.SkillID),
		DamageValue:              skill.DamageValue,
		DamageDice:               skill.DamageDice,
		DamageDiceCount:          skill.DamageDiceCount,
		SkillLevel:               skill.SkillLevel,
		Attr:                     skill.Attr,
		AttackerLevel:            int(player.Level),
		AttackerSTR:              int(player.Str),
		AttackerBaseSTR:          calcPlayerBaseStrLikeJava(player),
		AttackerDEX:              int(player.Dex),
		AttackerBaseDEX:          calcPlayerBaseDexLikeJava(player),
		AttackerINT:              int(player.Intel),
		AttackerBaseINT:          calcPlayerBaseIntLikeJava(player),
		AttackerWIS:              int(player.Wis),
		AttackerSP:               int(player.SP),
		AttackerTrueSP:           calcPlayerTrueSPLikeJava(player),
		AttackerFullSP:           calcPlayerSPLikeJava(player),
		AttackerDmgMod:           int(player.DmgMod),
		AttackerHitMod:           int(player.HitMod),
		AttackerHP:               int(player.HP),
		AttackerMaxHP:            int(player.MaxHP),
		AttackerMagicLevel:       calcMagicLevel(int(player.ClassType), int(player.Level)),
		AttackerMagicCrit:        int(player.MagicCritical),
		AttackerOriginalMagicHit: int(player.OriginalMagicHit),
		TargetAC:                 int(target.AC),
		TargetLevel:              int(target.Level),
		TargetMR:                 int(target.MR),
		TargetFireRes:            int(target.FireRes),
		TargetWaterRes:           int(target.WaterRes),
		TargetWindRes:            int(target.WindRes),
		TargetEarthRes:           int(target.EarthRes),
		TargetMP:                 int(target.MP),
	}
}

func (s *SkillSystem) applySkillDamageToPlayer(sess *net.Session, player, target *world.PlayerInfo, skill *data.SkillInfo, dmg int32, nearby []*world.PlayerInfo) int32 {
	if dmg < 0 {
		dmg = 0
	}
	if target.AbsoluteBarrier {
		dmg = 0
	}
	dmg = applyImmuneToHarmDamage(target, dmg)
	dmg = applyReductionArmorDamage(target, dmg, false)
	dmg = applyDragonEyeMagicDamageReductionLikeYiwei(target, dmg, world.RandInt(100), nearby)
	dmg = s.applyCounterMirrorMagicDamage(player, target, dmg, world.RandInt(100), nearby)

	gfxID := int32(skill.CastGfx)
	if gfxID <= 0 {
		gfxID = int32(skill.ActionID)
	}
	useType := byte(6)
	if skill.Area > 0 {
		useType = 8
	}
	for _, viewer := range nearby {
		handler.SendUseAttackSkill(viewer.Session, player.CharID, target.CharID,
			int16(dmg), player.Heading, gfxID, useType,
			player.X, player.Y, target.X, target.Y)
	}

	if player.AttackView {
		handler.SendDamageNumbers(sess, target.CharID, dmg)
	}
	if dmg <= 0 {
		return 0
	}

	if target.Sleeped {
		breakPlayerSleepBySkill(target)
	}

	s.applyJoyOfPainBacklash(player, target, nearby)

	target.HP -= dmg
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
		if s.deps.Death != nil {
			s.deps.Death.KillPlayer(target)
		}
		return dmg
	}
	sendHpUpdate(target.Session, target)
	return dmg
}

func (s *SkillSystem) applyAreaSkillDamageToNpc(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, npc *world.NpcInfo, nearby []*world.PlayerInfo) {
	if npcBlocksSkillTargetLikeJava(npc, skill.SkillID) {
		return
	}
	ctx := scripting.SkillDamageContext{
		SkillID:                  int(skill.SkillID),
		DamageValue:              skill.DamageValue,
		DamageDice:               skill.DamageDice,
		DamageDiceCount:          skill.DamageDiceCount,
		SkillLevel:               skill.SkillLevel,
		Attr:                     skill.Attr,
		AttackerLevel:            int(player.Level),
		AttackerSTR:              int(player.Str),
		AttackerBaseSTR:          calcPlayerBaseStrLikeJava(player),
		AttackerDEX:              int(player.Dex),
		AttackerBaseDEX:          calcPlayerBaseDexLikeJava(player),
		AttackerINT:              int(player.Intel),
		AttackerBaseINT:          calcPlayerBaseIntLikeJava(player),
		AttackerWIS:              int(player.Wis),
		AttackerSP:               int(player.SP),
		AttackerTrueSP:           calcPlayerTrueSPLikeJava(player),
		AttackerFullSP:           calcPlayerSPLikeJava(player),
		AttackerDmgMod:           int(player.DmgMod),
		AttackerHitMod:           int(player.HitMod),
		AttackerHP:               int(player.HP),
		AttackerMaxHP:            int(player.MaxHP),
		AttackerMagicLevel:       calcMagicLevel(int(player.ClassType), int(player.Level)),
		AttackerMagicCrit:        int(player.MagicCritical),
		AttackerOriginalMagicHit: int(player.OriginalMagicHit),
		TargetAC:                 int(npc.AC),
		TargetLevel:              int(npc.Level),
		TargetMR:                 int(npc.MR),
		TargetFireRes:            int(npc.FireRes),
		TargetWaterRes:           int(npc.WaterRes),
		TargetWindRes:            int(npc.WindRes),
		TargetEarthRes:           int(npc.EarthRes),
		TargetMP:                 int(npc.MP),
	}
	res := s.deps.Scripting.CalcSkillDamage(ctx)
	dmg := int32(res.Damage)
	if dmg < 0 {
		dmg = 0
	}
	if npcRejectsDamageWhileHiddenLikeJava(npc) {
		dmg = 0
	}
	if dmg > 0 && npcDamageBlockedBySkm0LikeJava(npc) {
		dmg = 0
	}
	if dmg > 0 && !(skill.DamageValue == 0 && skill.DamageDice == 0) {
		dmg = s.applyNpcKirtasMirrorMagicDamageLikeJava(player, npc, dmg, nearby)
	}
	// 副本武器需求檢查（火龍窟「必須裝備真死亡騎士烈炎之劍」）：玩家技能對副本怪傷害同樣受限。
	if dmg > 0 && npc.WeaponRequired != 0 {
		var equippedID int32
		if wpn := player.Equip.Weapon(); wpn != nil {
			equippedID = wpn.ItemID
		}
		if !npc.CanReceiveDamageFrom(equippedID) {
			dmg = 0
			handler.SendSystemMessage(sess, "你的武器無法傷害這個敵人。")
		}
	}
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
	}
	if player.AttackView {
		handler.SendDamageNumbers(sess, npc.ID, dmg)
	}
	npc.HP -= dmg
	if npc.HP < 0 {
		npc.HP = 0
	}
	AddPlayerHateLikeJava(s.deps.World, npc, player, dmg)
	TryNpcHideOnDamageLikeJava(npc, s.deps.World)
	hpRatio := int16(0)
	if npc.MaxHP > 0 {
		hpRatio = int16((npc.HP * 100) / npc.MaxHP)
	}
	handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
	if npc.HP <= 0 {
		handleNpcDeath(npc, player, nearby, s.deps)
	}
}

func breakPlayerSleepBySkill(target *world.PlayerInfo) {
	target.Sleeped = false
	target.RemoveBuff(62)
	target.RemoveBuff(66)
	target.RemoveBuff(103)
	handler.SendParalysis(target.Session, handler.SleepRemove)
}

const (
	burningSpiritChance     = 15
	doubleBreakBaseChance   = 20
	doubleBreakEdoryuBonus  = 5
	burningSpiritMultiplier = 3
	burningSpiritDivisor    = 2
	doubleBreakMultiplier   = 2
)

func darkElfPhysicalDamage(attacker *world.PlayerInfo, damage int32, weaponType string) int32 {
	return darkElfPhysicalDamageWithRolls(attacker, damage, weaponType, world.RandInt(100), world.RandInt(100))
}

// burningSlashDamage 套用燃燒擊砍（182）一次性 +10 傷害並消耗 buff。
// Java `L1AttackPc.calcBuffDamage` 第 2434-2438 行：
//
//	if (_pc.hasSkillEffect(BURNING_SLASH)) {
//	    dmg += 10.0D;
//	    _pc.sendPacketsX10(new S_EffectLocation(_targetX, _targetY, 6591));
//	    _pc.killSkillEffectTimer(BURNING_SLASH);
//	}
//
// `calcBuffDamage` 前置條件（同函數第 2408-2416 行）：weaponType 不為 bow(20)、gauntlet(62)、ki-koru(17)
// — 對齊以 `isRangedWeaponType` 早返回排除 bow/gauntlet（Go 暫無 ki-koru 類型）。
// 視覺特效廣播由呼叫方負責（需要 targetX/Y 與 nearby）。
//
// 回傳 (新傷害, 是否消耗 buff)；caller 若 consumed=true 應廣播 S_EffectLocation。
func burningSlashDamage(deps *handler.Deps, attacker *world.PlayerInfo, damage int32, weaponType string) (int32, bool) {
	if attacker == nil || damage <= 0 {
		return damage, false
	}
	if isRangedWeaponType(weaponType) {
		return damage, false
	}
	if !attacker.HasBuff(182) {
		return damage, false
	}
	damage += 10
	if deps != nil {
		handler.RemoveBuffAndRevert(attacker, 182, deps)
	}
	return damage, true
}

func darkElfPhysicalDamageWithRolls(attacker *world.PlayerInfo, damage int32, weaponType string, burningRoll, doubleBreakRoll int) int32 {
	if attacker == nil || damage <= 0 {
		return damage
	}
	if attacker.HasBuff(105) && doubleBreakRoll < doubleBreakChance(attacker, weaponType) {
		damage *= doubleBreakMultiplier
	}
	if attacker.HasBuff(102) && burningRoll < burningSpiritChance {
		damage = damage * burningSpiritMultiplier / burningSpiritDivisor
	}
	return damage
}

func doubleBreakChance(attacker *world.PlayerInfo, weaponType string) int {
	switch weaponType {
	case "claw":
		return doubleBreakBaseChance + doubleBreakLevelBonus(attacker)
	case "edoryu":
		return doubleBreakBaseChance + doubleBreakLevelBonus(attacker) + doubleBreakEdoryuBonus
	default:
		return 0
	}
}

func doubleBreakLevelBonus(attacker *world.PlayerInfo) int {
	if attacker == nil || attacker.Level <= 45 {
		return 0
	}
	return int(attacker.Level-45) / 5
}

func (s *SkillSystem) equippedWeaponType(player *world.PlayerInfo) string {
	if player == nil || s == nil || s.deps == nil || s.deps.Items == nil {
		return ""
	}
	wpn := player.Equip.Weapon()
	if wpn == nil {
		return ""
	}
	info := s.deps.Items.Get(wpn.ItemID)
	if info == nil {
		return ""
	}
	return info.Type
}

// ========================================================================
//  攻擊技能
// ========================================================================

// executeAttackSkill 處理傷害型技能（目標為 NPC）。
func (s *SkillSystem) executeAttackSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32) {
	ws := s.deps.World

	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		if target := ws.GetByCharID(targetID); target != nil {
			// Owner: skill_damage.go
			s.executeAttackSkillOnPlayer(sess, player, skill, target)
		}
		return
	}
	if npc.MapID != player.MapID || npc.ShowID != player.ShowID {
		return
	}
	if npcBlocksSkillTargetLikeJava(npc, skill.SkillID) {
		return
	}

	// 距離檢查
	maxRange := int32(skill.Ranged)
	if skill.SkillID == 132 {
		maxRange = 10
	} else if maxRange <= 0 {
		maxRange = 2
	}
	dist := chebyshevDist(player.X, player.Y, npc.X, npc.Y)
	if dist > maxRange+2 {
		return
	}

	// LOS 檢查（Java: L1SkillUse — glanceCheck）
	// 攻擊型技能需要視線，buff/heal 技能豁免
	if !s.canSkillReachTarget(player, skill, npc.MapID, npc.X, npc.Y) {
		s.sendCastFail(sess)
		return
	}
	if npc.HiddenStatus == world.NpcHiddenSink && skillCanRevealSinkHiddenLikeJava(skill.SkillID) {
		npcAppearOnGroundLikeJava(npc, ws, player)
	}

	player.Heading = CalcHeading(player.X, player.Y, npc.X, npc.Y)

	// 起死回生術 (18)：對不死族 NPC 機率即死
	if skill.SkillID == 18 {
		s.executeTurnUndead(sess, player, skill, npc)
		return
	}

	// 三重矢（132）：對齊 Java `TRIPLE_ARROW.start()` for-loop 3 次 `cha.onAction(srcpc)`，
	// 走完整 L1AttackPc 弓箭流程：每發獨立命中骰、武器/箭矢/DEX_DMG/buff 加成、扣箭矢。
	// 不套用 ConfigSkill.TRIPLE_ARROW_DMG=5 倍率（Go 設計刻意捨棄該倍率以維持遊戲平衡）。
	// 廣播 4394（加速封包）+ 11764（特效動畫）對應 Java skillmode 第 45-46 行收尾。
	if skill.SkillID == 132 && s.deps.Combat != nil {
		for i := 0; i < 3; i++ {
			s.deps.Combat.ExecuteRangedAttackOnNpc(player, npc.ID)
			if npc.Dead {
				break
			}
		}
		casterNearby := s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, 0, player.ShowID)
		handler.BroadcastToPlayers(casterNearby, handler.BuildSkillEffect(player.CharID, 4394))
		handler.BroadcastToPlayers(casterNearby, handler.BuildSkillEffect(player.CharID, 11764))
		return
	}

	// 武器傷害
	weaponDmg := 4 // 拳頭
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

	// Lua 傷害計算 context 建構
	buildCtx := func(n *world.NpcInfo) scripting.SkillDamageContext {
		return scripting.SkillDamageContext{
			SkillID:                  int(skill.SkillID),
			DamageValue:              skill.DamageValue,
			DamageDice:               skill.DamageDice,
			DamageDiceCount:          skill.DamageDiceCount,
			SkillLevel:               skill.SkillLevel,
			Attr:                     skill.Attr,
			AttackerLevel:            int(player.Level),
			AttackerSTR:              int(player.Str),
			AttackerBaseSTR:          calcPlayerBaseStrLikeJava(player),
			AttackerDEX:              int(player.Dex),
			AttackerBaseDEX:          calcPlayerBaseDexLikeJava(player),
			AttackerINT:              int(player.Intel),
			AttackerBaseINT:          calcPlayerBaseIntLikeJava(player),
			AttackerWIS:              int(player.Wis),
			AttackerSP:               int(player.SP),
			AttackerTrueSP:           calcPlayerTrueSPLikeJava(player),
			AttackerFullSP:           calcPlayerSPLikeJava(player),
			AttackerDmgMod:           int(player.DmgMod),
			AttackerHitMod:           int(player.HitMod),
			AttackerWeapon:           weaponDmg,
			AttackerHP:               int(player.HP),
			AttackerMaxHP:            int(player.MaxHP),
			AttackerMagicLevel:       calcMagicLevel(int(player.ClassType), int(player.Level)),
			AttackerMagicCrit:        int(player.MagicCritical),
			AttackerOriginalMagicHit: int(player.OriginalMagicHit),
			TargetAC:                 int(n.AC),
			TargetLevel:              int(n.Level),
			TargetMR:                 int(n.MR),
			TargetFireRes:            int(n.FireRes),
			TargetWaterRes:           int(n.WaterRes),
			TargetWindRes:            int(n.WindRes),
			TargetEarthRes:           int(n.EarthRes),
			TargetMP:                 int(n.MP),
		}
	}

	type hitTarget struct {
		npc      *world.NpcInfo
		dmg      int32
		hitCount int
		drainMP  int32
	}

	res := s.deps.Scripting.CalcSkillDamage(buildCtx(npc))

	// 心靈破壞（207）：特殊傷害公式 SP × 3.8（Java: MIND_BREAK.java）
	if skill.SkillID == 207 {
		res.Damage = int(float64(calcPlayerSPLikeJava(player)) * 3.8)
		res.DrainMP = 5 // 強制扣除目標 5 MP
	}
	if skill.SkillID == skillFinalBurn {
		res.Damage = int(player.MP)
		res.HitCount = 1
		res.DrainMP = 0
	}
	if skill.SkillID == skillJoyOfPain {
		s.applyJoyOfPainReady(player)
		res.Damage = 0
	}

	hits := []hitTarget{{npc: npc, dmg: int32(res.Damage), hitCount: res.HitCount, drainMP: int32(res.DrainMP)}}

	// 極光雷電（17）：Bresenham 直線目標（Java: getVisibleLineObjects）
	if skill.SkillID == 17 {
		lineNpcs := ws.GetNpcsAlongLine(player.X, player.Y, npc.X, npc.Y, player.MapID)
		for _, other := range lineNpcs {
			if other.ID == npc.ID || other.ShowID != player.ShowID {
				continue
			}
			if npcBlocksSkillTargetLikeJava(other, skill.SkillID) {
				continue
			}
			if !s.canSkillReachTarget(player, skill, other.MapID, other.X, other.Y) {
				continue
			}
			r := s.deps.Scripting.CalcSkillDamage(buildCtx(other))
			hits = append(hits, hitTarget{npc: other, dmg: int32(r.Damage), hitCount: r.HitCount, drainMP: int32(r.DrainMP)})
		}
	} else if skill.Area > 0 {
		allNpcs := ws.GetNearbyNpcsInShow(npc.X, npc.Y, npc.MapID, player.ShowID)
		for _, other := range allNpcs {
			if other.ID == npc.ID || other.Dead {
				continue
			}
			if npcBlocksSkillTargetLikeJava(other, skill.SkillID) {
				continue
			}
			if chebyshevDist(npc.X, npc.Y, other.X, other.Y) > int32(skill.Area) {
				continue
			}
			if !s.canSkillReachTarget(player, skill, other.MapID, other.X, other.Y) {
				continue
			}
			r := s.deps.Scripting.CalcSkillDamage(buildCtx(other))
			hits = append(hits, hitTarget{npc: other, dmg: int32(r.Damage), hitCount: r.HitCount, drainMP: int32(r.DrainMP)})
		}
	}

	nearby := ws.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, 0, player.ShowID)

	if skill.SkillID == skillFoeSlayer {
		s.executeFoeSlayerOnNpc(sess, player, skill, npc, nearby)
		return
	}

	isPhysicalSkill := skill.DamageValue == 0 && skill.DamageDice == 0
	directedAreaSkill := skill.Area > 0
	gfxID := int32(skill.CastGfx)
	if gfxID <= 0 {
		gfxID = int32(skill.ActionID)
	}

	useType := byte(6)
	if directedAreaSkill {
		rangeTargets := make([]handler.RangeSkillTarget, 0, len(hits))
		for _, t := range hits {
			rangeTargets = append(rangeTargets, handler.RangeSkillTarget{
				ObjectID: t.npc.ID,
				Hit:      t.dmg > 0,
				Damage:   t.dmg,
			})
		}
		handler.BroadcastToPlayers(nearby, handler.BuildRangeSkill(
			player.CharID,
			player.X,
			player.Y,
			player.Heading,
			gfxID,
			byte(skill.ActionID),
			handler.RangeSkillTypeDir,
			rangeTargets,
		))
	}

	for i, t := range hits {
		hitsToApply := t.hitCount
		if hitsToApply < 1 {
			hitsToApply = 1
		}

		// 副本武器需求檢查（火龍窟「必須裝備真死亡騎士烈炎之劍」）：
		// AoE / 多 hit 技能對每個副本怪目標檢查一次，不符則該目標所有 hit 傷害歸 0。
		blockDmg := false
		if t.npc.WeaponRequired != 0 {
			var equippedID int32
			if wpn := player.Equip.Weapon(); wpn != nil {
				equippedID = wpn.ItemID
			}
			if !t.npc.CanReceiveDamageFrom(equippedID) {
				blockDmg = true
			}
		}

		for h := 0; h < hitsToApply; h++ {
			dmg := t.dmg
			if blockDmg || npcRejectsDamageWhileHiddenLikeJava(t.npc) {
				dmg = 0
			}
			if dmg > 0 && npcDamageBlockedBySkm0LikeJava(t.npc) {
				dmg = 0
			}
			if dmg > 0 && !isPhysicalSkill {
				dmg = s.applyNpcKirtasMirrorMagicDamageLikeJava(player, t.npc, dmg, nearby)
			}

			if directedAreaSkill {
				// S_RangeSkill 已一次帶出方向範圍攻擊的施法與命中特效。
			} else if isPhysicalSkill {
				atkData := handler.BuildAttackPacket(player.CharID, t.npc.ID, dmg, player.Heading)
				handler.BroadcastToPlayers(nearby, atkData)
				if skill.CastGfx > 0 {
					effData := handler.BuildSkillEffect(t.npc.ID, skill.CastGfx)
					handler.BroadcastToPlayers(nearby, effData)
				}
			} else {
				if i == 0 {
					// 主目標：完整攻擊技能封包（含施法動畫）
					for _, viewer := range nearby {
						handler.SendUseAttackSkill(viewer.Session, player.CharID, t.npc.ID,
							int16(dmg), player.Heading, gfxID, useType,
							int32(player.X), int32(player.Y), int32(t.npc.X), int32(t.npc.Y))
					}
				} else {
					// AoE 次要目標：只顯示命中特效，不重播施法動畫
					if gfxID > 0 {
						handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(t.npc.ID, gfxID))
					}
				}
			}

			// 浮動傷害數字（技能攻擊，魔防抵抗時顯示 MISS）
			if player.AttackView {
				handler.SendDamageNumbers(sess, t.npc.ID, dmg)
			}

			t.npc.HP -= dmg
			if t.npc.HP < 0 {
				t.npc.HP = 0
			}

			// 受傷時解除睡眠
			if t.npc.Sleeped {
				BreakNpcSleep(t.npc, ws)
			}

			// Mind Break: Java 會扣 5 MP，目標不足時扣到 0。
			if h == 0 && t.drainMP > 0 {
				t.npc.MP -= t.drainMP
				if t.npc.MP < 0 {
					t.npc.MP = 0
				}
			}

			// 技能傷害累加仇恨
			AddPlayerHateLikeJava(ws, t.npc, player, dmg)
			TryNpcHideOnDamageLikeJava(t.npc, ws)

			hpRatio := int16(0)
			if t.npc.MaxHP > 0 {
				hpRatio = int16((t.npc.HP * 100) / t.npc.MaxHP)
			}
			hpData := handler.BuildHpMeter(t.npc.ID, hpRatio)
			handler.BroadcastToPlayers(nearby, hpData)

			if t.npc.HP <= 0 {
				handleNpcDeath(t.npc, player, nearby, s.deps)
				break
			}
		}
	}

	// 吸血系技能：傷害轉為治療（Java: CHILL_TOUCH / VAMPIRIC_TOUCH — heal = this._dmg）
	if skill.SkillID == 28 || skill.SkillID == 10 {
		totalDmg := int32(0)
		for _, t := range hits {
			totalDmg += int32(t.dmg)
		}
		if totalDmg > 0 {
			player.HP += totalDmg
			if player.HP > player.MaxHP {
				player.HP = player.MaxHP
			}
			sendHpUpdate(sess, player)
		}
	}

	// 凍結類攻擊技能：傷害後 MR 判定凍結（Java: setFrozen + S_Poison 灰色）
	// 50=冰矛圍籬, 30=岩牢, 80=冰雪颶風, 194=寒冰噴吐（Java: skill 22 寒冰氣息無凍結效果）
	if skill.SkillID == 50 || skill.SkillID == 30 || skill.SkillID == 80 || skill.SkillID == 194 {
		for _, t := range hits {
			if t.npc.Dead || t.npc.Paralyzed || t.npc.HasDebuff(22) || t.npc.HasDebuff(30) || t.npc.HasDebuff(50) || t.npc.HasDebuff(80) || t.npc.HasDebuff(194) {
				continue
			}
			if s.checkNpcMRResist(player, t.npc, skill.SkillID) {
				dur := skill.BuffDuration
				if dur <= 0 {
					switch skill.SkillID {
					case 50:
						dur = 16
					case 30:
						dur = 10
					case 80:
						dur = 16
					case 194:
						dur = 3
					}
				}
				t.npc.Paralyzed = true
				t.npc.AddDebuff(skill.SkillID, (dur+1)*5)
				handler.BroadcastToPlayers(nearby, handler.BuildPoison(t.npc.ID, 2))
				// Java `L1SkillUse:2234 spawnEffect(81168, time, npc.X, npc.Y, mapId, _user, 0)` —
				// 194 寒冰噴吐對 NPC 命中時也需生成 81168 冰矛圍籬視覺地面效果。
				if skill.SkillID == 194 {
					s.spawnFreezingBreathGroundEffect(player, t.npc.X, t.npc.Y, t.npc.MapID, int(dur+1))
				}
			}
		}
	}

	// 奪命之雷（192）：傷害後束縛 NPC（Java: THUNDER_GRAB.java:54-83）
	// Java 對 NPC 目標：a) STATUS_FREEZE(4000) 免疫；b) bindtime stack with cap 4；
	// c) 廣播 S_SkillSound(4184) + spawnEffect(81182, bindtime, npc.X, npc.Y)；
	// d) **不**呼叫 setParalyzed（明確 commented out），改用 setPassispeed(0) 速度鎖。
	// Go 目前缺 passispeed 欄位，暫以 npc.Paralyzed 替代（broader gap：NPC passispeed 系統未實作）。
	if skill.SkillID == 192 {
		for _, t := range hits {
			if t.npc.Dead || t.npc.Paralyzed {
				continue
			}
			if t.npc.HasDebuff(4000) {
				continue
			}
			if s.checkNpcMRResist(player, t.npc, 192) {
				bindSec := world.RandInt(4) + 1
				if rem, ok := t.npc.ActiveDebuffs[192]; ok && rem > 0 {
					bindSec += rem / 5
				}
				if bindSec > 4 {
					bindSec = 4
				}
				t.npc.Paralyzed = true // broader gap: 應為 passispeed=0 而非 Paralyzed
				t.npc.AddDebuff(192, bindSec*5)
				nearby := s.deps.World.GetNearbyPlayersInShow(t.npc.X, t.npc.Y, t.npc.MapID, 0, t.npc.ShowID)
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(t.npc.ID, 4184))
				s.spawnThunderGrabGroundEffect(player, t.npc.X, t.npc.Y, t.npc.MapID, bindSec)
			}
		}
	}

	// 骷髏毀壞（208）：傷害後暈眩 NPC（Java `BONE_BREAK.start():31-36`）。
	// PC→NPC 機率走 generic `checkNpcMRResist`（broader gap：Java 用 5/10/15 + INT/MR
	// config 預設皆 0、無 magichit），暫不個別化。命中後對 NPC `setParalyzed(true)` +
	// `broadcastPacketAll(S_SkillSound(npcId, 13119))`「骷髏毀壞動畫」。
	if skill.SkillID == 208 {
		for _, t := range hits {
			if t.npc.Dead || t.npc.Paralyzed {
				continue
			}
			if s.checkNpcMRResist(player, t.npc, 208) {
				dur := world.RandInt(2) + 1 // 1-2 秒
				t.npc.Paralyzed = true
				t.npc.AddDebuff(208, dur*5)
				nearby := s.deps.World.GetNearbyPlayersInShow(t.npc.X, t.npc.Y, t.npc.MapID, 0, t.npc.ShowID)
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(t.npc.ID, 13119))
			}
		}
	}

	// 混亂（202）：傷害後沉默 NPC。
	// Java `skillmode/CONFUSION.java:22-30`：對 cha 設 `L1SkillId.SILENCE (=64), integer*1000ms`，
	// 並非設 CONFUSION(202) 本身。Go 原本 `AddDebuff(202)` 是符號性的——其他系統檢查 silence
	// 都看 skillSilence(64)，202 不會啟動沉默語義。本步改為 64 對齊 Java；保留「已沉默不刷新」守衛。
	if skill.SkillID == 202 {
		for _, t := range hits {
			if t.npc.Dead || t.npc.HasDebuff(64) {
				continue
			}
			dur := skill.BuffDuration
			if dur <= 0 {
				dur = 8
			}
			t.npc.AddDebuff(64, dur*5)
		}
	}
}

// executeTurnUndead 起死回生術（skill 18）— 對不死族 NPC 機率即死。
// Java 參考: L1SkillUse.java TYPE_CURSE 分支，undeadType == 1 || 3 時 _dmg = currentHp。
// GFX：不走攻擊動畫，走 ActionGfx + SkillEffect（Java 明確排除 Turn Undead 的 S_UseAttackSkill）。
func (s *SkillSystem) executeTurnUndead(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, npc *world.NpcInfo) {
	ws := s.deps.World
	nearby := ws.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)

	// 施法動畫（Java: S_DoActionGFX）
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))

	// 前置判定：目標必須是不死族（Java: undeadType == 1 || 3 且 isTU == true）
	if !canTurnUndeadNpc(npc) {
		// 非不死族：施法動畫播放但不造成傷害
		return
	}

	// 目標特效（Java: S_SkillSound(targetId, castGfx=754)）
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
	}

	// 機率判定（Java: calcProbabilityMagic → default 分支）
	// diceCount = max(magicBonus + magicLevel, 1)
	// probability = sum of diceCount rolls of d7
	// 成功條件: probability >= random(1~100)
	magicLevel := int(player.Level) / 4 // 簡化：施法者等級 / 4
	magicBonus := int(player.Intel) - 12
	if magicBonus < 0 {
		magicBonus = 0
	}
	diceCount := magicLevel + magicBonus
	if diceCount < 1 {
		diceCount = 1
	}
	probability := 0
	for i := 0; i < diceCount; i++ {
		probability += world.RandInt(7) + 1 // 1~7
	}
	rnd := world.RandInt(100) + 1 // 1~100
	if probability < rnd {
		// 失敗
		s.sendCastFail(sess)
		return
	}

	// 成功：傷害 = 目標當前 HP（即死）
	dmg := npc.HP
	npc.HP = 0

	// 受傷時解除睡眠
	if npc.Sleeped {
		BreakNpcSleep(npc, ws)
	}

	// 即死傷害累加仇恨
	AddPlayerHateLikeJava(ws, npc, player, dmg)

	hpData := handler.BuildHpMeter(npc.ID, 0)
	handler.BroadcastToPlayers(nearby, hpData)

	_ = dmg // 即死傷害值，用於 handleNpcDeath 的經驗計算
	handleNpcDeath(npc, player, nearby, s.deps)
}

func canTurnUndeadNpc(npc *world.NpcInfo) bool {
	if npc == nil {
		return false
	}
	if npc.UndeadType != 0 && npc.UndeadType != 1 && npc.UndeadType != 3 {
		return false
	}
	if npc.TurnUndeadableSet {
		return npc.TurnUndeadable
	}
	return npc.Undead
}
