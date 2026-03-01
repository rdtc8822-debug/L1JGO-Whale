package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleWindows 處理 C_WINDOWS（opcode 254）。
// Java: C_Windows.java — 多用途封包，依 type 欄位分派不同功能。
// 封包格式：[C type][依 type 而定的後續欄位]
func HandleWindows(sess *net.Session, r *packet.Reader, deps *Deps) {
	windowType := r.ReadC()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	switch windowType {
	case 9:
		// Ctrl+Q 查詢限時地圖剩餘時間
		// Java: pc.sendPackets(new S_MapTimerOut(pc))
		SendMapTimerOut(sess, player)

	case 0x22:
		// 書籤排序（記憶場所順序配置）
		// Java: readBytes() → CharBookConfigReading.storeCharBookConfig
		// TODO: 實作書籤排序持久化

	case 0x27:
		// 變更書籤名稱
		// Java: readD(changeCount) → loop { readD(bookId), readS(newName) }
		// TODO: 實作書籤名稱修改

	case 6:
		// 龍門（副本傳送門）
		// Java: readD(itemObjID) + readD(selectDoor) → 龍門副本傳送
		// TODO: 實作龍門系統

	default:
		deps.Log.Debug("C_Windows 未處理的 type",
			zap.Uint8("type", windowType),
			zap.String("角色", player.Name),
		)
	}
}
