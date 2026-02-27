package system

import (
	"context"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// InputSystem drains packet queues from all sessions and dispatches them
// through the packet registry. Phase 0 (Input).
type InputSystem struct {
	netServer   *net.Server
	registry    *packet.Registry
	store       *net.SessionStore
	maxPerTick  int
	log         *zap.Logger
	accountRepo *persist.AccountRepo
	charRepo    *persist.CharacterRepo
	itemRepo    *persist.ItemRepo
	buffRepo    *persist.BuffRepo
	worldState  *world.State
	mapData     *data.MapDataTable
	petRepo     *persist.PetRepo
}

func NewInputSystem(
	netServer *net.Server,
	registry *packet.Registry,
	store *net.SessionStore,
	maxPerTick int,
	accountRepo *persist.AccountRepo,
	charRepo *persist.CharacterRepo,
	itemRepo *persist.ItemRepo,
	buffRepo *persist.BuffRepo,
	worldState *world.State,
	mapData *data.MapDataTable,
	petRepo *persist.PetRepo,
	log *zap.Logger,
) *InputSystem {
	return &InputSystem{
		netServer:   netServer,
		registry:    registry,
		store:       store,
		maxPerTick:  maxPerTick,
		log:         log,
		accountRepo: accountRepo,
		charRepo:    charRepo,
		itemRepo:    itemRepo,
		buffRepo:    buffRepo,
		worldState:  worldState,
		mapData:     mapData,
		petRepo:     petRepo,
	}
}

func (s *InputSystem) Phase() coresys.Phase { return coresys.PhaseInput }

func (s *InputSystem) Update(_ time.Duration) {
	// Accept new sessions
	for {
		select {
		case sess := <-s.netServer.NewSessions():
			s.store.Add(sess)
		default:
			goto doneNew
		}
	}
doneNew:

	// Process dead sessions
	for {
		select {
		case id := <-s.netServer.DeadSessions():
			s.store.Remove(id)
		default:
			goto doneDead
		}
	}
doneDead:

	// Drain packets from each session (up to maxPerTick per session)
	for id, sess := range s.store.Raw() {
		if sess.IsClosed() {
			// Drain any remaining packets BEFORE cleanup (e.g. C_SAVEIO sent just before disconnect).
			// Use the last known state so handlers like HandleCharConfig can still find the player.
			for i := 0; i < s.maxPerTick; i++ {
				select {
				case data := <-sess.InQueue:
					if err := s.registry.Dispatch(sess, sess.State(), data); err != nil {
						s.log.Debug("封包分派錯誤 (斷線中)",
							zap.Uint64("session", sess.ID),
							zap.Error(err),
						)
					}
				default:
					goto doneClosing
				}
			}
		doneClosing:
			// Flush any remaining buffered output before disconnect cleanup
			sess.FlushOutput()
			s.handleDisconnect(sess)
			s.netServer.NotifyDead(id)
			s.store.Remove(id)
			continue
		}

		processed := false
		for i := 0; i < s.maxPerTick; i++ {
			select {
			case data := <-sess.InQueue:
				processed = true
				if err := s.registry.Dispatch(sess, sess.State(), data); err != nil {
					s.log.Debug("封包分派錯誤",
						zap.Uint64("session", sess.ID),
						zap.Error(err),
					)
				}
			default:
				goto nextSession
			}
		}
	nextSession:
		// Mark player dirty if any in-world packets were processed this tick.
		// PersistenceSystem will only save dirty players.
		if processed && sess.State() == packet.StateInWorld {
			if p := s.worldState.GetBySession(sess.ID); p != nil {
				p.Dirty = true
			}
		}
	}

	// 提前 flush：讓 Phase 0 產生的封包（移動廣播、AOI 更新）
	// 立即進入 OutQueue，writeLoop 可在 Phase 1-3 運行時就開始發送。
	// Phase 4 的 OutputSystem 會再 flush Phase 1-3 產生的剩餘封包。
	s.store.ForEach(func(sess *net.Session) {
		sess.FlushOutput()
	})
}

// handleDisconnect cleans up when a session closes:
// removes from world state, broadcasts S_REMOVE_OBJECT, saves position, marks offline.
func (s *InputSystem) handleDisconnect(sess *net.Session) {
	// Clear player tile before removal (for NPC pathfinding)
	if pre := s.worldState.GetBySession(sess.ID); pre != nil && s.mapData != nil {
		s.mapData.SetImpassable(pre.MapID, pre.X, pre.Y, false)
	}

	// Remove from world state and broadcast removal
	player := s.worldState.RemovePlayer(sess.ID)
	if player != nil {
		// Clean up trade if in progress — restore partner's items (items are deducted on add-to-trade)
		if player.TradePartnerID != 0 {
			partner := s.worldState.GetByCharID(player.TradePartnerID)
			if partner != nil {
				// Restore partner's deducted trade items back to their inventory
				restoreTradeItemsOnDisconnect(partner)
				if partner.TradeWindowOpen {
					sendTradeStatusPacket(partner.Session, 1) // 1 = cancelled
				}
				partner.TradePartnerID = 0
				partner.TradeWindowOpen = false
				partner.TradeOk = false
				partner.TradeItems = nil
				partner.TradeGold = 0
			}
			// Disconnecting player's items are lost (they disconnected mid-trade)
			// Items were already deducted but player is gone — restore to inventory for DB save
			restoreTradeItemsOnDisconnect(player)
			player.TradePartnerID = 0
			player.TradeWindowOpen = false
			player.TradeItems = nil
			player.TradeGold = 0
		}

		// Clean up party membership — matching Java breakup logic:
		// Leader leaves or only 2 members → dissolve entire party.
		if player.PartyID != 0 {
			party := s.worldState.Parties.GetParty(player.CharID)
			if party != nil {
				isLeader := party.LeaderID == player.CharID
				memberCount := len(party.Members)

				if isLeader || memberCount == 2 {
					// Breakup: dissolve entire party
					members := make([]*world.PlayerInfo, 0, len(party.Members))
					for _, id := range party.Members {
						m := s.worldState.GetByCharID(id)
						if m != nil {
							members = append(members, m)
						}
					}
					s.worldState.Parties.Dissolve(party.LeaderID)

					// Clear HP meters and notify all members
					for i, a := range members {
						for j, b := range members {
							if i != j {
								sendHpMeterPacket(a.Session, b.CharID, 0xFF)
							}
						}
						sendHpMeterPacket(a.Session, a.CharID, 0xFF)
						a.PartyID = 0
						a.PartyLeader = false
						sendServerMessagePacket(a.Session, 418) // 隊伍已解散
					}
				} else {
					// Non-leader leaves, party continues
					partyID := party.LeaderID
					// Clear HP meters between leaver and remaining
					for _, memberID := range party.Members {
						if memberID == player.CharID {
							continue
						}
						member := s.worldState.GetByCharID(memberID)
						if member != nil {
							sendHpMeterPacket(member.Session, player.CharID, 0xFF)
						}
					}

					s.worldState.Parties.RemoveMember(player.CharID)
					player.PartyID = 0
					player.PartyLeader = false

					// Notify remaining members
					remaining := s.worldState.Parties.GetParty(partyID)
					if remaining != nil {
						for _, memberID := range remaining.Members {
							member := s.worldState.GetByCharID(memberID)
							if member != nil {
								sendServerMessageArgsPacket(member.Session, 420, player.Name) // %0離開了隊伍
							}
						}
					}
				}
			} else {
				player.PartyID = 0
				player.PartyLeader = false
			}
		}

		// Clean up chat party membership
		if s.worldState.ChatParties.IsInParty(player.CharID) {
			chatParty := s.worldState.ChatParties.GetParty(player.CharID)
			if chatParty != nil {
				isLeader := chatParty.LeaderID == player.CharID
				if isLeader || len(chatParty.Members) == 2 {
					// Dissolve chat party
					members := make([]*world.PlayerInfo, 0, len(chatParty.Members))
					for _, id := range chatParty.Members {
						m := s.worldState.GetByCharID(id)
						if m != nil {
							members = append(members, m)
						}
					}
					s.worldState.ChatParties.Dissolve(chatParty.LeaderID)
					for _, m := range members {
						sendServerMessagePacket(m.Session, 418) // 隊伍已解散
					}
				} else {
					s.worldState.ChatParties.RemoveMember(player.CharID)
					remaining := s.worldState.ChatParties.GetParty(chatParty.LeaderID)
					if remaining != nil {
						for _, memberID := range remaining.Members {
							member := s.worldState.GetByCharID(memberID)
							if member != nil {
								sendServerMessageArgsPacket(member.Session, 420, player.Name)
							}
						}
					}
				}
			}
		}

		// 釋放血盟倉庫鎖定（Java: QuitGame / L1PcInstance 離線清理）
		if player.ClanID != 0 {
			if clan := s.worldState.Clans.GetClan(player.ClanID); clan != nil {
				if clan.WarehouseUsingCharID == player.CharID {
					clan.WarehouseUsingCharID = 0
				}
			}
		}

		// Clean up all companion entities (summons, dolls, followers)
		s.cleanupCompanions(player)

		// 廣播移除 + 解鎖格子給附近玩家
		nearby := s.worldState.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		removePacket := buildRemoveObjectPacket(player.CharID)
		for _, other := range nearby {
			other.Session.Send(removePacket)
		}

		// Save full character state to DB
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
		if err := s.charRepo.SaveCharacter(ctx, row); err != nil {
			s.log.Error("斷線存檔角色失敗",
				zap.String("name", player.Name),
				zap.Error(err),
			)
		}
		cancel()

		// Save inventory items to DB
		if s.itemRepo != nil {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.itemRepo.SaveInventory(ctx2, player.CharID, player.Inv, &player.Equip); err != nil {
				s.log.Error("斷線存檔背包失敗",
					zap.String("name", player.Name),
					zap.Error(err),
				)
			}
			cancel2()
		}

		// Save bookmarks to DB (JSONB)
		ctx3, cancel3 := context.WithTimeout(context.Background(), 3*time.Second)
		if err := s.charRepo.SaveBookmarks(ctx3, player.Name, bookmarksToRows(player.Bookmarks)); err != nil {
			s.log.Error("斷線存檔書籤失敗",
				zap.String("name", player.Name),
				zap.Error(err),
			)
		}
		cancel3()

		// Save active buffs to DB (including polymorph state)
		if s.buffRepo != nil && len(player.ActiveBuffs) > 0 {
			buffRows := buffRowsFromPlayer(player)
			if len(buffRows) > 0 {
				ctx4, cancel4 := context.WithTimeout(context.Background(), 3*time.Second)
				if err := s.buffRepo.SaveBuffs(ctx4, player.CharID, buffRows); err != nil {
					s.log.Error("斷線存檔buff失敗",
						zap.String("name", player.Name),
						zap.Error(err),
					)
				}
				cancel4()
			}
		}
	}

	// Mark account offline
	if sess.AccountName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		s.accountRepo.SetOnline(ctx, sess.AccountName, false)
		cancel()
	}
}

// bookmarksToRows converts world.Bookmark slice to persist.BookmarkRow slice for JSONB storage.
func bookmarksToRows(bms []world.Bookmark) []persist.BookmarkRow {
	rows := make([]persist.BookmarkRow, len(bms))
	for i, bm := range bms {
		rows[i] = persist.BookmarkRow{
			ID:    bm.ID,
			Name:  bm.Name,
			X:     bm.X,
			Y:     bm.Y,
			MapID: bm.MapID,
		}
	}
	return rows
}

// buildRemoveObjectPacket builds a reusable S_REMOVE_OBJECT byte slice.
func buildRemoveObjectPacket(charID int32) []byte {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_OBJECT)
	w.WriteD(charID)
	return w.Bytes()
}


// SessionCount returns the current number of active sessions.
func (s *InputSystem) SessionCount() int {
	return len(s.store.Raw())
}

// --- Disconnect cleanup packet helpers ---
// These duplicate minimal packet building from handler/ to avoid circular imports.

// restoreTradeItemsOnDisconnect restores deducted trade items/gold back to a player's inventory.
func restoreTradeItemsOnDisconnect(p *world.PlayerInfo) {
	for _, item := range p.TradeItems {
		existing := p.Inv.FindByItemID(item.ItemID)
		wasExisting := existing != nil && item.Stackable

		newItem := p.Inv.AddItem(item.ItemID, item.Count, item.Name, item.InvGfx, item.Weight, item.Stackable, item.Bless)
		newItem.EnchantLvl = item.EnchantLvl
		newItem.UseType = item.UseType // preserve original use_type
		if wasExisting {
			sendChangeItemUsePacket(p.Session, newItem)
		} else {
			sendAddItemPacket(p.Session, newItem)
		}
	}

	// Restore gold
	if p.TradeGold > 0 {
		adena := p.Inv.FindByItemID(world.AdenaItemID)
		if adena != nil {
			adena.Count += p.TradeGold
			sendChangeItemUsePacket(p.Session, adena)
		} else {
			newItem := p.Inv.AddItem(world.AdenaItemID, p.TradeGold, "金幣", 0, 0, true, 0)
			sendAddItemPacket(p.Session, newItem)
		}
	}
}

// sendTradeStatusPacket sends S_TRADESTATUS (opcode 112).
func sendTradeStatusPacket(sess *net.Session, status byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TRADESTATUS)
	w.WriteC(status)
	sess.Send(w.Bytes())
}

// sendChangeItemUsePacket sends S_CHANGE_ITEM_USE (opcode 24) — update stack count.
func sendChangeItemUsePacket(sess *net.Session, item *world.InvItem) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_ITEM_USE)
	w.WriteD(item.ObjectID)
	w.WriteS(item.Name)
	w.WriteD(item.Count)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

// sendAddItemPacket sends S_ADD_ITEM (opcode 15) — single item add to inventory.
// Matches handler/shop.go sendAddItem format.
func sendAddItemPacket(sess *net.Session, item *world.InvItem) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ADD_ITEM)
	w.WriteD(item.ObjectID)
	w.WriteH(world.ItemDescID(item.ItemID)) // descId — Java: switch(itemId) for material items
	w.WriteC(item.UseType)
	w.WriteC(0)                    // charge count
	w.WriteH(uint16(item.InvGfx))
	w.WriteC(world.EffectiveBless(item)) // bless: 3=unidentified
	w.WriteD(item.Count)
	w.WriteC(0)                          // itemStatusX
	w.WriteS(item.Name)
	w.WriteC(0)                          // status bytes length
	w.WriteC(0x17)
	w.WriteC(0)
	w.WriteH(0)
	w.WriteH(0)
	w.WriteC(byte(item.EnchantLvl))
	w.WriteD(item.ObjectID)              // world serial
	w.WriteD(0)
	w.WriteD(0)
	w.WriteD(7)                          // flags: 7=deletable
	sess.Send(w.Bytes())
}

// sendHpMeterPacket sends S_HPMeter (opcode 237). 0xFF = clear HP bar.
func sendHpMeterPacket(sess *net.Session, objectID int32, hpRatio int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HP_METER)
	w.WriteD(objectID)
	w.WriteH(uint16(hpRatio))
	sess.Send(w.Bytes())
}

// sendServerMessagePacket sends S_MESSAGE_CODE (opcode 71) — system message by ID.
func sendServerMessagePacket(sess *net.Session, msgID uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

// sendServerMessageArgsPacket sends S_MESSAGE_CODE (opcode 71) with string args.
func sendServerMessageArgsPacket(sess *net.Session, msgID uint16, args ...string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(byte(len(args)))
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// buffRowsFromPlayer converts active buffs to persist.BuffRow for DB save.
// Duplicated from handler.BuffRowsFromPlayer to avoid circular imports.
const skillShapeChange int32 = 67

func buffRowsFromPlayer(p *world.PlayerInfo) []persist.BuffRow {
	if len(p.ActiveBuffs) == 0 {
		return nil
	}
	rows := make([]persist.BuffRow, 0, len(p.ActiveBuffs))
	for _, buff := range p.ActiveBuffs {
		if buff.SetInvisible || buff.SetParalyzed || buff.SetSleeped {
			continue
		}
		remainSec := buff.TicksLeft / 5
		if remainSec <= 0 {
			continue
		}
		row := persist.BuffRow{
			CharID:        p.CharID,
			SkillID:       buff.SkillID,
			RemainingTime: remainSec,
			DeltaAC:       buff.DeltaAC,
			DeltaStr:      buff.DeltaStr,
			DeltaDex:      buff.DeltaDex,
			DeltaCon:      buff.DeltaCon,
			DeltaWis:      buff.DeltaWis,
			DeltaIntel:    buff.DeltaIntel,
			DeltaCha:      buff.DeltaCha,
			DeltaMaxHP:    buff.DeltaMaxHP,
			DeltaMaxMP:    buff.DeltaMaxMP,
			DeltaHitMod:   buff.DeltaHitMod,
			DeltaDmgMod:   buff.DeltaDmgMod,
			DeltaSP:       buff.DeltaSP,
			DeltaMR:       buff.DeltaMR,
			DeltaHPR:      buff.DeltaHPR,
			DeltaMPR:      buff.DeltaMPR,
			DeltaBowHit:   buff.DeltaBowHit,
			DeltaBowDmg:   buff.DeltaBowDmg,
			DeltaFireRes:  buff.DeltaFireRes,
			DeltaWaterRes: buff.DeltaWaterRes,
			DeltaWindRes:  buff.DeltaWindRes,
			DeltaEarthRes: buff.DeltaEarthRes,
			DeltaDodge:    buff.DeltaDodge,
			SetMoveSpeed:  buff.SetMoveSpeed,
			SetBraveSpeed: buff.SetBraveSpeed,
		}
		if buff.SkillID == skillShapeChange {
			row.PolyID = p.PolyID
		}
		rows = append(rows, row)
	}
	return rows
}

// cleanupCompanions removes all companion entities owned by a disconnecting player.
// Summons: broadcast death sound + remove. Dolls: broadcast dismiss sound + remove (no bonus reversal needed — player offline).
// Followers: respawn original NPC + remove.
func (s *InputSystem) cleanupCompanions(player *world.PlayerInfo) {
	ws := s.worldState

	// Remove summons
	for _, sum := range ws.GetSummonsByOwner(player.CharID) {
		ws.RemoveSummon(sum.ID)
		nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
		for _, viewer := range nearby {
			sendCompanionEffectPacket(viewer.Session, sum.ID, 169) // death sound
			sendRemoveCompanionPacket(viewer.Session, sum.ID)
		}
	}

	// Remove dolls (no bonus reversal — player is leaving world)
	for _, doll := range ws.GetDollsByOwner(player.CharID) {
		ws.RemoveDoll(doll.ID)
		nearby := ws.GetNearbyPlayersAt(doll.X, doll.Y, doll.MapID)
		for _, viewer := range nearby {
			sendCompanionEffectPacket(viewer.Session, doll.ID, 5936) // dismiss sound
			sendRemoveCompanionPacket(viewer.Session, doll.ID)
		}
	}

	// Remove pets — save to DB before removal (pets persist across sessions)
	for _, pet := range ws.GetPetsByOwner(player.CharID) {
		// Save pet state to DB
		if s.petRepo != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			s.petRepo.Save(ctx, &persist.PetRow{
				ItemObjID: pet.ItemObjID,
				ObjID:     pet.ID,
				NpcID:     pet.NpcID,
				Name:      pet.Name,
				Level:     pet.Level,
				HP:        pet.HP,
				MaxHP:     pet.MaxHP,
				MP:        pet.MP,
				MaxMP:     pet.MaxMP,
				Exp:       pet.Exp,
				Lawful:    pet.Lawful,
			})
			cancel()
		}
		ws.RemovePet(pet.ID)
		nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
		for _, viewer := range nearby {
			sendRemoveCompanionPacket(viewer.Session, pet.ID)
		}
	}

	// Remove follower — respawn original NPC at spawn location
	if f := ws.GetFollowerByOwner(player.CharID); f != nil {
		ws.RemoveFollower(f.ID)
		nearby := ws.GetNearbyPlayersAt(f.X, f.Y, f.MapID)
		for _, viewer := range nearby {
			sendRemoveCompanionPacket(viewer.Session, f.ID)
		}
		// Respawn original NPC
		if f.OrigNpcID != 0 && s.mapData != nil {
			// Look up NPC template from world state (spawned NPCs store template data)
			// We don't have Deps here, so just create a basic NPC shell
			npc := &world.NpcInfo{
				ID:         world.NextNpcID(),
				NpcID:      f.OrigNpcID,
				GfxID:      f.GfxID,
				Name:       f.Name,
				NameID:     f.NameID,
				Level:      f.Level,
				HP:         f.MaxHP,
				MaxHP:      f.MaxHP,
				X:          f.SpawnX,
				Y:          f.SpawnY,
				MapID:      f.SpawnMapID,
				SpawnX:     f.SpawnX,
				SpawnY:     f.SpawnY,
				SpawnMapID: f.SpawnMapID,
			}
			ws.AddNpc(npc)
			respawnNearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
			for _, viewer := range respawnNearby {
				sendNpcPackForInput(viewer.Session, npc)
			}
		}
	}
}

// sendCompanionEffectPacket sends S_EFFECT (opcode 55) for companion sound effects.
func sendCompanionEffectPacket(sess *net.Session, objID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(objID)
	w.WriteH(uint16(gfxID))
	sess.Send(w.Bytes())
}

// sendRemoveCompanionPacket sends S_REMOVE_OBJECT for a companion entity.
func sendRemoveCompanionPacket(sess *net.Session, objID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_OBJECT)
	w.WriteD(objID)
	sess.Send(w.Bytes())
}

// sendNpcPackForInput sends S_PUT_OBJECT (87) for an NPC — minimal version for input system.
// Duplicated from handler/broadcast.go to avoid circular imports.
func sendNpcPackForInput(sess *net.Session, npc *world.NpcInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(npc.X))
	w.WriteH(uint16(npc.Y))
	w.WriteD(npc.ID)
	w.WriteH(uint16(npc.GfxID))

	status := byte(0)
	if npc.Dead {
		status = 8
	}
	w.WriteC(status)
	w.WriteC(byte(npc.Heading))
	w.WriteC(0) // light
	w.WriteC(byte(npc.MoveSpeed))
	w.WriteD(0)
	w.WriteH(0)
	w.WriteS(npc.NameID)
	w.WriteS(npc.Name)
	w.WriteC(0) // poison
	w.WriteD(0)
	w.WriteS("")
	w.WriteS("")
	w.WriteC(0)
	w.WriteC(0xFF)
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0xFF)
	w.WriteC(0xFF)
	sess.Send(w.Bytes())
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
