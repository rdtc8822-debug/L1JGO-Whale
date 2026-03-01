package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

const maxBookmarks = 50

// HandleBookmark processes C_BOOKMARK (opcode 165) — player adds a bookmark at current location.
// Format: [S name]
func HandleBookmark(sess *net.Session, r *packet.Reader, deps *Deps) {
	name := r.ReadS()
	if name == "" {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if len(player.Bookmarks) >= maxBookmarks {
		sendServerMessage(sess, 82) // "Bookmark limit reached"
		return
	}

	// Check for duplicate name
	for _, bm := range player.Bookmarks {
		if bm.Name == name {
			return
		}
	}

	// Generate a unique ID based on max existing ID + 1
	var bmID int32
	for _, bm := range player.Bookmarks {
		if bm.ID > bmID {
			bmID = bm.ID
		}
	}
	bmID++

	bm := world.Bookmark{
		ID:    bmID,
		Name:  name,
		X:     player.X,
		Y:     player.Y,
		MapID: player.MapID,
	}
	player.Bookmarks = append(player.Bookmarks, bm)

	// Send confirmation to client
	sendAddBookmark(sess, &bm)

	deps.Log.Info(fmt.Sprintf("書籤已新增  角色=%s  書籤=%s  x=%d  y=%d  地圖=%d", player.Name, name, bm.X, bm.Y, bm.MapID))
}

// HandleDeleteBookmark processes C_DELETE_BOOKMARK (opcode 3) — player deletes a bookmark.
// Format: [S name]
func HandleDeleteBookmark(sess *net.Session, r *packet.Reader, deps *Deps) {
	name := r.ReadS()
	if name == "" {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Find and remove bookmark by name
	for i, bm := range player.Bookmarks {
		if bm.Name == name {
			player.Bookmarks = append(player.Bookmarks[:i], player.Bookmarks[i+1:]...)
			deps.Log.Debug("bookmark deleted",
				zap.String("player", player.Name),
				zap.String("bookmark", name),
			)
			return
		}
	}
}

// SendAllBookmarks sends all of a player's bookmarks to the client on login.
// Uses the bulk-load format (opcode 64 / S_OPCODE_CHARRESET) so the client
// silently populates the bookmark list without showing the "added" popup.
func SendAllBookmarks(sess *net.Session, bookmarks []world.Bookmark) {
	count := len(bookmarks)
	if count > 127 {
		count = 127
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARSYNACK) // S_OPCODE_CHARRESET = 64
	w.WriteC(0x2a)
	w.WriteC(0x80)
	w.WriteC(0x00)
	w.WriteC(0x02)

	// 127-byte slot occupancy array: slot index if occupied, 0x00 if empty
	for i := 0; i <= 126; i++ {
		if i < count {
			w.WriteC(byte(i))
		} else {
			w.WriteC(0x00)
		}
	}

	w.WriteC(0x3c)
	w.WriteC(0)
	w.WriteC(byte(count))
	w.WriteC(0)

	// Each bookmark: [H x][H y][S name][H mapID][D id]
	for i := 0; i < count; i++ {
		bm := &bookmarks[i]
		w.WriteH(uint16(bm.X))
		w.WriteH(uint16(bm.Y))
		w.WriteS(bm.Name)
		w.WriteH(uint16(bm.MapID))
		w.WriteD(bm.ID)
	}

	sess.Send(w.Bytes())
}

// sendAddBookmark sends S_ADD_BOOKMARK (opcode 92) for a single bookmark.
// Format: [S name][H mapID][D bookmarkID][H x][H y]
func sendAddBookmark(sess *net.Session, bm *world.Bookmark) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ADD_BOOKMARK)
	w.WriteS(bm.Name)
	w.WriteH(uint16(bm.MapID))
	w.WriteD(bm.ID)
	w.WriteH(uint16(bm.X))
	w.WriteH(uint16(bm.Y))
	sess.Send(w.Bytes())
}
