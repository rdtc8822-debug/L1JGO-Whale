package system

import (
	"sort"
	"time"
)

// Runner executes systems in phase order each tick.
type Runner struct {
	systems []System
	sorted  bool
}

func NewRunner() *Runner {
	return &Runner{
		systems: make([]System, 0, 16),
	}
}

func (r *Runner) Register(s System) {
	r.systems = append(r.systems, s)
	r.sorted = false
}

func (r *Runner) Tick(dt time.Duration) {
	if !r.sorted {
		sort.Slice(r.systems, func(i, j int) bool {
			return r.systems[i].Phase() < r.systems[j].Phase()
		})
		r.sorted = true
	}
	for _, s := range r.systems {
		s.Update(dt)
	}
}
