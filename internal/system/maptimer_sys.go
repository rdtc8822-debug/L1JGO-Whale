package system

import (
	"context"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// MapTimerSystem 每秒遞減限時地圖計時，時間到則強制傳送出地圖。
// 同時負責每日重置（Java: ServerResetMapTimer + MapTimerThread）。
// Go: Phase 3（PostUpdate），透過 tick 累加器實現每秒觸發。
type MapTimerSystem struct {
	world   *world.State
	deps    *handler.Deps
	lastDay int // 上次重置時的日期（day of year）
}

func NewMapTimerSystem(ws *world.State, deps *handler.Deps) *MapTimerSystem {
	return &MapTimerSystem{
		world:   ws,
		deps:    deps,
		lastDay: time.Now().YearDay(),
	}
}

func (s *MapTimerSystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

func (s *MapTimerSystem) Update(_ time.Duration) {
	// 每日重置檢查（Java: ServerResetMapTimer，每 24 小時執行一次）
	today := time.Now().YearDay()
	if today != s.lastDay {
		s.lastDay = today
		s.resetAllOnlinePlayers()
	}

	// 逐玩家 tick 計時
	s.world.AllPlayers(func(p *world.PlayerInfo) {
		if p.MapTimerGroupIdx <= 0 {
			return // 不在限時地圖
		}

		// tick 累加器：每 5 tick（約 1 秒）觸發一次
		p.MapTimerTickAcc++
		if p.MapTimerTickAcc < 5 {
			return
		}
		p.MapTimerTickAcc = 0

		expired := handler.TickMapTimer(p)
		if expired {
			// 時間到 → 強制傳送到地圖出口
			grp := handler.GetMapTimerGroup(p.MapID)
			if grp == nil {
				// 玩家已不在限時地圖（可能被其他系統傳送）
				p.MapTimerGroupIdx = -1
				return
			}
			handler.TeleportPlayer(p.Session, p, grp.ExitX, grp.ExitY, grp.ExitMapID, grp.ExitHead, s.deps)
		}
	})
}

// resetAllOnlinePlayers 每日重置所有線上玩家的限時地圖時間 + DB 全量重置。
// Java: ServerResetMapTimer.ResetTimingMap()
func (s *MapTimerSystem) resetAllOnlinePlayers() {
	s.deps.Log.Info("每日重置限時地圖計時器")

	// 1. 重置所有線上玩家
	s.world.AllPlayers(func(p *world.PlayerInfo) {
		handler.ResetAllMapTimers(p)
		// 若仍在限時地圖中，重新啟動計時器
		if grp := handler.GetMapTimerGroup(p.MapID); grp != nil {
			handler.OnEnterTimedMap(p.Session, p, p.MapID)
		}
	})

	// 2. DB 全量重置離線玩家
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.deps.CharRepo.ResetAllMapTimes(ctx); err != nil {
		s.deps.Log.Error("每日重置限時地圖 DB 失敗", zap.Error(err))
	}
}
