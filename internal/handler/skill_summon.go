package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

// HandleSummonRingSelection is called from HandleNpcAction when the player responds to
// the "summonlist" HTML dialog with a numeric string (e.g. "7", "263", "519").
// Java: L1ActionPc.java checks isSummonMonster() → calls summonMonster(pc, cmd).
func HandleSummonRingSelection(sess *net.Session, player *world.PlayerInfo, summonIDStr string, deps *Deps) {
	if deps.Summon == nil {
		return
	}

	// Parse the summon selection string to int32
	var summonID int32
	for _, c := range summonIDStr {
		if c < '0' || c > '9' {
			return // invalid
		}
		summonID = summonID*10 + int32(c-'0')
	}
	if summonID == 0 {
		return
	}

	// Look up the skill info for Summon Monster (skill 51) to consume resources
	skill := deps.Skills.Get(51)
	if skill == nil {
		return
	}

	// Re-validate MP/HP/materials (player state may have changed since dialog was shown)
	if skill.HpConsume > 0 && player.HP <= int16(skill.HpConsume) {
		sendServerMessage(sess, msgNotEnoughHP)
		return
	}
	if skill.MpConsume > 0 && player.MP < int16(skill.MpConsume) {
		sendServerMessage(sess, msgNotEnoughMP)
		return
	}
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot == nil || slot.Count < int32(skill.ItemConsumeCount) {
			sendServerMessage(sess, 299) // 施放魔法所需材料不足
			return
		}
	}

	// Delegate to SummonSystem.ExecuteSummonMonster with the selected summon ID as targetID.
	deps.Summon.ExecuteSummonMonster(sess, player, skill, summonID)
}

// DismissSummon handles voluntary summon dismissal (from NPC action menu).
// Tamed summons are liberated; skill-summoned are destroyed.
func DismissSummon(sum *world.SummonInfo, player *world.PlayerInfo, deps *Deps) {
	if deps.Summon != nil {
		deps.Summon.DismissSummon(sum, player)
	}
}