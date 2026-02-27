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
		handler.TickItemMagicEnchants(p, s.deps)
		handler.TickPlayerPoison(p, s.deps)
		handler.TickPlayerCurse(p, s.deps)
		if len(p.ActiveBuffs) < prevBuffCount {
			p.Dirty = true
		}
	})
}
