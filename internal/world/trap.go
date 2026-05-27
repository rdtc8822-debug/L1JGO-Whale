package world

import (
	"math/rand"
	"time"

	"github.com/l1jgo/server/internal/data"
)

// trapTileKey 陷阱座標查詢鍵（O(1) 查詢用）。
type trapTileKey struct {
	X     int32
	Y     int32
	MapID int16
}

// TrapInstance 運行時陷阱實例。
type TrapInstance struct {
	Template *data.TrapTemplate // 陷阱範本
	Spawn    *data.TrapSpawn    // 生成點定義
	X        int32              // 當前座標 X
	Y        int32              // 當前座標 Y
	MapID    int16              // 地圖 ID
	Alive    bool               // 是否存活（觸發後變 false）
	SpanSec  int32              // 重生秒數（0=一次性）
}

// trapRespawnEntry 待重生的陷阱。
type trapRespawnEntry struct {
	Trap      *TrapInstance
	RespawnAt time.Time
}

// TrapManager 陷阱管理器。
// 使用 tile-based map 提供 O(1) 座標查詢，比 Java 的 O(N) 遍歷效率更高。
// 單線程遊戲迴圈保證安全，不需要鎖。
type TrapManager struct {
	byTile         map[trapTileKey][]*TrapInstance // 座標 → 該位置的陷阱列表
	allTraps       []*TrapInstance                 // 所有陷阱實例
	pendingRespawn []trapRespawnEntry              // 待重生佇列
	mapChecker     MapPassableChecker              // 地圖通行性檢查（可選）
}

// MapPassableChecker 地圖通行性檢查介面（用於隨機座標驗證）。
type MapPassableChecker interface {
	IsInMap(mapID int16, x, y int32) bool
	IsPassablePoint(mapID int16, x, y int32) bool
}

// NewTrapManager 從陷阱資料建立管理器，生成所有陷阱實例。
func NewTrapManager(trapData *data.TrapData, checker MapPassableChecker) *TrapManager {
	mgr := &TrapManager{
		byTile:     make(map[trapTileKey][]*TrapInstance),
		mapChecker: checker,
	}

	for i := range trapData.Spawns {
		sp := &trapData.Spawns[i]
		tpl := trapData.Templates[sp.TrapID]
		if tpl == nil {
			continue
		}

		// 重生秒數：Java 中 span 為毫秒，_span = span / 1000
		// ServerTrapTimer 每 5 秒加 1，_stop >= _span 時重生
		// 實際重生時間 = (span/1000) * 5 秒
		spanSec := int32(0)
		if sp.Span > 0 {
			spanSec = (sp.Span / 1000) * 5
			if spanSec <= 0 {
				spanSec = 5 // 至少 5 秒
			}
		}

		// 每個生成點可產生多個陷阱實例
		for j := int32(0); j < sp.Count; j++ {
			inst := &TrapInstance{
				Template: tpl,
				Spawn:    sp,
				MapID:    int16(sp.MapID),
				Alive:    true,
				SpanSec:  spanSec,
			}
			// 計算初始座標
			mgr.randomizePosition(inst)
			mgr.allTraps = append(mgr.allTraps, inst)
			mgr.registerTile(inst)
		}
	}

	return mgr
}

// randomizePosition 為陷阱實例計算隨機座標。
// Java: L1TrapInstance.resetLocation() — 在 baseLoc ± rndPt 範圍內隨機選取可通行座標。
func (mgr *TrapManager) randomizePosition(inst *TrapInstance) {
	sp := inst.Spawn
	if sp.RndX == 0 && sp.RndY == 0 {
		// 固定座標
		inst.X = sp.X
		inst.Y = sp.Y
		return
	}

	// 嘗試 50 次找到可通行座標（與 Java 一致）
	for i := 0; i < 50; i++ {
		dx := rand.Int31n(sp.RndX+1) * int32(randomSign())
		dy := rand.Int31n(sp.RndY+1) * int32(randomSign())
		nx := sp.X + dx
		ny := sp.Y + dy

		if mgr.mapChecker != nil {
			if !mgr.mapChecker.IsInMap(int16(sp.MapID), nx, ny) {
				continue
			}
			if !mgr.mapChecker.IsPassablePoint(int16(sp.MapID), nx, ny) {
				continue
			}
		}
		inst.X = nx
		inst.Y = ny
		return
	}

	// 50 次都失敗，使用基準座標
	inst.X = sp.X
	inst.Y = sp.Y
}

// randomSign 隨機回傳 +1 或 -1。
func randomSign() int {
	if rand.Intn(2) == 0 {
		return 1
	}
	return -1
}

// registerTile 將陷阱註冊到座標索引。
func (mgr *TrapManager) registerTile(inst *TrapInstance) {
	key := trapTileKey{X: inst.X, Y: inst.Y, MapID: inst.MapID}
	mgr.byTile[key] = append(mgr.byTile[key], inst)
}

// unregisterTile 從座標索引移除陷阱。
func (mgr *TrapManager) unregisterTile(inst *TrapInstance) {
	key := trapTileKey{X: inst.X, Y: inst.Y, MapID: inst.MapID}
	traps := mgr.byTile[key]
	for i, t := range traps {
		if t == inst {
			mgr.byTile[key] = append(traps[:i], traps[i+1:]...)
			break
		}
	}
	if len(mgr.byTile[key]) == 0 {
		delete(mgr.byTile, key)
	}
}

// GetTrapsAt 取得指定座標的所有存活陷阱。
// 移動時呼叫，O(1) 查詢。
func (mgr *TrapManager) GetTrapsAt(x, y int32, mapID int16) []*TrapInstance {
	key := trapTileKey{X: x, Y: y, MapID: mapID}
	traps := mgr.byTile[key]
	if len(traps) == 0 {
		return nil
	}
	// 只回傳存活的
	var alive []*TrapInstance
	for _, t := range traps {
		if t.Alive {
			alive = append(alive, t)
		}
	}
	return alive
}

// GetTrapsInScreen 取得指定座標畫面內的所有存活陷阱。
// Java: WorldTrap.onDetection() 使用 L1Location.isInScreen()。
func (mgr *TrapManager) GetTrapsInScreen(x, y int32, mapID int16) []*TrapInstance {
	if mgr == nil {
		return nil
	}
	var traps []*TrapInstance
	for _, t := range mgr.allTraps {
		if t == nil || !t.Alive || t.MapID != mapID {
			continue
		}
		if trapInScreenLikeJava(x, y, t.X, t.Y) {
			traps = append(traps, t)
		}
	}
	return traps
}

func trapInScreenLikeJava(x, y, targetX, targetY int32) bool {
	dist := absTrap32(targetX-x) + absTrap32(targetY-y)
	if dist > 19 {
		return false
	}
	if dist <= 18 {
		return true
	}
	dist2 := absTrap32(targetX-(x-18)) + absTrap32(targetY-(y-18))
	return dist2 >= 19 && dist2 <= 52
}

func absTrap32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// DisableTrap 觸發後停用陷阱 + 排入重生佇列。
func (mgr *TrapManager) DisableTrap(inst *TrapInstance) {
	inst.Alive = false
	mgr.unregisterTile(inst)

	if inst.SpanSec > 0 {
		mgr.pendingRespawn = append(mgr.pendingRespawn, trapRespawnEntry{
			Trap:      inst,
			RespawnAt: time.Now().Add(time.Duration(inst.SpanSec) * time.Second),
		})
	}
}

// ProcessRespawns 處理到期的陷阱重生。
// 由 TrapRespawnSystem 每 tick 呼叫。
func (mgr *TrapManager) ProcessRespawns(now time.Time) {
	if len(mgr.pendingRespawn) == 0 {
		return
	}

	remaining := mgr.pendingRespawn[:0]
	for _, entry := range mgr.pendingRespawn {
		if now.Before(entry.RespawnAt) {
			remaining = append(remaining, entry)
			continue
		}
		// 重生：重新隨機位置 + 啟用
		inst := entry.Trap
		mgr.randomizePosition(inst)
		inst.Alive = true
		mgr.registerTile(inst)
	}
	mgr.pendingRespawn = remaining
}

// Count 回傳陷阱實例總數。
func (mgr *TrapManager) Count() int {
	return len(mgr.allTraps)
}

// TileCount 回傳有陷阱的座標數量。
func (mgr *TrapManager) TileCount() int {
	return len(mgr.byTile)
}
