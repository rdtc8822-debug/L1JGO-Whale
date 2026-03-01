package system

import (
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
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
	lua       *scripting.Engine
	tickCount int
}

func NewRegenSystem(ws *world.State, lua *scripting.Engine) *RegenSystem {
	return &RegenSystem{world: ws, lua: lua}
}

func (s *RegenSystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

func (s *RegenSystem) Update(_ time.Duration) {
	s.tickCount++

	// HP regen check every 5 ticks (1 second), matching Java's 1-second interval.
	// Each player has their own accumulator via RegenHPAcc.
	if s.tickCount%5 == 0 {
		s.world.AllPlayers(func(p *world.PlayerInfo) {
			s.tickHPRegen(p)
		})
	}

	// MP regen: fixed 16-second interval = 80 ticks.
	if s.tickCount%80 == 0 {
		s.world.AllPlayers(func(p *world.PlayerInfo) {
			s.tickMPRegen(p)
		})
	}
}

// tickHPRegen runs once per second. Uses accumulator to determine when to actually regen.
func (s *RegenSystem) tickHPRegen(p *world.PlayerInfo) {
	if p.Dead || p.HP <= 0 || p.HP >= p.MaxHP {
		return
	}
	// 絕對屏障期間停止 HP 回復（Java: pc.stopHpRegeneration）
	if p.AbsoluteBarrier {
		return
	}

	// Increment 1-second accumulator
	p.RegenHPAcc++

	// Regen interval from Lua (level + class based)
	threshold := s.lua.GetHPRegenInterval(int(p.Level), int(p.ClassType))
	if threshold < 1 {
		threshold = 1
	}
	if p.RegenHPAcc < threshold {
		return
	}
	p.RegenHPAcc = 0

	// Calculate HP regen amount via Lua
	maxW := world.PlayerMaxWeight(p)
	amount := s.lua.CalcHPRegenAmount(scripting.HPRegenContext{
		Level:             int(p.Level),
		Con:               int(p.Con),
		HPR:               int(p.HPR),
		Food:              int(p.Food),
		WeightPct:         int(p.Inv.Weight242(maxW)),
		HasExoticVitalize: p.HasBuff(226),
		HasAdditionalFire: p.HasBuff(238),
	})
	if amount == 0 {
		return
	}

	total := int16(amount)
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
	p.Dirty = true
	sendHPUpdatePacket(p.Session, p.HP, p.MaxHP)
}

// tickMPRegen runs every 16 seconds (80 ticks). Matches Java's fixed 64-point threshold.
func (s *RegenSystem) tickMPRegen(p *world.PlayerInfo) {
	if p.Dead || p.MP >= p.MaxMP {
		return
	}
	// 絕對屏障期間停止 MP 回復（Java: pc.stopMpRegeneration）
	if p.AbsoluteBarrier {
		return
	}

	// Calculate MP regen amount via Lua
	maxW := world.PlayerMaxWeight(p)
	amount := s.lua.CalcMPRegenAmount(scripting.MPRegenContext{
		Wis:               int(p.Wis),
		MPR:               int(p.MPR),
		Food:              int(p.Food),
		WeightPct:         int(p.Inv.Weight242(maxW)),
		HasExoticVitalize: p.HasBuff(226),
		HasAdditionalFire: p.HasBuff(238),
		HasBluePotion:     p.HasBuff(1002),
	})
	if amount == 0 {
		return
	}

	total := int16(amount)
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
	p.Dirty = true
	sendMPUpdatePacket(p.Session, p.MP, p.MaxMP)
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
