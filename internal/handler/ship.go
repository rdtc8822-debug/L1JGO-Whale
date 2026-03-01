package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// ═══════════════════════════════════════════════════════════════
// 船運系統 — 航線排程 + 碼頭驗證 + 下船處理
// Java: C_Ship.java（下船）, DungeonTable.java（碼頭時間/船票判定）
// ═══════════════════════════════════════════════════════════════

// --- 航線排程 ---

// shipScheduleGroup 定義航線排程群組（往程 / 返程）
type shipScheduleGroup int

const (
	shipGroupA shipScheduleGroup = iota // 往程航線
	shipGroupB                          // 返程航線
)

// 往程航線時間窗口（遊戲時間秒 % 86400）
// Java: DungeonTable.dg() — 第一個 if 區塊
var shipGroupAWindows = [][2]int{
	{15 * 360, 25 * 360},   // 遊戲時間 1:30~2:30
	{45 * 360, 55 * 360},   // 4:30~5:30
	{75 * 360, 85 * 360},   // 7:30~8:30
	{105 * 360, 115 * 360}, // 10:30~11:30
	{135 * 360, 145 * 360}, // 13:30~14:30
	{165 * 360, 175 * 360}, // 16:30~17:30
	{195 * 360, 205 * 360}, // 19:30~20:30
	{225 * 360, 235 * 360}, // 22:30~23:30
}

// 返程航線時間窗口
// Java: DungeonTable.dg() — else if 區塊
var shipGroupBWindows = [][2]int{
	{0, 360},               // 0:00~0:06（遊戲時間）
	{30 * 360, 40 * 360},   // 3:00~4:00
	{60 * 360, 70 * 360},   // 6:00~7:00
	{90 * 360, 100 * 360},  // 9:00~10:00
	{120 * 360, 130 * 360}, // 12:00~13:00
	{150 * 360, 160 * 360}, // 15:00~16:00
	{180 * 360, 190 * 360}, // 18:00~19:00
	{210 * 360, 220 * 360}, // 21:00~22:00
}

// isShipScheduleOpen 檢查指定航線群組是否在運營時間內。
// Java: DungeonTable.dg() — (servertime % 86400) 對照時間窗口。
func isShipScheduleOpen(group shipScheduleGroup) bool {
	nowtime := world.GameTimeNow().Seconds() % 86400
	var windows [][2]int
	if group == shipGroupA {
		windows = shipGroupAWindows
	} else {
		windows = shipGroupBWindows
	}
	for _, w := range windows {
		if nowtime >= w[0] && nowtime < w[1] {
			return true
		}
	}
	return false
}

// --- 碼頭座標表 ---

type shipDockKey struct {
	x, y  int32
	mapID int16
}

type shipDockInfo struct {
	group    shipScheduleGroup
	ticketID int32
}

// shipDocks 碼頭座標 → 航線排程群組 + 船票物品ID。
// Java: DungeonTable.load() 中透過座標比對硬編碼判斷 DungeonType。
var shipDocks = map[shipDockKey]shipDockInfo{
	// ── SHIP_FOR_GLUDIN（往程A，船票40299）──
	// 說話之島碼頭 → 船地圖5
	{32630, 32983, 0}: {shipGroupA, 40299},
	{32631, 32983, 0}: {shipGroupA, 40299},
	{32632, 32983, 0}: {shipGroupA, 40299},
	// 船地圖5出口 → 說話之島
	{32733, 32796, 5}: {shipGroupA, 40299},
	{32734, 32796, 5}: {shipGroupA, 40299},
	{32735, 32796, 5}: {shipGroupA, 40299},

	// ── SHIP_FOR_TI（返程B，船票40298）──
	// 古魯丁碼頭 → 船地圖6
	{32540, 32728, 4}: {shipGroupB, 40298},
	{32542, 32728, 4}: {shipGroupB, 40298},
	{32543, 32728, 4}: {shipGroupB, 40298},
	{32544, 32728, 4}: {shipGroupB, 40298},
	{32545, 32728, 4}: {shipGroupB, 40298},
	// 船地圖6出口 → 古魯丁
	{32734, 32794, 6}: {shipGroupB, 40298},
	{32735, 32794, 6}: {shipGroupB, 40298},
	{32736, 32794, 6}: {shipGroupB, 40298},
	{32737, 32794, 6}: {shipGroupB, 40298},

	// ── SHIP_FOR_FI（返程B，船票40300）──
	// 海音碼頭 → 船地圖83
	{33423, 33502, 4}: {shipGroupB, 40300},
	{33424, 33502, 4}: {shipGroupB, 40300},
	{33425, 33502, 4}: {shipGroupB, 40300},
	{33426, 33502, 4}: {shipGroupB, 40300},
	// 船地圖83出口 → 海音
	{32733, 32794, 83}: {shipGroupB, 40300},
	{32734, 32794, 83}: {shipGroupB, 40300},
	{32735, 32794, 83}: {shipGroupB, 40300},
	{32736, 32794, 83}: {shipGroupB, 40300},

	// ── SHIP_FOR_HEINE（往程A，船票40301）──
	// 被遺忘之島碼頭 → 船地圖84
	{32935, 33058, 70}: {shipGroupA, 40301},
	{32936, 33058, 70}: {shipGroupA, 40301},
	{32937, 33058, 70}: {shipGroupA, 40301},
	// 船地圖84出口 → 被遺忘之島
	{32732, 32796, 84}: {shipGroupA, 40301},
	{32733, 32796, 84}: {shipGroupA, 40301},
	{32734, 32796, 84}: {shipGroupA, 40301},
	{32735, 32796, 84}: {shipGroupA, 40301},

	// ── SHIP_FOR_PI（往程A，船票40302）──
	// 隱藏碼頭 → 船地圖447
	{32750, 32874, 445}: {shipGroupA, 40302},
	{32751, 32874, 445}: {shipGroupA, 40302},
	{32752, 32874, 445}: {shipGroupA, 40302},
	// 船地圖447出口 → 隱藏碼頭
	{32731, 32796, 447}: {shipGroupA, 40302},
	{32732, 32796, 447}: {shipGroupA, 40302},
	{32733, 32796, 447}: {shipGroupA, 40302},

	// ── SHIP_FOR_HIDDENDOCK（返程B，船票40303）──
	// 海賊島碼頭 → 船地圖446
	{32296, 33087, 440}: {shipGroupB, 40303},
	{32297, 33087, 440}: {shipGroupB, 40303},
	{32298, 33087, 440}: {shipGroupB, 40303},
	// 船地圖446出口 → 海賊島
	{32735, 32794, 446}: {shipGroupB, 40303},
	{32736, 32794, 446}: {shipGroupB, 40303},
	{32737, 32794, 446}: {shipGroupB, 40303},
}

// CheckShipDock 檢查指定座標是否為船舶碼頭，若是則驗證航線時間和船票。
// 返回 (是否為碼頭, 是否通過驗證)。
// Java: DungeonTable.dg() 中的 DungeonType 判定 + 時間/物品檢查。
func CheckShipDock(x, y int32, mapID int16, player *world.PlayerInfo) (isDock, allowed bool) {
	info, ok := shipDocks[shipDockKey{x, y, mapID}]
	if !ok {
		return false, false
	}
	// 檢查航線排程時間
	if !isShipScheduleOpen(info.group) {
		return true, false
	}
	// 檢查船票（只檢查不消耗，Java: checkItem）
	if player.Inv.FindByItemID(info.ticketID) == nil {
		return true, false
	}
	return true, true
}

// --- C_Ship 封包處理（下船） ---

// shipTicketByCurrentMap 玩家當前所在船地圖 → 需消耗的船票物品ID。
// Java: C_Ship.java 用 pc.getMapId()（當前地圖）判斷船票，非目的地。
var shipTicketByCurrentMap = map[int16]int32{
	5:   40299, // 說話之島航線船
	6:   40298, // 古魯丁航線船
	83:  40300, // 被遺忘之島航線船
	84:  40301, // 海音航線船
	446: 40303, // 海賊島航線船
	447: 40302, // 隱藏碼頭航線船
}

// HandleEnterShip 處理 C_SHIP（opcode 231）— 從船上下船。
// Java: C_Ship.java — 依據玩家當前地圖（船地圖）消耗船票，讀取目的地座標後傳送。
// 封包格式：[H destMapId][H destX][H destY]
func HandleEnterShip(sess *net.Session, r *packet.Reader, deps *Deps) {
	destMapID := int16(r.ReadH())
	destX := int32(r.ReadH())
	destY := int32(r.ReadH())

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Java: 以玩家當前地圖（船地圖）查找需消耗的船票
	ticketItemID, ok := shipTicketByCurrentMap[player.MapID]
	if !ok {
		deps.Log.Warn(fmt.Sprintf("船票  角色=%s  未知船地圖=%d", player.Name, player.MapID))
		return
	}

	// 尋找並消耗船票
	ticket := player.Inv.FindByItemID(ticketItemID)
	if ticket == nil {
		// 沒有船票 → 靜默失敗（匹配 Java 行為）
		return
	}

	fullyRemoved := player.Inv.RemoveItem(ticket.ObjectID, 1)
	if fullyRemoved {
		sendRemoveInventoryItem(sess, ticket.ObjectID)
	} else {
		sendItemCountUpdate(sess, ticket)
	}
	sendWeightUpdate(sess, player)

	// 取消交易（Java: L1Trade.tradeCancel）
	cancelTradeIfActive(player, deps)

	// 傳送到目的地
	teleportPlayer(sess, player, destX, destY, destMapID, 5, deps)

	deps.Log.Info(fmt.Sprintf("下船  角色=%s  目的地=%d  x=%d  y=%d",
		player.Name, destMapID, destX, destY))
}
