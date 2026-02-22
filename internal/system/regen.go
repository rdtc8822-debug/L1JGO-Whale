package system

import (
	"math/rand"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// RegenSystem handles HP/MP regeneration for all online players.
// Phase 3 (PostUpdate) — runs every tick, internal counters gate actual regen.
//
// Java reference:
//   - HpRegeneration.java: 1-second timer, accumulator with level-based threshold
//   - MpRegeneration.java: 1-second timer, fixed 64-point threshold (16 seconds)
//
// Conversion to tick-based:
//   Java runs every 1 second adding 4 points.
//   Go tick = 200ms, so every 5 ticks = 1 second. We add 4 points per 5 ticks.
//   Simplification: accumulate 1 point per tick, thresholds = Java threshold / 4 * 5.
//   Or simpler: count ticks, trigger every N ticks.
//
// Approach: count ticks. HP regen triggers every hpInterval ticks (level-based).
// MP regen triggers every mpInterval ticks (fixed ~16 seconds = 80 ticks).
type RegenSystem struct {
	world     *world.State
	tickCount int
}

func NewRegenSystem(ws *world.State) *RegenSystem {
	return &RegenSystem{world: ws}
}

func (s *RegenSystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

func (s *RegenSystem) Update(_ time.Duration) {
	s.tickCount++

	// HP regen check every 5 ticks (1 second), matching Java's 1-second interval.
	// Each player has their own accumulator via RegenHPAcc.
	if s.tickCount%5 == 0 {
		s.world.AllPlayers(func(p *world.PlayerInfo) {
			tickHPRegen(p)
		})
	}

	// MP regen: fixed 16-second interval = 80 ticks.
	if s.tickCount%80 == 0 {
		s.world.AllPlayers(func(p *world.PlayerInfo) {
			tickMPRegen(p)
		})
	}
}

// ---------- HP Level Table ----------
// Java HpRegeneration.java: lvlTable determines regen threshold.
// Lower value = faster regen. Unit: seconds between regen ticks.
// Index 0=Lv1, ..., 9=Lv18+, 10=Knight Lv30+.
var hpRegenIntervalTable = [11]int{30, 25, 20, 16, 14, 12, 11, 10, 9, 3, 2}

// hpRegenSeconds returns the number of seconds between HP regen events.
// Java: regenMax = lvlTable[regenLvl-1] * 4, with _curPoint=4 added per second.
// So threshold / 4 = seconds to regen.
func hpRegenSeconds(level int16, classType int16) int {
	idx := int(level)
	if idx < 1 {
		idx = 1
	}
	if idx > 10 {
		idx = 10
	}
	// Knight Lv30+ gets index 11 (value=2)
	if level >= 30 && classType == 1 { // 1=Knight
		return hpRegenIntervalTable[10] // 2 seconds
	}
	return hpRegenIntervalTable[idx-1]
}

// tickHPRegen runs once per second. Uses accumulator to determine when to actually regen.
func tickHPRegen(p *world.PlayerInfo) {
	if p.Dead || p.HP <= 0 || p.HP >= p.MaxHP {
		return
	}

	// Increment 1-second accumulator
	p.RegenHPAcc++

	threshold := hpRegenSeconds(p.Level, p.ClassType)
	if p.RegenHPAcc < threshold {
		return
	}
	p.RegenHPAcc = 0

	// --- Calculate HP regen amount ---

	// CON bonus: only Lv12+, CON >= 14. Java: random(CON-12)+1, cap 14.
	maxBonus := 1
	if p.Level > 11 && p.Con >= 14 {
		maxBonus = int(p.Con) - 12
		if maxBonus > 14 {
			maxBonus = 14
		}
	}
	bonus := rand.Intn(maxBonus) + 1

	// Equipment HPR (from buffs and items)
	equipHPR := int(p.HPR)

	// Skill bonuses (future: NATURES_TOUCH +15, cooking, house, inn, elf forest)
	// TODO: add when skill/location systems are implemented

	// --- Penalty checks: food < 3 OR overweight → zero base regen ---
	if isHPRegenBlocked(p) {
		bonus = 0
		if equipHPR > 0 {
			equipHPR = 0 // positive equipment bonus also blocked
		}
		// Negative equipment HPR still applies (cursed items)
	}

	total := int16(bonus + equipHPR)
	if total == 0 {
		return
	}

	newHP := p.HP + total
	if newHP < 1 {
		newHP = 1
	}
	if newHP > p.MaxHP {
		newHP = p.MaxHP
	}
	if newHP == p.HP {
		return
	}
	p.HP = newHP
	sendHPUpdatePacket(p.Session, p.HP, p.MaxHP)
}

// tickMPRegen runs every 16 seconds (80 ticks). Matches Java's fixed 64-point threshold.
func tickMPRegen(p *world.PlayerInfo) {
	if p.Dead || p.MP >= p.MaxMP {
		return
	}

	// --- WIS-based MP regen ---
	// Java: WIS 15-16 → 2, WIS >= 17 → 3, else 1
	baseMPR := 1
	wis := int(p.Wis)
	if wis == 15 || wis == 16 {
		baseMPR = 2
	} else if wis >= 17 {
		baseMPR = 3
	}

	// Blue Potion bonus: WIS min 11, +WIS-10
	// Check for STATUS_BLUE_POTION (skillID 75 in Java enum)
	if p.HasBuff(75) {
		effWis := wis
		if effWis < 11 {
			effWis = 11
		}
		baseMPR += effWis - 10
	}

	// Equipment MPR (from buffs and items)
	equipMPR := int(p.MPR)

	// Skill bonuses (future: cooking, house, inn, elf forest)
	// TODO: add when skill/location systems are implemented

	// --- Penalty checks: food < 3 OR overweight → zero base regen ---
	if isMPRegenBlocked(p) {
		baseMPR = 0
		if equipMPR > 0 {
			equipMPR = 0
		}
	}

	total := int16(baseMPR + equipMPR)
	if total == 0 {
		return
	}

	newMP := p.MP + total
	if newMP < 0 {
		newMP = 0
	}
	if newMP > p.MaxMP {
		newMP = p.MaxMP
	}
	if newMP == p.MP {
		return
	}
	p.MP = newMP
	sendMPUpdatePacket(p.Session, p.MP, p.MaxMP)
}

// ---------- Penalty checks ----------

// isHPRegenBlocked returns true if HP regen should be suppressed.
// Java HpRegeneration: food < 3 OR Weight242 >= 121 (unless EXOTIC_VITALIZE / ADDITIONAL_FIRE).
func isHPRegenBlocked(p *world.PlayerInfo) bool {
	// Food check
	if p.Food < 3 {
		return true
	}
	// Weight check
	if isOverWeight(p, 121) {
		return true
	}
	return false
}

// isMPRegenBlocked returns true if MP regen should be suppressed.
// Java MpRegeneration: food < 3 OR Weight242 >= 120 (unless EXOTIC_VITALIZE / ADDITIONAL_FIRE).
func isMPRegenBlocked(p *world.PlayerInfo) bool {
	if p.Food < 3 {
		return true
	}
	if isOverWeight(p, 120) {
		return true
	}
	return false
}

// isOverWeight returns true if the player's Weight242 >= threshold.
// Java: checks EXOTIC_VITALIZE and ADDITIONAL_FIRE skills to override.
func isOverWeight(p *world.PlayerInfo, threshold int) bool {
	maxW := world.MaxWeight(p.Str, p.Con)
	w242 := int(p.Inv.Weight242(maxW))
	if w242 < threshold {
		return false
	}
	// EXOTIC_VITALIZE (skill 226) and ADDITIONAL_FIRE (skill 238) negate overweight
	if p.HasBuff(226) || p.HasBuff(238) {
		return false
	}
	return true
}

// ---------- Packet helpers ----------
// These duplicate the minimal packet builders to avoid circular import with handler/.

func sendHPUpdatePacket(sess *net.Session, hp, maxHP int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteH(uint16(hp))
	w.WriteH(uint16(maxHP))
	sess.Send(w.Bytes())
}

func sendMPUpdatePacket(sess *net.Session, mp, maxMP int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MANA_POINT)
	w.WriteH(uint16(mp))
	w.WriteH(uint16(maxMP))
	sess.Send(w.Bytes())
}
