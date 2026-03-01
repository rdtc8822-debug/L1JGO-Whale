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
// Java: C_NewCharSelect — 先發送 UI 清理封包，再登出，最後發送 LOGOUT 封包。
func HandleChangeChar(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	// Java C_NewCharSelect: 先發送 UI 清理封包再登出
	if player != nil {
		sendChangeName(sess, player.CharID, player.Name)
		sendUpdateER(sess, 0)
		sendDodgeIcon(sess, byte(player.Dodge))
	}

	// Java: quitGame() 先清理 + 儲存，最後才發送 LOGOUT
	// Clear player tile before removal (for NPC pathfinding)
	if player != nil && deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, player.X, player.Y, false)
	}

	// Remove from world if in-world
	player = deps.World.RemovePlayer(sess.ID)
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
		// 儲存時必須扣除裝備加成和 buff 加成，只保存基礎值。
		// 否則重新登入時 InitEquipStats / loadAndRestoreBuffs 會重複疊加，造成屬性膨脹。
		eq := player.EquipBonuses
		var bStr, bDex, bCon, bWis, bIntel, bCha, bMaxHP, bMaxMP int16
		for _, b := range player.ActiveBuffs {
			bStr += b.DeltaStr
			bDex += b.DeltaDex
			bCon += b.DeltaCon
			bWis += b.DeltaWis
			bIntel += b.DeltaIntel
			bCha += b.DeltaCha
			bMaxHP += b.DeltaMaxHP
			bMaxMP += b.DeltaMaxMP
		}
		row := &persist.CharacterRow{
			Name:        player.Name,
			Level:       player.Level,
			Exp:         int64(player.Exp),
			HP:          player.HP,
			MP:          player.MP,
			MaxHP:       player.MaxHP - int16(eq.AddHP) - bMaxHP,
			MaxMP:       player.MaxMP - int16(eq.AddMP) - bMaxMP,
			X:           player.X,
			Y:           player.Y,
			MapID:       player.MapID,
			Heading:     player.Heading,
			Lawful:      player.Lawful,
			Str:         player.Str - int16(eq.AddStr) - bStr,
			Dex:         player.Dex - int16(eq.AddDex) - bDex,
			Con:         player.Con - int16(eq.AddCon) - bCon,
			Wis:         player.Wis - int16(eq.AddWis) - bWis,
			Cha:         player.Cha - int16(eq.AddCha) - bCha,
			Intel:       player.Intel - int16(eq.AddInt) - bIntel,
			BonusStats:  player.BonusStats,
			ElixirStats: player.ElixirStats,
			ClanID:      player.ClanID,
			ClanName:    player.ClanName,
			ClanRank:    player.ClanRank,
			Title:       player.Title,
			Karma:       player.Karma,
			PKCount:     player.PKCount,
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

		// 存檔限時地圖已使用時間（JSONB）
		if len(player.MapTimeUsed) > 0 {
			ctx4b, cancel4b := context.WithTimeout(context.Background(), 3*time.Second)
			if err := deps.CharRepo.SaveMapTimes(ctx4b, player.Name, player.MapTimeUsed); err != nil {
				deps.Log.Error("切換角色時存檔限時地圖時間失敗",
					zap.String("name", player.Name), zap.Error(err))
			}
			cancel4b()
		}

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

	// Java: quitGame() 完成後才發送 LOGOUT 封包（S_PacketBoxSelect）
	sendPacketBoxLogout(sess)

	sess.CharName = ""
	sess.SetState(packet.StateAuthenticated)
	// 注意：不主動推送角色列表。
	// Java 在此等待客戶端收到 LOGOUT 後自動發送 C_CommonClick (opcode 16)，
	// 再由 HandleCommonClick 回應角色列表。
}

// sendPacketBoxLogout sends S_PacketBoxSelect (opcode 250) subcode 42 — 告知客戶端返回選角畫面。
// Java: S_PacketBoxSelect — writeC(opcode) + writeC(42) + 6×writeC(0)。
func sendPacketBoxLogout(sess *net.Session) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(42) // subcode: LOGOUT
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

// sendChangeName sends S_ChangeName (opcode 46) — 重設角色名稱顯示。
// Java C_NewCharSelect: S_ChangeName(pc, false) — 清理 UI 狀態。
func sendChangeName(sess *net.Session, charID int32, name string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGENAME)
	w.WriteD(charID)
	w.WriteS(name)
	sess.Send(w.Bytes())
}

// sendUpdateER sends S_PacketBox(UPDATE_ER) (opcode 250, subcode 132) — 更新迴避率。
// Java C_NewCharSelect: S_PacketBox(UPDATE_ER, pc.getEr())。
func sendUpdateER(sess *net.Session, er int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(132) // UPDATE_ER
	w.WriteH(uint16(er))
	sess.Send(w.Bytes())
}

// sendDodgeIcon sends S_PacketBoxIcon1(true, dodge) (opcode 250, subcode 0x58) — 閃避率圖示。
// Java C_NewCharSelect: S_PacketBoxIcon1(true, pc.get_dodge())。
func sendDodgeIcon(sess *net.Session, dodge byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x58) // _dodge_up
	w.WriteC(dodge)
	sess.Send(w.Bytes())
}
