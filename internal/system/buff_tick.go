package system

import (
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

// BuffTickSystem decrements spell buff durations and item magic enchant timers
// for all online players every tick. Phase 2 (Update).
type BuffTickSystem struct {
	world *world.State
	deps  *handler.Deps
}

func NewBuffTickSystem(ws *world.State, deps *handler.Deps) *BuffTickSystem {
	return &BuffTickSystem{world: ws, deps: deps}
}

func (s *BuffTickSystem) Phase() coresys.Phase { return coresys.PhaseUpdate }

func (s *BuffTickSystem) Update(_ time.Duration) {
	s.world.AllPlayers(func(p *world.PlayerInfo) {
		// Track buff count to detect expirations (dirty flag for persistence).
		prevBuffCount := len(p.ActiveBuffs)
		handler.TickPlayerBuffs(p, s.deps)
		tickItemMagicEnchants(p, s.deps)
		TickPlayerPoison(p, s.deps)
		TickPlayerCurse(p, s.deps)
		if len(p.ActiveBuffs) < prevBuffCount {
			p.Dirty = true
		}
	})
}

// tickItemMagicEnchants 遞減裝備魔法附魔計時器。
// 到期時歸零並重新計算裝備屬性。
func tickItemMagicEnchants(p *world.PlayerInfo, deps *handler.Deps) {
	changed := false

	// 武器附魔計時
	weapon := p.Equip.Weapon()
	if weapon != nil && weapon.DmgMagicExpiry > 0 {
		weapon.DmgMagicExpiry--
		if weapon.DmgMagicExpiry <= 0 {
			weapon.DmgByMagic = 0
			weapon.DmgMagicExpiry = 0
			changed = true
		}
	}

	// 防具附魔計時
	armor := p.Equip.Get(world.SlotArmor)
	if armor != nil && armor.AcMagicExpiry > 0 {
		armor.AcMagicExpiry--
		if armor.AcMagicExpiry <= 0 {
			armor.AcByMagic = 0
			armor.AcMagicExpiry = 0
			changed = true
		}
	}

	if changed && p.Session != nil && deps.Equip != nil {
		deps.Equip.RecalcEquipStats(p.Session, p)
	}
}
