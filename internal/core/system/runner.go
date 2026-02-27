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
	r.ensureSorted()
	for _, s := range r.systems {
		s.Update(dt)
	}
}

// TickPhase 只執行指定 Phase 的 System。
// 用於高頻輸入輪詢：在系統 tick 之間只跑 Phase 0，
// 讓封包處理延遲從 0~200ms 降至 0~2ms，同時保持架構合規。
func (r *Runner) TickPhase(phase Phase, dt time.Duration) {
	r.ensureSorted()
	for _, s := range r.systems {
		if s.Phase() == phase {
			s.Update(dt)
		}
	}
}

func (r *Runner) ensureSorted() {
	if !r.sorted {
		sort.Slice(r.systems, func(i, j int) bool {
			return r.systems[i].Phase() < r.systems[j].Phase()
		})
		r.sorted = true
	}
}
