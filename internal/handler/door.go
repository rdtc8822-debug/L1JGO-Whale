package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// HandleOpen processes C_OPEN (opcode 41) — player clicks a door to open/close.
// Packet: [H padding][H padding][D objectID]
func HandleOpen(sess *net.Session, r *packet.Reader, deps *Deps) {
	_ = r.ReadH() // skip
	_ = r.ReadH() // skip
	objectID := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	door := deps.World.GetDoor(objectID)
	if door == nil {
		return
	}

	// Dead doors can't be toggled
	if door.Dead {
		return
	}

	// Keeper check: if door has a keeper, only the clan owning that house can toggle.
	// For now, keeper doors with non-zero KeeperID are restricted.
	// TODO: Implement full clan house keeper lookup when house system is added.
	if door.KeeperID != 0 {
		// Block interaction — requires clan house ownership check
		return
	}

	// Toggle door state
	if door.OpenStatus == world.DoorActionOpen {
		door.Close()
		broadcastDoorClose(door, deps)
	} else if door.OpenStatus == world.DoorActionClose {
		door.Open()
		broadcastDoorOpen(door, deps)
	}
}

// broadcastDoorOpen sends open state to all nearby players and updates tile passability.
func broadcastDoorOpen(door *world.DoorInfo, deps *Deps) {
	nearby := deps.World.GetNearbyPlayersAt(door.X, door.Y, door.MapID)
	for _, viewer := range nearby {
		sendDoorPack(viewer.Session, door)
		sendDoorAction(viewer.Session, door.ID, world.DoorActionOpen)
	}
	// Update passability: open = passable
	sendDoorTilesAll(door, deps)
}

// broadcastDoorClose sends close state to all nearby players and updates tile passability.
func broadcastDoorClose(door *world.DoorInfo, deps *Deps) {
	nearby := deps.World.GetNearbyPlayersAt(door.X, door.Y, door.MapID)
	for _, viewer := range nearby {
		sendDoorPack(viewer.Session, door)
		sendDoorAction(viewer.Session, door.ID, world.DoorActionClose)
	}
	// Update passability: close = blocked
	sendDoorTilesAll(door, deps)
}

// sendDoorPack sends S_DoorPack (opcode 87 = S_PUT_OBJECT) — door appearance.
// Same opcode as S_CharPack but with door-specific status byte.
func sendDoorPack(viewer *net.Session, door *world.DoorInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(door.X))
	w.WriteH(uint16(door.Y))
	w.WriteD(door.ID)
	w.WriteH(uint16(door.GfxID))
	w.WriteC(door.PackStatus()) // door state: 28=open, 29=close, 32-37=damage
	w.WriteC(0)                 // heading
	w.WriteC(0)                 // light
	w.WriteC(0)                 // speed
	w.WriteD(1)                 // always 1 (Java S_DoorPack)
	w.WriteH(0)                 // lawful
	w.WriteS("")                // name (null)
	w.WriteS("")                // title (null)
	w.WriteC(0x00)              // status flags (not a PC)
	w.WriteD(0)                 // reserved
	w.WriteS("")                // clan (null)
	w.WriteS("")                // master (null)
	w.WriteC(0x00)              // hidden
	w.WriteC(0xFF)              // HP% (full)
	w.WriteC(0x00)              // reserved
	w.WriteC(0x00)              // level (0 for door)
	w.WriteC(0xFF)              // reserved
	w.WriteC(0xFF)              // reserved
	w.WriteC(0x00)              // reserved
	viewer.Send(w.Bytes())
}

// sendDoorAttr sends S_Door (opcode 209 = S_CHANGE_ATTR) — tile passability.
// Format: [H x][H y][C direction][C passable]
// Java S_Door.java: PASS = 0, NOT_PASS = 1
// Client interprets: 0 = can walk through, 1 = blocked
func sendDoorAttr(viewer *net.Session, x, y int32, direction int, passable bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_ATTR)
	w.WriteH(uint16(x))
	w.WriteH(uint16(y))
	w.WriteC(byte(direction))
	if passable {
		w.WriteC(0x00) // PASS（Java: L1DoorInstance.PASS = 0x00）
	} else {
		w.WriteC(0x41) // NOT_PASS（Java: L1DoorInstance.NOT_PASS = 0x41）
	}
	viewer.Send(w.Bytes())
}

// sendDoorAction sends S_DoActionGFX (opcode 158) — door open/close/damage animation.
func sendDoorAction(viewer *net.Session, doorID int32, actionCode byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ACTION)
	w.WriteD(doorID)
	w.WriteC(actionCode)
	viewer.Send(w.Bytes())
}

// SendDoorPerceive sends door appearance + passability tiles to a single player.
// Called when a player first sees a door (enter world, teleport, movement AOI).
func SendDoorPerceive(sess *net.Session, door *world.DoorInfo) {
	sendDoorPack(sess, door)
	// Only send tile blockers if door is closed (Java optimization)
	if door.OpenStatus == world.DoorActionClose && !door.Dead {
		sendDoorTilesToPlayer(sess, door)
	}
}

// sendDoorTilesToPlayer sends S_Door passability packets for all tiles of a door to one player.
func sendDoorTilesToPlayer(sess *net.Session, door *world.DoorInfo) {
	passable := door.IsPassable()
	entranceX := door.EntranceX()
	entranceY := door.EntranceY()
	left := door.LeftEdge
	right := door.RightEdge

	if left == right {
		// Single-tile door
		sendDoorAttr(sess, entranceX, entranceY, door.Direction, passable)
		return
	}

	// Multi-tile door
	if door.Direction == 0 {
		// "/" direction: iterate X, fixed Y
		for x := left; x <= right; x++ {
			sendDoorAttr(sess, x, entranceY, door.Direction, passable)
		}
	} else {
		// "\" direction: iterate Y, fixed X
		for y := left; y <= right; y++ {
			sendDoorAttr(sess, entranceX, y, door.Direction, passable)
		}
	}
}

// sendDoorTilesAll broadcasts S_Door passability to all nearby players.
func sendDoorTilesAll(door *world.DoorInfo, deps *Deps) {
	nearby := deps.World.GetNearbyPlayersAt(door.X, door.Y, door.MapID)
	for _, viewer := range nearby {
		sendDoorTilesToPlayer(viewer.Session, door)
	}
}

// ==================== 實體動態格子封鎖 ====================
// 第一層防線：告知客戶端 NPC/玩家/召喚獸所站的格子不可通行。
// 客戶端收到後會在本地標記該格子為不可通行，根本不會發出 C_MOVE。
//
// L1J 地圖通行性原理：
//   每個格子只有 2 個 bit：north (0x02) 和 east (0x01)。
//   客戶端 isPassable 依方向檢查不同格子的 bit：
//     heading 0 (北): tile1=(x,y).north     ← 檢查「自己格」的 north
//     heading 2 (東): tile1=(x,y).east      ← 檢查「自己格」的 east
//     heading 4 (南): tile2=(dx,dy).north   ← 檢查「目標格」的 north
//     heading 6 (西): tile2=(dx,dy).east    ← 檢查「目標格」的 east
//
//   因此要完全封鎖格子 (tx,ty)，必須封鎖 4 個邊界：
//     北邊界：(tx, ty) 的 north bit    → 阻擋從北方(heading 4)進入
//     東邊界：(tx, ty) 的 east bit     → 阻擋從東方(heading 6)進入
//     南邊界：(tx, ty+1) 的 north bit  → 阻擋從南方(heading 0)進入
//     西邊界：(tx-1, ty) 的 east bit   → 阻擋從西方(heading 2)進入
//
// rejectMove（第二層）只是安全網，理論上不應該觸發。


// ChebyshevDist returns Chebyshev (chessboard) distance between two points.
func ChebyshevDist(x1, y1, x2, y2 int32) int32 {
	dx := x1 - x2
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y2
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

