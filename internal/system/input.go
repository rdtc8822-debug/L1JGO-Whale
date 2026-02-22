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
	sessions    map[uint64]*net.Session
	maxPerTick  int
	log         *zap.Logger
	accountRepo *persist.AccountRepo
	charRepo    *persist.CharacterRepo
	itemRepo    *persist.ItemRepo
	worldState  *world.State
	mapData     *data.MapDataTable
}

func NewInputSystem(
	netServer *net.Server,
	registry *packet.Registry,
	maxPerTick int,
	accountRepo *persist.AccountRepo,
	charRepo *persist.CharacterRepo,
	itemRepo *persist.ItemRepo,
	worldState *world.State,
	mapData *data.MapDataTable,
	log *zap.Logger,
) *InputSystem {
	return &InputSystem{
		netServer:   netServer,
		registry:    registry,
		sessions:    make(map[uint64]*net.Session),
		maxPerTick:  maxPerTick,
		log:         log,
		accountRepo: accountRepo,
		charRepo:    charRepo,
		itemRepo:    itemRepo,
		worldState:  worldState,
		mapData:     mapData,
	}
}

func (s *InputSystem) Phase() coresys.Phase { return coresys.PhaseInput }

func (s *InputSystem) Update(_ time.Duration) {
	// Accept new sessions
	for {
		select {
		case sess := <-s.netServer.NewSessions():
			s.sessions[sess.ID] = sess
		default:
			goto doneNew
		}
	}
doneNew:

	// Process dead sessions
	for {
		select {
		case id := <-s.netServer.DeadSessions():
			delete(s.sessions, id)
		default:
			goto doneDead
		}
	}
doneDead:

	// Drain packets from each session (up to maxPerTick per session)
	for id, sess := range s.sessions {
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
			s.handleDisconnect(sess)
			s.netServer.NotifyDead(id)
			delete(s.sessions, id)
			continue
		}

		for i := 0; i < s.maxPerTick; i++ {
			select {
			case data := <-sess.InQueue:
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
	}
}

// handleDisconnect cleans up when a session closes:
// removes from world state, broadcasts S_REMOVE_OBJECT, saves position, marks offline.
func (s *InputSystem) handleDisconnect(sess *net.Session) {
	// Remove from world state and broadcast removal
	player := s.worldState.RemovePlayer(sess.ID)
	if player != nil {
		// Clear tile collision for the position this player was occupying
		if s.mapData != nil {
			s.mapData.SetImpassable(player.MapID, player.X, player.Y, false)
		}

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

		// Broadcast S_REMOVE_OBJECT to nearby players
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
	return len(s.sessions)
}

// --- Disconnect cleanup packet helpers ---
// These duplicate minimal packet building from handler/ to avoid circular imports.

// restoreTradeItemsOnDisconnect restores deducted trade items/gold back to a player's inventory.
func restoreTradeItemsOnDisconnect(p *world.PlayerInfo) {
	for _, item := range p.TradeItems {
		existing := p.Inv.FindByItemID(item.ItemID)
		wasExisting := existing != nil && item.Stackable

		newItem := p.Inv.AddItem(item.ItemID, item.Count, item.Name, item.InvGfx, item.Weight, item.Stackable, item.EnchantLvl)
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
	w.WriteH(0)                    // descId
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
	w.WriteC(item.EnchantLvl)
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
