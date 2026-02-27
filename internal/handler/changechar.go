package handler

import (
	"context"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"go.uber.org/zap"
)

// HandleChangeChar processes C_REQUEST_ROLL (opcode 7).
// Returns the player to the character select screen and resends the character list.
func HandleChangeChar(sess *net.Session, _ *packet.Reader, deps *Deps) {
	// Send S_EVENT (S_PacketBox) subcode 42 = LOGOUT first.
	// This tells the client to transition to the character select UI.
	sendPacketBoxLogout(sess)

	// Clear player tile before removal (for NPC pathfinding)
	if pre := deps.World.GetBySession(sess.ID); pre != nil && deps.MapData != nil {
		deps.MapData.SetImpassable(pre.MapID, pre.X, pre.Y, false)
	}

	// Remove from world if in-world
	player := deps.World.RemovePlayer(sess.ID)
	if player != nil {
		// 清理進行中的交易
		cancelTradeIfActive(player, deps)

		// Clean up party membership and notify remaining members
		if player.PartyID != 0 {
			partyLeaveMember(player, deps)
		}

		// Broadcast removal to nearby players
		nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		for _, other := range nearby {
			SendRemoveObject(other.Session, player.CharID)
		}

		// Save full character state
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		row := &persist.CharacterRow{
			Name:       player.Name,
			Level:      player.Level,
			Exp:        int64(player.Exp),
			HP:         player.HP,
			MP:         player.MP,
			MaxHP:      player.MaxHP,
			MaxMP:      player.MaxMP,
			X:          player.X,
			Y:          player.Y,
			MapID:      player.MapID,
			Heading:    player.Heading,
			Lawful:     player.Lawful,
			Str:        player.Str,
			Dex:        player.Dex,
			Con:        player.Con,
			Wis:        player.Wis,
			Cha:        player.Cha,
			Intel:      player.Intel,
			BonusStats: player.BonusStats,
			ClanID:     player.ClanID,
			ClanName:   player.ClanName,
			ClanRank:   player.ClanRank,
			Title:      player.Title,
		}
		if err := deps.CharRepo.SaveCharacter(ctx, row); err != nil {
			deps.Log.Error("切換角色時存檔角色失敗",
				zap.String("name", player.Name), zap.Error(err))
		}
		cancel()

		// Save inventory
		if deps.ItemRepo != nil {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			if err := deps.ItemRepo.SaveInventory(ctx2, player.CharID, player.Inv, &player.Equip); err != nil {
				deps.Log.Error("切換角色時存檔背包失敗",
					zap.String("name", player.Name), zap.Error(err))
			}
			cancel2()
		}

		// Save bookmarks (JSONB)
		ctx3, cancel3 := context.WithTimeout(context.Background(), 3*time.Second)
		bmRows := make([]persist.BookmarkRow, len(player.Bookmarks))
		for i, bm := range player.Bookmarks {
			bmRows[i] = persist.BookmarkRow{ID: bm.ID, Name: bm.Name, X: bm.X, Y: bm.Y, MapID: bm.MapID}
		}
		if err := deps.CharRepo.SaveBookmarks(ctx3, player.Name, bmRows); err != nil {
			deps.Log.Error("切換角色時存檔書籤失敗",
				zap.String("name", player.Name), zap.Error(err))
		}
		cancel3()

		// Save known spells (JSONB)
		ctx4, cancel4 := context.WithTimeout(context.Background(), 3*time.Second)
		if err := deps.CharRepo.SaveKnownSpells(ctx4, player.Name, player.KnownSpells); err != nil {
			deps.Log.Error("切換角色時存檔魔法書失敗",
				zap.String("name", player.Name), zap.Error(err))
		}
		cancel4()

		// Save active buffs (including polymorph state)
		if deps.BuffRepo != nil && len(player.ActiveBuffs) > 0 {
			buffRows := BuffRowsFromPlayer(player)
			if len(buffRows) > 0 {
				ctx5, cancel5 := context.WithTimeout(context.Background(), 3*time.Second)
				if err := deps.BuffRepo.SaveBuffs(ctx5, player.CharID, buffRows); err != nil {
					deps.Log.Error("切換角色時存檔buff失敗",
						zap.String("name", player.Name), zap.Error(err))
				}
				cancel5()
			}
		}
	}

	sess.CharName = ""
	sess.SetState(packet.StateAuthenticated)
	sendCharacterList(sess, deps)
}

// sendPacketBoxLogout sends S_EVENT (opcode 250) subcode 42 — tells client to return to char select.
func sendPacketBoxLogout(sess *net.Session) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteH(42) // subcode: LOGOUT
	sess.Send(w.Bytes())
}
