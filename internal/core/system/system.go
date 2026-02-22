package system

import "time"

// Phase defines execution ordering within a single tick.
type Phase int

const (
	PhaseInput      Phase = iota // 0: drain packet queues
	PhasePreUpdate               // 1: process last tick's events
	PhaseUpdate                  // 2: game logic
	PhasePostUpdate              // 3: regen, spawn, visibility
	PhaseOutput                  // 4: build + send packets
	PhasePersist                 // 5: WAL flush + batch save
	PhaseCleanup                 // 6: destroy queued entities
)

// System is the interface every ECS system implements.
type System interface {
	Phase() Phase
	Update(dt time.Duration)
}
