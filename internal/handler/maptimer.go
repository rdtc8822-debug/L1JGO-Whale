package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// ═══════════════════════════════════════════════════════════════
// 地圖定時器（MapTimer）系統
// Java: MapTimerThread.java, S_MapTimer.java, S_MapTimerOut.java
// ═══════════════════════════════════════════════════════════════

// MapTimerGroup 定義一組限時地圖。
// Java: MapsGroupTable / mapids_group 資料表。
type MapTimerGroup struct {
	OrderID    int     // 組別 ID（1-based，用於 DB 持久化）
	Name       string  // 顯示名稱（Ctrl+Q 用）
	MaxTimeSec int     // 最大停留時間（秒）
	MapIDs     []int16 // 屬於此組的所有地圖 ID
	ExitX      int32   // 時間到時的傳送目標 X
	ExitY      int32   // 時間到時的傳送目標 Y
	ExitMapID  int16   // 時間到時的傳送目標地圖
	ExitHead   int16   // 時間到時的傳送面向
}

// mapTimerGroups 所有限時地圖組（對照 Java mapids_group 資料表）。
var mapTimerGroups = []MapTimerGroup{
	{
		OrderID: 1, Name: "龍之谷地監", MaxTimeSec: 7200,
		MapIDs:  []int16{560, 561, 562, 563, 564, 565, 566},
		ExitX:   33443, ExitY: 32800, ExitMapID: 4, ExitHead: 5,
	},
	{
		OrderID: 2, Name: "奇岩/古魯丁地監", MaxTimeSec: 7200,
		MapIDs:  []int16{807, 808, 809, 810, 811, 812, 813, 567, 568, 569, 570},
		ExitX:   33443, ExitY: 32800, ExitMapID: 4, ExitHead: 5,
	},
	{
		OrderID: 3, Name: "象牙塔", MaxTimeSec: 7200,
		MapIDs:  []int16{280, 281, 282, 283, 284, 285, 286, 287, 288, 289},
		ExitX:   33443, ExitY: 32800, ExitMapID: 4, ExitHead: 5,
	},
	{
		OrderID: 4, Name: "新遺忘之島", MaxTimeSec: 7200,
		MapIDs:  []int16{1700},
		ExitX:   33443, ExitY: 32800, ExitMapID: 4, ExitHead: 5,
	},
	{
		OrderID: 5, Name: "新傲慢之塔", MaxTimeSec: 7200,
		MapIDs:  []int16{3301, 3302, 3303, 3304, 3305, 3306, 3307, 3308, 3309, 3310, 7100},
		ExitX:   33443, ExitY: 32800, ExitMapID: 4, ExitHead: 5,
	},
	{
		OrderID: 6, Name: "拉斯塔巴德地監", MaxTimeSec: 7200,
		MapIDs:  []int16{633},
		ExitX:   33443, ExitY: 32800, ExitMapID: 4, ExitHead: 5,
	},
}

// mapToGroupIdx 地圖 ID → mapTimerGroups 索引（快速查找用）。
var mapToGroupIdx map[int16]int

func init() {
	mapToGroupIdx = make(map[int16]int)
	for i, g := range mapTimerGroups {
		for _, mid := range g.MapIDs {
			mapToGroupIdx[mid] = i
		}
	}
}

// GetMapTimerGroup 返回指定地圖所屬的限時地圖組，不存在則返回 nil。
func GetMapTimerGroup(mapID int16) *MapTimerGroup {
	idx, ok := mapToGroupIdx[mapID]
	if !ok {
		return nil
	}
	return &mapTimerGroups[idx]
}

// --- 傳送門進入限時地圖時的處理 ---

// OnEnterTimedMap 玩家進入限時地圖時呼叫。
// 計算剩餘時間，發送 S_MapTimer 封包，啟動計時。
// Java: Teleportation.teleportation() 中的 isTimingMap 檢查。
func OnEnterTimedMap(sess *net.Session, player *world.PlayerInfo, mapID int16) {
	grp := GetMapTimerGroup(mapID)
	if grp == nil {
		// 離開限時地圖 → 停止計時
		player.MapTimerGroupIdx = -1
		return
	}

	if player.MapTimeUsed == nil {
		player.MapTimeUsed = make(map[int]int)
	}
	usedSec := player.MapTimeUsed[grp.OrderID]
	remaining := grp.MaxTimeSec - usedSec
	if remaining <= 0 {
		remaining = 0
	}

	player.MapTimerGroupIdx = grp.OrderID
	player.MapTimerRemaining = remaining

	// 發送 S_MapTimer — 客戶端左上角顯示倒計時
	sendMapTimer(sess, remaining)
}

// sendMapTimer 發送 S_PacketBox(MAP_TIMER=153) — 左上角限時倒計時。
// Java: S_MapTimer — [C 250][C 153][H 剩餘秒數]
func sendMapTimer(sess *net.Session, remainingSeconds int) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT) // 250
	w.WriteC(153)                                           // MAP_TIMER
	w.WriteH(uint16(remainingSeconds))
	sess.Send(w.Bytes())
}

// --- Ctrl+Q 查看限時地圖剩餘時間 ---

// SendMapTimerOut 發送 S_PacketBox(DISPLAY_MAP_TIME=159) — Ctrl+Q 顯示所有限時地圖剩餘時間。
// Java: S_MapTimerOut / S_PacketBoxMapTimer — [C 250][C 159][D 組數]{[D orderID][S 名稱][D 剩餘分鐘]}...
func SendMapTimerOut(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT) // 250
	w.WriteC(159)                                           // DISPLAY_MAP_TIME
	w.WriteD(int32(len(mapTimerGroups)))

	for _, grp := range mapTimerGroups {
		var usedSec int
		if player.MapTimeUsed != nil {
			usedSec = player.MapTimeUsed[grp.OrderID]
		}
		remainMin := (grp.MaxTimeSec - usedSec) / 60
		if remainMin < 0 {
			remainMin = 0
		}
		w.WriteD(int32(grp.OrderID))
		w.WriteS(grp.Name)
		w.WriteD(int32(remainMin))
	}
	sess.Send(w.Bytes())
}

// --- 地圖定時器每秒 tick（由 MapTimerSystem 呼叫）---

// TickMapTimer 每秒呼叫一次，遞減限時地圖計時。
// 返回 true 表示時間到需強制傳送。
// Java: MapTimerThread.MapTimeCheck()。
func TickMapTimer(player *world.PlayerInfo) (expired bool) {
	if player.MapTimerGroupIdx <= 0 {
		return false // 不在限時地圖中
	}
	if player.Dead {
		return false // 死亡不計時（Java: pc.isDead() → continue）
	}

	// 遞增已使用時間
	if player.MapTimeUsed == nil {
		player.MapTimeUsed = make(map[int]int)
	}
	player.MapTimeUsed[player.MapTimerGroupIdx]++
	player.MapTimerRemaining--

	if player.MapTimerRemaining <= 0 {
		return true // 時間到
	}

	// 每分鐘發送一次更新（Java: if (leftTime % 60) == 0）
	if player.MapTimerRemaining%60 == 0 {
		sendMapTimer(player.Session, player.MapTimerRemaining)
	}
	return false
}

// ResetAllMapTimers 日結重置所有限時地圖時間。
// Java: ServerResetMapTimer.ResetTimingMap()。
func ResetAllMapTimers(player *world.PlayerInfo) {
	for k := range player.MapTimeUsed {
		delete(player.MapTimeUsed, k)
	}
	player.MapTimerRemaining = 0
	player.MapTimerGroupIdx = -1
}
