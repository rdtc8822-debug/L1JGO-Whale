package system

import (
	"time"

	"github.com/l1jgo/server/internal/core/ecs"
	coresys "github.com/l1jgo/server/internal/core/system"
)

// CleanupSystem flushes the deferred entity destruction queue at tick end.
// Phase 6 (Cleanup).
type CleanupSystem struct {
	world *ecs.World
}

func NewCleanupSystem(world *ecs.World) *CleanupSystem {
	return &CleanupSystem{world: world}
}

func (s *CleanupSystem) Phase() coresys.Phase { return coresys.PhaseCleanup }

func (s *CleanupSystem) Update(_ time.Duration) {
	s.world.FlushDestroyQueue()
}
