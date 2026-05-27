package system

import (
	"time"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

func (s *SkillSystem) castMagicScrollSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID, targetX, targetY int32, now time.Time) {
	if s == nil || s.deps == nil || player == nil || skill == nil {
		return
	}
	previousSuppress := s.suppressCastFailMessage
	s.suppressCastFailMessage = true
	defer func() {
		s.suppressCastFailMessage = previousSuppress
	}()
	skillID := skill.SkillID
	if player.AbsoluteBarrier {
		s.cancelAbsoluteBarrier(player)
	}
	if player.Invisible {
		s.cancelInvisibility(player)
	}
	if player.Paralyzed || player.Sleeped || player.Silenced {
		return
	}
	if player.PolyID != 0 && s.deps.Polys != nil {
		poly := s.deps.Polys.GetByID(player.PolyID)
		if poly != nil && !poly.CanUseSkill {
			handler.SendServerMessage(sess, 285)
			return
		}
	}

	delay := skill.ReuseDelay
	if delay <= 0 {
		delay = 1000
	}
	player.SkillDelayUntil = now.Add(time.Duration(delay) * time.Millisecond)

	if skillID == 5 || skillID == 69 {
		s.executeTeleportSpell(sess, player, skill, 0)
		return
	}
	if s.deps.Summon != nil {
		switch skillID {
		case 51:
			s.deps.Summon.ExecuteSummonMonster(sess, player, skill, 0)
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
	switch skillID {
	case 116, 118:
		s.executeClanTargetSkill(sess, player, skill, targetID, "", false)
		return
	}
	if isGroundTargetSkill(skillID) {
		if isCubeSkill(skillID) && s.hasNearbySameCube(player, skillID) {
			handler.SendServerMessage(sess, 1412)
			return
		}
		s.executeGroundTargetSkill(sess, player, skill, targetX, targetY)
		return
	}
	if s.isResurrectionSkill(skill) {
		s.executeResurrection(sess, player, skill, targetID)
		return
	}
	switch skillID {
	case 21:
		s.executeArmorEnchant(sess, player, skill, targetID)
		return
	case 48:
		s.executeBlessWeaponEnchant(sess, player, skill, targetID)
		return
	case 12, 107:
		s.executeTargetedWeaponEnchant(sess, player, skill, targetID)
		return
	case 73:
		s.executeCreateMagicalWeapon(sess, player, skill, targetID)
		return
	case 100:
		s.executeBringStone(sess, player, skill, targetID)
		return
	}

	switch skill.Target {
	case "attack":
		s.executeAttackSkill(sess, player, skill, targetID)
	case "buff":
		s.executeBuffSkill(sess, player, skill, targetID)
	default:
		s.executeSelfSkill(sess, player, skill)
	}
}
