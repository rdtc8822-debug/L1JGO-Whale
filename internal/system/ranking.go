package system

import (
	"sort"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

// 英雄排名更新間隔（Java: 每 10 分鐘 = 600 秒 = 3000 ticks）
const rankingUpdateTicks = 3000

// RankingSystem 每 10 分鐘按等級排序所有線上玩家，維護 TOP10 全職業 + 各職業 TOP3。
// 影響英雄變身卷軸（GFX 13715-13745）的使用權限。
type RankingSystem struct {
	deps    *handler.Deps
	ws      *world.State
	elapsed int

	// heroNames 存放所有排名上榜的玩家名稱（TOP10 + 各職業 TOP3）
	heroNames map[string]bool
}

// NewRankingSystem 建構英雄排名系統。
func NewRankingSystem(ws *world.State, deps *handler.Deps) *RankingSystem {
	return &RankingSystem{
		deps:      deps,
		ws:        ws,
		heroNames: make(map[string]bool),
	}
}

func (s *RankingSystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

func (s *RankingSystem) Update(_ time.Duration) {
	s.elapsed++
	if s.elapsed < rankingUpdateTicks {
		return
	}
	s.elapsed = 0
	s.recalculate()
}

type rankedPlayer struct {
	Name      string
	Level     int16
	ClassType int16
}

func (s *RankingSystem) recalculate() {
	var players []rankedPlayer
	s.ws.AllPlayers(func(p *world.PlayerInfo) {
		players = append(players, rankedPlayer{
			Name:      p.Name,
			Level:     p.Level,
			ClassType: p.ClassType,
		})
	})

	// 按等級降序排列
	sort.Slice(players, func(i, j int) bool {
		return players[i].Level > players[j].Level
	})

	newHeroes := make(map[string]bool)

	// 全職業 TOP10
	for i := 0; i < len(players) && i < 10; i++ {
		newHeroes[players[i].Name] = true
	}

	// 各職業 TOP3（0=王族, 1=騎士, 2=精靈, 3=法師, 4=黑暗精靈, 5=龍騎士, 6=幻術師）
	classCount := [7]int{}
	for _, p := range players {
		ct := int(p.ClassType)
		if ct < 0 || ct > 6 {
			continue
		}
		if classCount[ct] < 3 {
			newHeroes[p.Name] = true
			classCount[ct]++
		}
	}

	s.heroNames = newHeroes
}

// IsHero 檢查玩家是否在英雄排名中（TOP10 全職業或任一職業 TOP3）。
func (s *RankingSystem) IsHero(name string) bool {
	return s.heroNames[name]
}
