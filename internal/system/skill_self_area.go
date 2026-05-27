package system

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

func isSelfAreaAttackSkill(skill *data.SkillInfo) bool {
	return skill.Type == 64 && skill.Area > 0 && (skill.DamageValue > 0 || skill.DamageDice > 0)
}

func (s *SkillSystem) executeSelfAreaAttackSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, nearby []*world.PlayerInfo) {
	targets := make([]handler.RangeSkillTarget, 0, 8)
	for _, target := range nearby {
		if target.CharID == player.CharID || target.Dead {
			continue
		}
		if chebyshevDist(player.X, player.Y, target.X, target.Y) > int32(skill.Area) {
			continue
		}
		if !s.canSkillReachTarget(player, skill, target.MapID, target.X, target.Y) {
			continue
		}
		if s.tryCounterMagic(target, skill.SkillID) {
			continue
		}
		res := s.deps.Scripting.CalcSkillDamage(s.buildPlayerSkillDamageContext(player, target, skill))
		dmg := int32(res.Damage)
		applied := s.applySelfAreaSkillDamageToPlayerNoVisual(sess, player, target, skill, dmg, nearby)
		targets = append(targets, handler.RangeSkillTarget{ObjectID: target.CharID, Hit: applied > 0, Damage: applied})
	}

	nearbyNpcs := s.deps.World.GetNearbyNpcs(player.X, player.Y, player.MapID)
	for _, npc := range nearbyNpcs {
		if npc.Dead {
			continue
		}
		if chebyshevDist(player.X, player.Y, npc.X, npc.Y) > int32(skill.Area) {
			continue
		}
		if !s.canSkillReachTarget(player, skill, npc.MapID, npc.X, npc.Y) {
			continue
		}
		dmg := s.calcSelfAreaSkillNpcDamage(player, npc, skill)
		targets = append(targets, handler.RangeSkillTarget{ObjectID: npc.ID, Hit: dmg > 0, Damage: dmg})
		s.applySelfAreaSkillDamageToNpcNoVisual(sess, player, skill, npc, dmg, nearby)
	}

	if skill.CastGfx > 0 {
		data := handler.BuildRangeSkill(
			player.CharID,
			player.X,
			player.Y,
			player.Heading,
			skill.CastGfx,
			byte(skill.ActionID),
			handler.RangeSkillTypeNoDir,
			targets,
		)
		handler.BroadcastToPlayers(nearby, data)
	}
}

func (s *SkillSystem) applySelfAreaSkillDamageToPlayerNoVisual(sess *net.Session, player, target *world.PlayerInfo, skill *data.SkillInfo, dmg int32, nearby []*world.PlayerInfo) int32 {
	if dmg < 0 {
		dmg = 0
	}
	if target.AbsoluteBarrier {
		dmg = 0
	}
	dmg = applyImmuneToHarmDamage(target, dmg)
	dmg = applyDragonEyeMagicDamageReductionLikeYiwei(target, dmg, world.RandInt(100), nearby)
	dmg = s.applyCounterMirrorMagicDamage(player, target, dmg, world.RandInt(100), nearby)

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

func (s *SkillSystem) calcSelfAreaSkillNpcDamage(player *world.PlayerInfo, npc *world.NpcInfo, skill *data.SkillInfo) int32 {
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
	}
	res := s.deps.Scripting.CalcSkillDamage(ctx)
	return int32(res.Damage)
}

func (s *SkillSystem) applySelfAreaSkillDamageToNpcNoVisual(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, npc *world.NpcInfo, dmg int32, nearby []*world.PlayerInfo) {
	if dmg < 0 {
		dmg = 0
	}
	if dmg > 0 && npcDamageBlockedBySkm0LikeJava(npc) {
		dmg = 0
	}
	if dmg > 0 && !(skill.DamageValue == 0 && skill.DamageDice == 0) {
		dmg = s.applyNpcKirtasMirrorMagicDamageLikeJava(player, npc, dmg, nearby)
	}
	// 副本武器需求檢查（火龍窟「必須裝備真死亡騎士烈炎之劍」）：自我中心 AoE 技能對副本怪同樣需符合武器需求。
	if dmg > 0 && npc.WeaponRequired != 0 {
		var equippedID int32
		if wpn := player.Equip.Weapon(); wpn != nil {
			equippedID = wpn.ItemID
		}
		if !npc.CanReceiveDamageFrom(equippedID) {
			dmg = 0
		}
	}
	if player.AttackView {
		handler.SendDamageNumbers(sess, npc.ID, dmg)
	}
	npc.HP -= dmg
	if npc.HP < 0 {
		npc.HP = 0
	}
	AddPlayerHateLikeJava(s.deps.World, npc, player, dmg)
	hpRatio := int16(0)
	if npc.MaxHP > 0 {
		hpRatio = int16((npc.HP * 100) / npc.MaxHP)
	}
	handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
	if npc.HP <= 0 {
		handleNpcDeath(npc, player, nearby, s.deps)
		return
	}

	if skill.SkillID == 80 && !npc.Paralyzed && !npc.HasDebuff(50) && !npc.HasDebuff(80) {
		if s.checkNpcMRResist(player, npc, skill.SkillID) {
			dur := skill.BuffDuration
			if dur <= 0 {
				dur = 16
			}
			npc.Paralyzed = true
			npc.AddDebuff(80, (dur+1)*5)
			handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
		}
	}
}
