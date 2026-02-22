package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

// HandleBuySpell processes C_BUY_SPELL (opcode 39).
// Client sends this when talking to a magic shop NPC to see available spells.
func HandleBuySpell(sess *net.Session, r *packet.Reader, deps *Deps) {
	_ = r.ReadD() // NPC object ID (unused — we check by class)
	openSpellShop(sess, deps)
}

// openSpellShop sends the spell shop list to the player.
// Called from both C_BUY_SPELL and NPC action "buyskill".
func openSpellShop(sess *net.Session, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	available := getAvailableSpells(player, deps)
	if len(available) == 0 {
		return
	}

	sendSkillBuy(sess, available)
}

// HandleBuyableSpell processes C_BUYABLE_SPELL (opcode 145).
// Client sends this with the list of spells the player selected to buy.
func HandleBuyableSpell(sess *net.Session, r *packet.Reader, deps *Deps) {
	count := int(r.ReadH())
	if count <= 0 || count > 128 {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Read requested skill IDs
	requested := make([]int32, 0, count)
	for i := 0; i < count; i++ {
		skillID := r.ReadD()
		if skillID > 0 {
			requested = append(requested, skillID)
		}
	}
	if len(requested) == 0 {
		return
	}

	// Build set of already-known spells for quick lookup
	knownSet := make(map[int32]bool, len(player.KnownSpells))
	for _, sid := range player.KnownSpells {
		knownSet[sid] = true
	}

	tiers := deps.Scripting.GetSpellTiers(int(player.ClassType))
	if len(tiers) == 0 {
		return
	}

	// Calculate total cost and validate each spell
	totalCost := int32(0)
	var validSpells []*data.SkillInfo
	for _, skillID := range requested {
		if knownSet[skillID] {
			continue // already known
		}
		skill := deps.Skills.Get(skillID)
		if skill == nil {
			continue
		}
		// Find the matching tier for this skill
		cost, ok := getSpellCost(skill, tiers, int(player.Level))
		if !ok {
			continue // class/level can't learn this
		}
		totalCost += cost
		validSpells = append(validSpells, skill)
	}

	if len(validSpells) == 0 {
		return
	}

	// Check adena
	currentGold := player.Inv.GetAdena()
	if currentGold < totalCost {
		sendServerMessage(sess, 189) // 金幣不足
		return
	}

	// Deduct adena
	adenaItem := player.Inv.FindByItemID(world.AdenaItemID)
	if adenaItem == nil {
		return
	}
	adenaItem.Count -= totalCost
	if adenaItem.Count <= 0 {
		player.Inv.RemoveItem(adenaItem.ObjectID, 0)
		sendRemoveInventoryItem(sess, adenaItem.ObjectID)
	} else {
		sendItemCountUpdate(sess, adenaItem)
	}

	// Learn each spell
	for _, skill := range validSpells {
		player.KnownSpells = append(player.KnownSpells, skill.SkillID)
		sendAddSingleSkill(sess, skill)
	}

	// Play learn sound effect (GFX 224 on player)
	sendSkillEffect(sess, player.CharID, 224)

	deps.Log.Info(fmt.Sprintf("玩家學習魔法  角色=%s  數量=%d  花費=%d", player.Name, len(validSpells), totalCost))
}

// getAvailableSpells returns spells the player can learn but doesn't know yet.
// Spell tier data is defined in Lua (scripts/skill/spellshop.lua).
func getAvailableSpells(player *world.PlayerInfo, deps *Deps) []*data.SkillInfo {
	tiers := deps.Scripting.GetSpellTiers(int(player.ClassType))
	if len(tiers) == 0 {
		return nil
	}

	// Build set of already-known spells
	knownSet := make(map[int32]bool, len(player.KnownSpells))
	for _, sid := range player.KnownSpells {
		knownSet[sid] = true
	}

	var available []*data.SkillInfo
	for _, sk := range deps.Skills.All() {
		if knownSet[sk.SkillID] {
			continue
		}
		// Check if this skill's level is within any of the class's tiers
		// and the player meets the level requirement
		if _, ok := getSpellCost(sk, tiers, int(player.Level)); ok {
			available = append(available, sk)
		}
	}
	return available
}

// getSpellCost returns the cost for learning a spell, or false if the class/level can't learn it.
func getSpellCost(skill *data.SkillInfo, tiers []scripting.SpellTierInfo, charLevel int) (int32, bool) {
	for _, tier := range tiers {
		if skill.SkillLevel >= tier.MinSkillLevel && skill.SkillLevel <= tier.MaxSkillLevel {
			if charLevel >= tier.MinCharLevel {
				return int32(tier.Cost), true
			}
			return 0, false // right class but level too low
		}
	}
	return 0, false // wrong class
}

// sendSkillBuy sends S_SkillBuy (opcode 23) — lists available spells for purchase.
func sendSkillBuy(sess *net.Session, skills []*data.SkillInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SKILL_BUY)
	w.WriteD(100)                     // flag (matches Java)
	w.WriteH(uint16(len(skills)))
	for _, sk := range skills {
		w.WriteD(sk.SkillID)
	}
	sess.Send(w.Bytes())
}
