package system

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

const skillThunderGrab = int32(192)

func (s *SkillSystem) applyDragonKnightBindAttackEffect(caster, target *world.PlayerInfo, skill *data.SkillInfo) {
	if caster == nil || target == nil || skill == nil || target.Dead {
		return
	}
	if skill.SkillID == skillThunderGrab && checkDragonKnightDebuffSuccess(caster, target, skill) {
		s.applyThunderGrabBind(caster, target)
	}
}

func checkDragonKnightDebuffSuccess(caster, target *world.PlayerInfo, skill *data.SkillInfo) bool {
	return world.RandInt(100) < calcDragonKnightDebuffProbability(caster, target, skill)
}

func calcDragonKnightDebuffProbability(caster, target *world.PlayerInfo, skill *data.SkillInfo) int {
	if caster == nil || target == nil || skill == nil {
		return 0
	}
	levelDiff := int(caster.Level - target.Level)
	probability := int(float64(skill.ProbabilityDice)/10.0*float64(levelDiff)) + skill.ProbabilityValue
	if probability < 0 {
		return 0
	}
	if probability > 100 {
		return 100
	}
	return probability
}

func (s *SkillSystem) applyThunderGrabBind(caster, target *world.PlayerInfo) {
	// Java `THUNDER_GRAB.java:35 if (!cha.hasSkillEffect(4000))` 對 STATUS_FREEZE 目標免疫。
	if target.HasBuff(4000) {
		return
	}
	// Java `THUNDER_GRAB.java:23-32`：bindtime = random(4)+1；若已持 192，加上殘餘秒數，最多 4 秒上限。
	bindSeconds := world.RandInt(4) + 1
	if old := target.GetBuff(skillThunderGrab); old != nil {
		// `cha.getSkillEffectTimeSec(192)` 殘餘秒數（Go buff.TicksLeft 為 0.2 秒精度，÷5 換算秒）
		remainingSec := int(old.TicksLeft / 5)
		bindSeconds += remainingSec
	}
	if bindSeconds > 4 {
		bindSeconds = 4
	}
	buff := &world.ActiveBuff{
		SkillID:      skillThunderGrab,
		TicksLeft:    bindSeconds * 5,
		SetParalyzed: true,
	}
	old := target.AddBuff(buff)
	if old != nil {
		s.revertBuffStats(target, old)
	}
	target.Paralyzed = true
	handler.SendParalysis(target.Session, handler.BindApply)

	nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, 4184))
	// Java `THUNDER_GRAB.java:40 spawnEffect(81182, bindtime, pc.X, pc.Y, mapId, srcpc, 0)` —
	// 在目標位置生成 81182 視覺地面效果，存活時間 = bindtime（秒）。
	if caster != nil {
		s.spawnThunderGrabGroundEffect(caster, target.X, target.Y, target.MapID, bindSeconds)
	}
}

// spawnThunderGrabGroundEffect 在指定位置生成 81182 視覺地面效果，存活時間 = bindSec 秒。
// 對齊 Java `L1SpawnUtil.spawnEffect(81182, bindtime, x, y, mapId, srcpc, 0)`。
func (s *SkillSystem) spawnThunderGrabGroundEffect(caster *world.PlayerInfo, x, y int32, mapID int16, bindSec int) {
	if s.deps == nil || s.deps.Npcs == nil || s.deps.World == nil {
		return
	}
	tpl := s.deps.Npcs.Get(thunderGrabEffectNpcID)
	if tpl == nil {
		return
	}
	effect := &world.GroundEffect{
		ID:           world.NextGroundEffectID(),
		SkillID:      skillThunderGrab,
		NpcID:        thunderGrabEffectNpcID,
		GfxID:        tpl.GfxID,
		Type:         world.GroundEffectThunderGrab,
		X:            x,
		Y:            y,
		MapID:        mapID,
		ShowID:       caster.ShowID,
		OwnerCharID:  caster.CharID,
		OwnerSession: caster.SessionID,
		OwnerName:    caster.Name,
		OwnerIntel:   caster.Intel,
		OwnerClanID:  caster.ClanID,
		TicksLeft:    bindSec * groundEffectTickSec,
	}
	if effect.TicksLeft <= 0 {
		effect.TicksLeft = groundEffectTickSec
	}
	s.deps.World.AddGroundEffect(effect)
	s.broadcastGroundEffect(effect)
}

const thunderGrabEffectNpcID = int32(81182)

const skillFreezingBreath = int32(194)
const freezingBreathEffectNpcID = int32(81168)

func (s *SkillSystem) applyDragonKnightFreezeAttackEffect(caster, target *world.PlayerInfo, skill *data.SkillInfo) {
	if caster == nil || target == nil || skill == nil || target.Dead {
		return
	}
	if skill.SkillID != skillFreezingBreath {
		return
	}
	if checkDragonKnightDebuffSuccess(caster, target, skill) {
		s.applyFreezingBreathFreeze(caster, target, skill)
	}
	s.revealInvisibleTarget(target)
}

func (s *SkillSystem) applyFreezingBreathFreeze(caster, target *world.PlayerInfo, skill *data.SkillInfo) {
	if target.Paralyzed || target.HasBuff(50) || target.HasBuff(80) || target.HasBuff(30) || target.HasBuff(157) || target.HasBuff(skillFreezingBreath) {
		return
	}
	dur := skill.BuffDuration + 1
	if dur <= 0 {
		dur = 4
	}
	buff := &world.ActiveBuff{
		SkillID:      skillFreezingBreath,
		TicksLeft:    dur * 5,
		SetParalyzed: true,
	}
	old := target.AddBuff(buff)
	if old != nil {
		s.revertBuffStats(target, old)
	}
	target.Paralyzed = true
	handler.SendParalysis(target.Session, handler.FreezeApply)
	broadcastPlayerPoison(target, 2, s.deps)

	// Java `L1SkillUse:2225 spawnEffect(81168, time, pc.X, pc.Y, mapId, _user, 0)` —
	// 在目標位置生成 81168 視覺地面效果（冰矛圍籬）存活 dur 秒，非單純 S_SkillSound。
	if caster != nil {
		s.spawnFreezingBreathGroundEffect(caster, target.X, target.Y, target.MapID, int(dur))
	}
}

// spawnFreezingBreathGroundEffect 在指定位置生成 81168 視覺地面效果，存活時間 = durSec 秒。
// 對齊 Java `L1SpawnUtil.spawnEffect(81168, time, x, y, mapId, _user, 0)`。
func (s *SkillSystem) spawnFreezingBreathGroundEffect(caster *world.PlayerInfo, x, y int32, mapID int16, durSec int) {
	if s.deps == nil || s.deps.Npcs == nil || s.deps.World == nil {
		return
	}
	tpl := s.deps.Npcs.Get(freezingBreathEffectNpcID)
	if tpl == nil {
		return
	}
	effect := &world.GroundEffect{
		ID:           world.NextGroundEffectID(),
		SkillID:      skillFreezingBreath,
		NpcID:        freezingBreathEffectNpcID,
		GfxID:        tpl.GfxID,
		Type:         world.GroundEffectFreezingBreath,
		X:            x,
		Y:            y,
		MapID:        mapID,
		ShowID:       caster.ShowID,
		OwnerCharID:  caster.CharID,
		OwnerSession: caster.SessionID,
		OwnerName:    caster.Name,
		OwnerIntel:   caster.Intel,
		OwnerClanID:  caster.ClanID,
		TicksLeft:    durSec * groundEffectTickSec,
	}
	if effect.TicksLeft <= 0 {
		effect.TicksLeft = groundEffectTickSec
	}
	s.deps.World.AddGroundEffect(effect)
	s.broadcastGroundEffect(effect)
}

const (
	skillFoeSlayer     = int32(187)
	skillCopyShockStun = int32(508)

	foeSlayerHitCount        = 3
	foeSlayerDefaultBonusMax = 10
	foeSlayerDefaultStunRate = 15
	foeSlayerDefaultStunSec  = 30
)

const (
	skillMortalBody = int32(191)

	mortalBodyChance      = 23 // Java `_random.nextInt(100) < 23` 觸發率
	mortalBodyDamage      = 40 // Java `int dmg = 40` 反彈基礎傷害
	mortalBodyEffectGfx   = 10710
	mortalBodyAttackerGfx = 2 // Java `S_DoActionGFX(attacker.getId(), 2)`
)

// mortalBodyReflectPvP 處理 PvP 路徑下致命身軀（191）反彈邏輯。
// 對齊 Java `L1PcInstance.java:2775-2798`：當 target 持有 191 buff 且 attacker 非自身、
// 23% 機率觸發 → 攻擊者承受 40 傷害（聖界 IMMUNE_TO_HARM=68 減半）+ 廣播音效，
// 原始攻擊傷害歸零（Java 用 `return` 跳過後續 receiveDamage）。
// 回傳：(可能被歸零的傷害, 是否觸發反彈)。
func mortalBodyReflectPvP(target, attacker *world.PlayerInfo, damage int32, nearby []*world.PlayerInfo) (int32, bool) {
	if damage <= 0 || target == nil || attacker == nil || target.CharID == attacker.CharID {
		return damage, false
	}
	if !target.HasBuff(skillMortalBody) {
		return damage, false
	}
	if world.RandInt(100) >= mortalBodyChance {
		return damage, false
	}
	reflectDmg := int32(mortalBodyDamage)
	reflectDmg = applyImmuneToHarmDamage(attacker, reflectDmg)
	if reflectDmg > 0 {
		attacker.HP -= reflectDmg
		if attacker.HP < 0 {
			attacker.HP = 0
		}
		handler.SendHpUpdate(attacker.Session, attacker)
	}
	// Java `attackPc.sendPacketsAll(S_DoActionGFX(attackPc.getId(), 2))` + `this.sendPacketsAll(S_SkillSound(this.getId(), 10710))`
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(attacker.CharID, mortalBodyAttackerGfx))
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, mortalBodyEffectGfx))
	return 0, true
}

// mortalBodyReflectFromNpc 處理 NPC 攻擊玩家時的致命身軀反彈邏輯（npc_ai.go 攻擊管線）。
// 對齊 Java `L1PcInstance:2788-2796` else-if `attackNpc != null` 分支：
// attackNpc.receiveDamage(this, dmg) + broadcastPacketAll(S_DoActionGFX(npc.getId(), 2))。
// 回傳：(可能被歸零的傷害, 是否觸發反彈)。
func mortalBodyReflectFromNpc(target *world.PlayerInfo, npc *world.NpcInfo, damage int32, nearby []*world.PlayerInfo) (int32, bool) {
	if damage <= 0 || target == nil || npc == nil {
		return damage, false
	}
	if !target.HasBuff(skillMortalBody) {
		return damage, false
	}
	if world.RandInt(100) >= mortalBodyChance {
		return damage, false
	}
	reflectDmg := int32(mortalBodyDamage)
	// 注意：Java `attackNpc.hasSkillEffect(68)` 對 NPC 也檢查（NPC 也可被施 IMMUNE_TO_HARM）。
	// Go npc.HasDebuff(68) 為 NPC 旗標路徑；目前 Go 無對 NPC 套用 IMMUNE_TO_HARM 的場景，但保留 hook。
	if npc.HasDebuff(immuneToHarmSkillID) {
		reflectDmg /= 2
	}
	if reflectDmg > 0 && !npcDamageBlockedBySkm0LikeJava(npc) {
		npc.HP -= reflectDmg
		if npc.HP < 0 {
			npc.HP = 0
		}
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, mortalBodyAttackerGfx))
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, mortalBodyEffectGfx))
	return 0, true
}

func (s *SkillSystem) executeFoeSlayerOnPlayer(sess *net.Session, caster *world.PlayerInfo, skill *data.SkillInfo, target *world.PlayerInfo, nearby []*world.PlayerInfo) {
	if caster == nil || target == nil || skill == nil || target.Dead {
		return
	}
	defer clearDragonKnightWeakness(caster)
	// Java skillmode/FOE_SLAYER.java:27-34 `for (int i = 0; i < 3; i++) { cha.onAction(srcpc); }` 三次攻擊無論目標死亡與否都跑完，
	// 隨後不論目標死活都廣播 7020/12119 並嘗試 COPY_SHOCK_STUN（dead target 的 setParalyzed 為 no-op，但 buff 仍會被 setSkillEffect）。
	for range foeSlayerHitCount {
		damage := s.calcFoeSlayerPlayerHitDamage(caster, target)
		s.broadcastFoeSlayerAttack(caster, target.CharID, damage, nearby)
		s.applyFoeSlayerPlayerDamage(sess, caster, target, damage)
	}
	bonus := foeSlayerRandomBonus(skill)
	if bonus > 0 {
		s.applyFoeSlayerPlayerDamage(sess, caster, target, bonus)
	}
	s.broadcastFoeSlayerEffects(caster.CharID, target.CharID, nearby)
	s.applyFoeSlayerPlayerStun(caster, target, skill, nearby)
}

func (s *SkillSystem) executeFoeSlayerOnNpc(sess *net.Session, caster *world.PlayerInfo, skill *data.SkillInfo, npc *world.NpcInfo, nearby []*world.PlayerInfo) {
	if caster == nil || skill == nil || npc == nil || npc.Dead {
		return
	}
	defer clearDragonKnightWeakness(caster)
	// Java skillmode/FOE_SLAYER.java:61-66 NPC caster 與 PC caster 共用「不中斷三段」邏輯；
	// applyFoeSlayerNpcDamage 對 npc.Dead 已有 guard，迴圈後段呼叫安全 no-op。
	for range foeSlayerHitCount {
		damage := s.calcFoeSlayerNpcHitDamage(caster, npc)
		s.broadcastFoeSlayerAttack(caster, npc.ID, damage, nearby)
		s.applyFoeSlayerNpcDamage(sess, caster, npc, damage, nearby)
	}
	bonus := foeSlayerRandomBonus(skill)
	if bonus > 0 {
		s.applyFoeSlayerNpcDamage(sess, caster, npc, bonus, nearby)
	}
	s.broadcastFoeSlayerEffects(caster.CharID, npc.ID, nearby)
	s.applyFoeSlayerNpcStun(npc, skill, nearby)
}

func (s *SkillSystem) calcFoeSlayerPlayerHitDamage(caster, target *world.PlayerInfo) int32 {
	if s.deps == nil || s.deps.Scripting == nil || target.AbsoluteBarrier {
		return 0
	}
	result := s.deps.Scripting.CalcMeleeAttack(scripting.CombatContext{
		AttackerLevel:   int(caster.Level),
		AttackerSTR:     int(caster.Str),
		AttackerBaseSTR: calcPlayerBaseStrLikeJava(caster),
		AttackerDEX:     int(caster.Dex),
		AttackerWeapon:  s.foeSlayerWeaponDamage(caster, "small"),
		AttackerHitMod:  int(caster.HitMod),
		AttackerDmgMod:  int(caster.DmgMod),
		TargetAC:        int(target.AC),
		TargetLevel:     int(target.Level),
		TargetMR:        0,
	})
	if !result.IsHit || result.Damage <= 0 {
		return 0
	}
	damage := int32(result.Damage)
	if target.HasBuff(112) {
		damage = int32(float64(damage) * 1.58)
	}
	damage += dragonKnightWeaknessFoeSlayerBonus(caster, target.CharID)
	return applyImmuneToHarmDamage(target, damage)
}

func (s *SkillSystem) calcFoeSlayerNpcHitDamage(caster *world.PlayerInfo, npc *world.NpcInfo) int32 {
	if npcDamageBlockedBySkm0LikeJava(npc) {
		return 0
	}
	if s.deps == nil || s.deps.Scripting == nil {
		return 0
	}
	targetSize := npc.Size
	if targetSize == "" {
		targetSize = "small"
	}
	result := s.deps.Scripting.CalcMeleeAttack(scripting.CombatContext{
		AttackerLevel:   int(caster.Level),
		AttackerSTR:     int(caster.Str),
		AttackerBaseSTR: calcPlayerBaseStrLikeJava(caster),
		AttackerDEX:     int(caster.Dex),
		AttackerWeapon:  s.foeSlayerWeaponDamage(caster, targetSize),
		AttackerHitMod:  int(caster.HitMod),
		AttackerDmgMod:  int(caster.DmgMod),
		TargetAC:        int(npc.AC),
		TargetLevel:     int(npc.Level),
		TargetMR:        int(npc.MR),
		TargetClassType: -1,
	})
	if !result.IsHit || result.Damage <= 0 {
		return 0
	}
	damage := int32(result.Damage)
	if npc.HasDebuff(112) {
		damage = int32(float64(damage) * 1.58)
	}
	damage += dragonKnightWeaknessFoeSlayerBonus(caster, npc.ID)
	return damage
}

func (s *SkillSystem) foeSlayerWeaponDamage(caster *world.PlayerInfo, targetSize string) int {
	weaponDmg := 4
	if caster == nil || caster.Equip.Weapon() == nil || s.deps == nil || s.deps.Items == nil {
		return weaponDmg
	}
	info := s.deps.Items.Get(caster.Equip.Weapon().ItemID)
	if info == nil {
		return weaponDmg
	}
	if targetSize == "large" && info.DmgLarge > 0 {
		return info.DmgLarge
	}
	if info.DmgSmall > 0 {
		return info.DmgSmall
	}
	return weaponDmg
}

func (s *SkillSystem) applyFoeSlayerPlayerDamage(sess *net.Session, caster, target *world.PlayerInfo, damage int32) {
	if damage <= 0 || target.Dead {
		return
	}
	if target.Sleeped {
		breakPlayerSleepBySkill(target)
	}
	target.HP -= damage
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
		if s.deps.Death != nil {
			s.deps.Death.KillPlayer(target)
		}
		return
	}
	sendHpUpdate(target.Session, target)
	if caster.AttackView {
		handler.SendDamageNumbers(sess, target.CharID, damage)
	}
}

func (s *SkillSystem) applyFoeSlayerNpcDamage(sess *net.Session, caster *world.PlayerInfo, npc *world.NpcInfo, damage int32, nearby []*world.PlayerInfo) {
	if damage <= 0 || npc.Dead {
		return
	}
	if npcDamageBlockedBySkm0LikeJava(npc) {
		return
	}
	// 副本武器需求檢查（火龍窟「必須裝備真死亡騎士烈炎之劍」）：FoeSlayer 龍騎士魔法傷害同樣受限。
	if npc.WeaponRequired != 0 {
		var equippedID int32
		if wpn := caster.Equip.Weapon(); wpn != nil {
			equippedID = wpn.ItemID
		}
		if !npc.CanReceiveDamageFrom(equippedID) {
			return
		}
	}
	npc.HP -= damage
	if npc.HP < 0 {
		npc.HP = 0
	}
	if npc.Sleeped {
		BreakNpcSleep(npc, s.deps.World)
	}
	AddPlayerHateLikeJava(s.deps.World, npc, caster, damage)
	hpRatio := int16(0)
	if npc.MaxHP > 0 {
		hpRatio = int16((npc.HP * 100) / npc.MaxHP)
	}
	handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
	if caster.AttackView {
		handler.SendDamageNumbers(sess, npc.ID, damage)
	}
	if npc.HP <= 0 {
		handleNpcDeath(npc, caster, nearby, s.deps)
	}
}

func (s *SkillSystem) applyFoeSlayerPlayerStun(caster, target *world.PlayerInfo, skill *data.SkillInfo, nearby []*world.PlayerInfo) {
	if target.HasBuff(skillCopyShockStun) || !foeSlayerStunSuccess(skill) {
		return
	}
	buff := &world.ActiveBuff{
		SkillID:      skillCopyShockStun,
		TicksLeft:    foeSlayerStunSeconds(skill) * 5,
		SetParalyzed: true,
	}
	old := target.AddBuff(buff)
	if old != nil {
		s.revertBuffStats(target, old)
	}
	target.Paralyzed = true
	handler.SendParalysis(target.Session, handler.StunApply)
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, 81162))
	// Java skillmode/FOE_SLAYER.java:48 `L1PinkName.onAction(pc, srcpc)` — PC 目標被 COPY_SHOCK_STUN 命中時觸發粉紅名。
	if s.deps != nil && s.deps.PvP != nil && caster != nil {
		s.deps.PvP.TriggerPinkName(caster, target)
	}
}

func (s *SkillSystem) applyFoeSlayerNpcStun(npc *world.NpcInfo, skill *data.SkillInfo, nearby []*world.PlayerInfo) {
	if npc.HasDebuff(skillCopyShockStun) || !foeSlayerStunSuccess(skill) {
		return
	}
	// Java skillmode/FOE_SLAYER.java:49-53 `else if Monster/Summon/Pet → setParalyzed(true)`：
	// 只對 Monster/Summon/Pet 設 Paralyzed，Guardian/Guard/Tower 等其他 NPC 類型只設 COPY_SHOCK_STUN 計時器與 81162 效果，
	// 不會觸發 setParalyzed（與 SHOCK_STUN NPC 目標處理對齊，見 skill_status.go:508）。
	if npc.Impl == "L1Monster" || npc.Impl == "L1Summon" || npc.Impl == "L1Pet" {
		npc.Paralyzed = true
	}
	npc.AddDebuff(skillCopyShockStun, foeSlayerStunSeconds(skill)*5)
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, 81162))
}

func (s *SkillSystem) broadcastFoeSlayerAttack(caster *world.PlayerInfo, targetID int32, damage int32, nearby []*world.PlayerInfo) {
	for _, viewer := range nearby {
		handler.SendAttackPacket(viewer.Session, caster.CharID, targetID, damage, caster.Heading)
	}
}

func (s *SkillSystem) broadcastFoeSlayerEffects(casterID, targetID int32, nearby []*world.PlayerInfo) {
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(casterID, 7020))
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(targetID, 12119))
}

func foeSlayerRandomBonus(skill *data.SkillInfo) int32 {
	maxBonus := foeSlayerDefaultBonusMax
	if skill != nil {
		if skill.DamageDice > 0 {
			maxBonus = skill.DamageDice
		} else if skill.DamageValue > 0 {
			maxBonus = int(skill.DamageValue)
		}
	}
	if maxBonus <= 0 {
		return 0
	}
	return int32(world.RandInt(maxBonus) + 1)
}

func foeSlayerStunSuccess(skill *data.SkillInfo) bool {
	chance := foeSlayerDefaultStunRate
	if skill != nil && skill.ProbabilityValue > 0 {
		chance = skill.ProbabilityValue
	}
	if chance <= 0 {
		return false
	}
	if chance > 100 {
		chance = 100
	}
	// Java skillmode/FOE_SLAYER.java:40 `_random.nextInt(100) <= FOE_SLAYER_RND`：
	// 邊界為 inclusive，roll 0..FOE_SLAYER_RND 皆視為命中（預設 15 為 16/100=16% 機率）。
	return world.RandInt(100) <= chance
}

func foeSlayerStunSeconds(skill *data.SkillInfo) int {
	if skill != nil && skill.BuffDuration > 0 {
		return skill.BuffDuration
	}
	return foeSlayerDefaultStunSec
}

const (
	dragonKnightClassType = int16(5)
	chainSwordType        = "chainsword"

	dragonKnightWeaknessBaseChance  = 15
	dragonKnightWeaknessOtherChance = 10
	dragonKnightWeaknessOtherItemID = int32(410189)
)

var dragonKnightWeaknessRoll = world.RandInt

func applyDragonKnightWeaknessExposure(player *world.PlayerInfo, targetID int32, weaponItemID int32, weaponType string) {
	if player == nil || player.ClassType != dragonKnightClassType || weaponType != chainSwordType {
		return
	}
	if player.WeaknessTargetID != targetID {
		clearDragonKnightWeakness(player)
		player.WeaknessTargetID = targetID
	}

	chance := dragonKnightWeaknessBaseChance
	if weaponItemID == dragonKnightWeaknessOtherItemID {
		chance += dragonKnightWeaknessOtherChance
	}
	roll := dragonKnightWeaknessRoll(100)
	switch player.WeaknessLevel {
	case 0:
		if roll < chance {
			setDragonKnightWeakness(player, 1)
		}
	case 1:
		if roll < chance {
			setDragonKnightWeakness(player, 1)
		} else if roll < chance*2 {
			setDragonKnightWeakness(player, 2)
		}
	case 2:
		if roll < chance {
			setDragonKnightWeakness(player, 2)
		} else if roll < chance*2 {
			setDragonKnightWeakness(player, 3)
		}
	case 3:
		if roll < chance {
			setDragonKnightWeakness(player, 3)
		}
	}
}

func setDragonKnightWeakness(player *world.PlayerInfo, level int16) {
	player.WeaknessLevel = level
	handler.SendPacketBoxDk(player.Session, level)
}

func clearDragonKnightWeakness(player *world.PlayerInfo) {
	if player == nil {
		return
	}
	player.WeaknessLevel = 0
	player.WeaknessTargetID = 0
	handler.SendPacketBoxDk(player.Session, 0)
}

func dragonKnightWeaknessFoeSlayerBonus(player *world.PlayerInfo, targetID int32) int32 {
	if player == nil || player.WeaknessTargetID != targetID {
		return 0
	}
	switch player.WeaknessLevel {
	case 1:
		return 20 + player.FoeSlayerBonusDmg
	case 2:
		return 40 + player.FoeSlayerBonusDmg
	case 3:
		return 60 + player.FoeSlayerBonusDmg
	default:
		return 0
	}
}

func (s *CombatSystem) applyDragonKnightWeaknessFromMelee(player *world.PlayerInfo, targetID int32) {
	itemID, weaponType, ok := equippedWeaponForWeakness(s.deps, player)
	if !ok {
		return
	}
	applyDragonKnightWeaknessExposure(player, targetID, itemID, weaponType)
}

func (s *PvPSystem) applyDragonKnightWeaknessFromMelee(player *world.PlayerInfo, targetID int32) {
	itemID, weaponType, ok := equippedWeaponForWeakness(s.deps, player)
	if !ok {
		return
	}
	applyDragonKnightWeaknessExposure(player, targetID, itemID, weaponType)
}

func equippedWeaponForWeakness(deps *handler.Deps, player *world.PlayerInfo) (int32, string, bool) {
	if deps == nil || deps.Items == nil || player == nil || player.Equip.Weapon() == nil {
		return 0, "", false
	}
	weapon := player.Equip.Weapon()
	info := deps.Items.Get(weapon.ItemID)
	if info == nil {
		return 0, "", false
	}
	return weapon.ItemID, info.Type, true
}
