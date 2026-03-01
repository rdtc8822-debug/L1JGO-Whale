package handler

import (
	"context"
	"strings"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"go.uber.org/zap"
)


// boardNpcIDs is the set of NPC template IDs that are bulletin boards.
var boardNpcIDs = map[int32]bool{
	80006: true,
	81126: true,
	81127: true,
	81128: true,
	81129: true,
	81130: true,
	81201: true, // wedding board
}

// IsBoardNpc returns true if the NPC template ID is a bulletin board.
func IsBoardNpc(npcID int32) bool {
	return boardNpcIDs[npcID]
}

// HandleBoardOrPlate handles opcode 10 = C_Board（佈告欄開啟）。
// Java: C_Board 格式為 [D npcObjID]。
// 注意：加點（stat allocation）在 Java 中使用 C_Attr（opcode 121, mode 479），
// 已由 HandleAttr 處理，不在此 opcode。
func HandleBoardOrPlate(sess *net.Session, r *packet.Reader, deps *Deps) {
	npcObjID := r.ReadD()

	npc := deps.World.GetNpc(npcObjID)
	if npc != nil && IsBoardNpc(npc.NpcID) {
		handleBoardOpen(sess, npcObjID, deps)
	}
}

// handleBoardOpen sends the first page of board posts (S_Board).
// Triggered by C_Board (opcode 10): [D npcObjID]
func handleBoardOpen(sess *net.Session, npcObjID int32, deps *Deps) {
	if deps.BoardRepo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	posts, err := deps.BoardRepo.ListPage(ctx, 0, deps.Config.Gameplay.BoardPageSize)
	if err != nil {
		deps.Log.Error("讀取佈告欄失敗", zap.Error(err))
	}
	sendBoardList(sess, npcObjID, posts, deps.Config.Gameplay.BoardPostCost)
}

// HandleBoardBack processes C_BoardBack (opcode 23) — next page of board posts.
// Format: [D npcObjID][D lastTopicID]
func HandleBoardBack(sess *net.Session, r *packet.Reader, deps *Deps) {
	npcObjID := r.ReadD()
	lastTopicID := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	npc := deps.World.GetNpc(npcObjID)
	if npc == nil || !IsBoardNpc(npc.NpcID) {
		return
	}

	if deps.BoardRepo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	posts, err := deps.BoardRepo.ListPage(ctx, lastTopicID, deps.Config.Gameplay.BoardPageSize)
	if err != nil {
		deps.Log.Error("讀取佈告欄下一頁失敗", zap.Error(err))
	}
	sendBoardList(sess, npcObjID, posts, deps.Config.Gameplay.BoardPostCost)
}

// HandleBoardRead processes C_BoardRead (opcode 114) — read a single post.
// Format: [D npcObjID][D topicID]
func HandleBoardRead(sess *net.Session, r *packet.Reader, deps *Deps) {
	_ = r.ReadD() // npcObjID (unused for read)
	topicID := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if deps.BoardRepo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	post, err := deps.BoardRepo.GetByID(ctx, topicID)
	if err != nil {
		deps.Log.Error("讀取佈告欄文章失敗", zap.Error(err))
		return
	}
	if post == nil {
		sendServerMessage(sess, 1243) // "信件已被刪除了。"
		return
	}

	sendBoardRead(sess, post)
}

// HandleBoardWrite processes C_BoardWrite (opcode 141) — write a new post.
// Format: [D npcObjID][S title][S content]
func HandleBoardWrite(sess *net.Session, r *packet.Reader, deps *Deps) {
	npcObjID := r.ReadD()
	title := r.ReadS()
	content := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	npc := deps.World.GetNpc(npcObjID)
	if npc == nil || !IsBoardNpc(npc.NpcID) {
		return
	}

	// Validate title/content length (Java: C_BoardWrite max 16 title, 1000 content)
	if len([]rune(title)) > 16 {
		sendServerMessageArgs(sess, 166, "標題過長")
		return
	}
	if len([]rune(content)) > 1000 {
		sendServerMessageArgs(sess, 166, "內容過長")
		return
	}

	// Charge posting fee
	if !consumeAdena(player, int32(deps.Config.Gameplay.BoardPostCost)) {
		sendServerMessage(sess, 189) // "金幣不足。"
		return
	}
	sendAdenaUpdate(sess, player)

	if deps.BoardRepo == nil {
		return
	}

	// Format date
	date := time.Now().Format("2006/01/02")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := deps.BoardRepo.Write(ctx, player.Name, date, title, content)
	if err != nil {
		deps.Log.Error("寫入佈告欄失敗", zap.Error(err))
	}
}

// HandleBoardDelete processes C_BoardDelete (opcode 153) — delete a post.
// Format: [D npcObjID][D topicID]
// Note: Java has NO author check (anyone can delete). We add one for safety.
// The 3.80C client optimistically removes the post from display, so we must
// re-send the board list to refresh the view when rejecting non-author deletes.
func HandleBoardDelete(sess *net.Session, r *packet.Reader, deps *Deps) {
	npcObjID := r.ReadD()
	topicID := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if deps.BoardRepo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	post, err := deps.BoardRepo.GetByID(ctx, topicID)
	if err != nil {
		deps.Log.Error("讀取佈告欄文章失敗", zap.Error(err))
		return
	}
	if post == nil {
		sendServerMessage(sess, 1243) // "信件已被刪除了。"
		return
	}

	// Only author can delete (case-insensitive)
	if !strings.EqualFold(post.Name, player.Name) {
		deps.Log.Debug("board delete rejected: not author",
			zap.String("author", post.Name),
			zap.String("player", player.Name),
		)
		// Client optimistically removes the post — re-send the list to refresh
		sendServerMessageArgs(sess, 166, "只有作者才能刪除文章。")
		posts, err := deps.BoardRepo.ListPage(ctx, 0, deps.Config.Gameplay.BoardPageSize)
		if err == nil {
			sendBoardList(sess, npcObjID, posts, deps.Config.Gameplay.BoardPostCost)
		}
		return
	}

	if err := deps.BoardRepo.Delete(ctx, topicID); err != nil {
		deps.Log.Error("刪除佈告欄文章失敗", zap.Error(err))
	}
}

// --- Packet builders ---

// sendBoardList sends S_Board (opcode 68) — bulletin board post list.
func sendBoardList(sess *net.Session, npcObjID int32, posts []persist.BoardPost, postCost int) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_BOARD)
	w.WriteC(0x00)
	w.WriteD(npcObjID)
	w.WriteC(0xff)
	w.WriteC(0xff)
	w.WriteC(0xff)
	w.WriteC(0x7f)
	w.WriteH(uint16(len(posts)))
	w.WriteH(uint16(postCost)) // adena cost display
	for _, p := range posts {
		w.WriteD(p.ID)
		w.WriteS(p.Name)
		w.WriteS(p.Date)
		w.WriteS(p.Title)
	}
	sess.Send(w.Bytes())
}

// sendBoardRead sends S_BoardRead (opcode 148) — single post content.
func sendBoardRead(sess *net.Session, post *persist.BoardPost) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_BOARDREAD)
	w.WriteD(post.ID)
	w.WriteS(post.Name)
	w.WriteS(post.Date)
	w.WriteS(post.Title)
	w.WriteS(post.Content)
	sess.Send(w.Bytes())
}
