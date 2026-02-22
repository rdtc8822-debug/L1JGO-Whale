package handler

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// yesNoCounter is a global sequential number for S_Message_YN dialogs.
var yesNoCounter atomic.Int32

// HandleAskTrade processes C_ASK_XCHG (opcode 2) — initiate trade request.
// Java C_Trade reads NO packet data — target is determined by FaceToFace proximity.
// Sends S_Message_YN(252) to target for confirmation. Trade windows open only after YES.
func HandleAskTrade(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Already in trade
	if player.TradePartnerID != 0 {
		return
	}

	// Find face-to-face trade partner (adjacent tile, opposite heading)
	target := findFaceToFace(player, deps)
	if target == nil {
		sendGlobalChat(sess, 9, "找不到交易對象。")
		return
	}

	// Target already trading
	if target.TradePartnerID != 0 {
		sendGlobalChat(sess, 9, fmt.Sprintf("%s 正在進行交易中。", target.Name))
		return
	}

	// Set trade partner IDs (like Java — set immediately, windows open after YES)
	player.TradePartnerID = target.CharID
	player.TradeWindowOpen = false
	player.TradeOk = false
	player.TradeItems = nil
	player.TradeGold = 0

	target.TradePartnerID = player.CharID
	target.TradeWindowOpen = false
	target.TradeOk = false
	target.TradeItems = nil
	target.TradeGold = 0

	// Send S_Message_YN(252) to target for confirmation
	// Java: target.sendPackets(new S_Message_YN(252, player.getName()))
	count := yesNoCounter.Add(1)
	target.PendingYesNoType = 252
	target.PendingYesNoData = player.CharID

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_YES_NO)
	w.WriteH(0)
	w.WriteD(count)
	w.WriteH(252)            // message type: trade confirmation
	w.WriteS(player.Name)    // inviter name
	target.Session.Send(w.Bytes())

	deps.Log.Debug("trade request sent",
		zap.String("player", player.Name),
		zap.String("target", target.Name),
	)
}

// handleTradeYesNo processes the target's yes/no response to a trade request.
// Called from HandleAttr (attr.go).
func handleTradeYesNo(sess *net.Session, player *world.PlayerInfo, partnerID int32, accepted bool, deps *Deps) {
	partner := deps.World.GetByCharID(partnerID)

	if !accepted {
		// Declined — clear trade state
		if partner != nil {
			sendGlobalChat(partner.Session, 9, fmt.Sprintf("%s 拒絕了交易。", player.Name))
			clearTradeState(partner)
		}
		clearTradeState(player)
		return
	}

	// Accepted — verify partner still waiting
	if partner == nil || partner.TradePartnerID != player.CharID {
		clearTradeState(player)
		return
	}

	// Open trade windows for both
	player.TradeWindowOpen = true
	partner.TradeWindowOpen = true

	sendTradeOpen(partner.Session, player.Name)
	sendTradeOpen(sess, partner.Name)

	deps.Log.Debug("trade accepted",
		zap.String("player", player.Name),
		zap.String("partner", partner.Name),
	)
}

// findFaceToFace finds a player at the adjacent tile in the direction the player is facing,
// who is also facing back toward the player (opposite heading). Java: L1World.findFaceToFace.
func findFaceToFace(player *world.PlayerInfo, deps *Deps) *world.PlayerInfo {
	h := player.Heading
	if h < 0 || h > 7 {
		return nil
	}

	// Expected target position: one tile in the direction player is facing
	targetX := player.X + headingDX[h]
	targetY := player.Y + headingDY[h]
	oppositeH := (h + 4) % 8

	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, player.SessionID)
	for _, other := range nearby {
		if other.X == targetX && other.Y == targetY && other.Heading == oppositeH {
			return other
		}
	}
	return nil
}

// HandleAddTrade processes C_ADD_XCHG (opcode 37) — add item to trade.
// Format: [D objectID][D count]
// Java approach: deduct from inventory IMMEDIATELY when adding to trade.
// On trade complete, only add to partner. On cancel, restore to source.
func HandleAddTrade(sess *net.Session, r *packet.Reader, deps *Deps) {
	objectID := r.ReadD()
	count := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.TradePartnerID == 0 || !player.TradeWindowOpen {
		return
	}

	partner := deps.World.GetByCharID(player.TradePartnerID)
	if partner == nil {
		cancelTrade(player, nil, deps)
		return
	}

	// Reset both confirmations when items change
	player.TradeOk = false
	partner.TradeOk = false

	// Handle gold separately (AdenaItemID)
	if objectID == 0 {
		// Adding gold — objectID=0 means gold, count = amount
		if count <= 0 {
			return
		}

		// Restore previously traded gold first (if changing amount)
		if player.TradeGold > 0 {
			adena := player.Inv.FindByItemID(world.AdenaItemID)
			if adena != nil {
				adena.Count += player.TradeGold
			}
			player.TradeGold = 0
		}

		currentGold := player.Inv.GetAdena()
		if count > currentGold {
			count = currentGold
		}
		if count <= 0 {
			return
		}
		player.TradeGold = count

		// Deduct gold from inventory immediately (Java approach)
		adena := player.Inv.FindByItemID(world.AdenaItemID)
		if adena != nil {
			adena.Count -= count
			if adena.Count <= 0 {
				player.Inv.RemoveItem(adena.ObjectID, 0)
				sendRemoveInventoryItem(sess, adena.ObjectID)
			} else {
				sendItemCountUpdate(sess, adena)
			}
		}

		// Notify both players
		goldName := fmt.Sprintf("金幣 (%d)", count)
		sendTradeAddItem(sess, 0, goldName, 1, 0)            // type 0 = own panel (top)
		sendTradeAddItem(partner.Session, 0, goldName, 1, 1) // type 1 = partner panel (bottom)
		return
	}

	invItem := player.Inv.FindByObjectID(objectID)
	if invItem == nil || invItem.Equipped {
		return
	}

	// Check tradeable — YAML tradeable: true means Java trade=1 (non-tradeable)
	itemInfo := deps.Items.Get(invItem.ItemID)
	if itemInfo != nil && itemInfo.Tradeable {
		sendGlobalChat(sess, 9, "此道具無法交易。")
		return
	}

	if count <= 0 {
		count = invItem.Count
	}
	if count > invItem.Count {
		count = invItem.Count
	}

	// Check if already added
	for _, ti := range player.TradeItems {
		if ti.ObjectID == objectID {
			return // already in trade
		}
	}

	// Limit trade items
	if len(player.TradeItems) >= 16 {
		return
	}

	// Store a COPY with the trade count
	tradeCopy := *invItem
	tradeCopy.Count = count
	player.TradeItems = append(player.TradeItems, &tradeCopy)

	// Deduct from real inventory immediately (Java approach: tradeItem)
	removed := player.Inv.RemoveItem(invItem.ObjectID, count)
	if removed {
		sendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		// Partial: real item still exists with reduced count
		sendItemCountUpdate(sess, invItem)
	}
	sendWeightUpdate(sess, player)

	// Build display name (Java: getNumberedViewName)
	viewName := tradeCopy.Name
	if tradeCopy.EnchantLvl > 0 {
		viewName = fmt.Sprintf("+%d %s", tradeCopy.EnchantLvl, viewName)
	}
	if count > 1 {
		viewName = fmt.Sprintf("%s (%d)", viewName, count)
	}

	// Send to both players: type 0 = own panel, type 1 = partner panel
	sendTradeAddItem(sess, uint16(tradeCopy.InvGfx), viewName, byte(tradeCopy.Bless), 0)
	sendTradeAddItem(partner.Session, uint16(tradeCopy.InvGfx), viewName, byte(tradeCopy.Bless), 1)

	deps.Log.Debug("trade item added",
		zap.String("player", player.Name),
		zap.Int32("item_id", tradeCopy.ItemID),
		zap.Int32("count", count),
	)
}

// HandleAcceptTrade processes C_ACCEPT_XCHG (opcode 71) — confirm trade.
func HandleAcceptTrade(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.TradePartnerID == 0 || !player.TradeWindowOpen {
		return
	}

	partner := deps.World.GetByCharID(player.TradePartnerID)
	if partner == nil {
		cancelTrade(player, nil, deps)
		return
	}

	player.TradeOk = true

	// If both confirmed, execute trade
	if player.TradeOk && partner.TradeOk {
		executeTrade(player, partner, deps)
	}
}

// HandleCancelTrade processes C_CANCEL_XCHG (opcode 86) — cancel trade.
func HandleCancelTrade(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.TradePartnerID == 0 {
		return
	}

	partner := deps.World.GetByCharID(player.TradePartnerID)
	cancelTrade(player, partner, deps)
}

// executeTrade performs the item + gold exchange between two players.
// Items were already deducted from source inventories during HandleAddTrade.
// This function only adds items to the receiving partner.
func executeTrade(p1, p2 *world.PlayerInfo, deps *Deps) {
	// Build WAL entries
	var walEntries []persist.WALEntry

	for _, item := range p1.TradeItems {
		walEntries = append(walEntries, persist.WALEntry{
			TxType:     "trade",
			FromChar:   p1.CharID,
			ToChar:     p2.CharID,
			ItemID:     item.ItemID,
			Count:      item.Count,
			EnchantLvl: int16(item.EnchantLvl),
		})
	}

	for _, item := range p2.TradeItems {
		walEntries = append(walEntries, persist.WALEntry{
			TxType:     "trade",
			FromChar:   p2.CharID,
			ToChar:     p1.CharID,
			ItemID:     item.ItemID,
			Count:      item.Count,
			EnchantLvl: int16(item.EnchantLvl),
		})
	}

	if p1.TradeGold > 0 {
		walEntries = append(walEntries, persist.WALEntry{
			TxType:     "trade",
			FromChar:   p1.CharID,
			ToChar:     p2.CharID,
			ItemID:     world.AdenaItemID,
			GoldAmount: int64(p1.TradeGold),
		})
	}
	if p2.TradeGold > 0 {
		walEntries = append(walEntries, persist.WALEntry{
			TxType:     "trade",
			FromChar:   p2.CharID,
			ToChar:     p1.CharID,
			ItemID:     world.AdenaItemID,
			GoldAmount: int64(p2.TradeGold),
		})
	}

	// Write WAL to DB first (safety)
	if len(walEntries) > 0 && deps.WALRepo != nil {
		ctx := context.Background()
		if err := deps.WALRepo.WriteWAL(ctx, walEntries); err != nil {
			deps.Log.Error("交易 WAL 寫入失敗，取消交易", zap.Error(err))
			cancelTrade(p1, p2, deps)
			return
		}
	}

	// WAL succeeded — items already deducted from sources, now add to receivers

	// Add p1's items to p2
	for _, item := range p1.TradeItems {
		addTradeItemToPlayer(p2, item, deps)
	}

	// Add p2's items to p1
	for _, item := range p2.TradeItems {
		addTradeItemToPlayer(p1, item, deps)
	}

	// Transfer gold (already deducted from source, just add to receiver)
	if p1.TradeGold > 0 {
		addGoldToPlayer(p2, p1.TradeGold)
	}
	if p2.TradeGold > 0 {
		addGoldToPlayer(p1, p2.TradeGold)
	}

	// Close trade windows — Java: 0 = trade complete
	sendTradeStatus(p1.Session, 0)
	sendTradeStatus(p2.Session, 0)

	// Clear trade state (no restore needed — trade succeeded)
	clearTradeState(p1)
	clearTradeState(p2)

	deps.Log.Info(fmt.Sprintf("交易完成  玩家1=%s  玩家2=%s", p1.Name, p2.Name))
}

// addTradeItemToPlayer adds a trade item to the receiving player's inventory.
func addTradeItemToPlayer(receiver *world.PlayerInfo, item *world.InvItem, deps *Deps) {
	itemInfo := deps.Items.Get(item.ItemID)
	stackable := false
	invGfx := item.InvGfx
	weight := item.Weight
	name := item.Name
	if itemInfo != nil {
		stackable = itemInfo.Stackable || item.ItemID == world.AdenaItemID
		invGfx = itemInfo.InvGfx
		weight = itemInfo.Weight
		name = itemInfo.Name
	}

	existing := receiver.Inv.FindByItemID(item.ItemID)
	wasExisting := existing != nil && stackable

	newItem := receiver.Inv.AddItem(item.ItemID, item.Count, name, invGfx, weight, stackable, item.EnchantLvl)
	if itemInfo != nil {
		newItem.UseType = itemInfo.UseTypeID
	}
	if wasExisting {
		sendItemCountUpdate(receiver.Session, newItem)
	} else {
		sendAddItem(receiver.Session, newItem)
	}
	sendWeightUpdate(receiver.Session, receiver)
}

// addGoldToPlayer adds gold to the receiving player (source already deducted).
func addGoldToPlayer(receiver *world.PlayerInfo, amount int32) {
	adena := receiver.Inv.FindByItemID(world.AdenaItemID)
	if adena != nil {
		adena.Count += amount
		sendItemCountUpdate(receiver.Session, adena)
	} else {
		newItem := receiver.Inv.AddItem(world.AdenaItemID, amount, "金幣", 0, 0, true, 1)
		sendAddItem(receiver.Session, newItem)
	}
	sendWeightUpdate(receiver.Session, receiver)
}

// cancelTrade cancels the trade, restores items to sources, and clears state.
func cancelTrade(p1 *world.PlayerInfo, p2 *world.PlayerInfo, deps *Deps) {
	// Restore items to source inventories (they were deducted on add-to-trade)
	restoreTradeItems(p1, deps)

	// Java: S_TradeStatus 1 = cancelled (only send if trade window was open)
	if p1.TradeWindowOpen {
		sendTradeStatus(p1.Session, 1)
	}
	clearTradeState(p1)

	if p2 != nil {
		restoreTradeItems(p2, deps)
		if p2.TradeWindowOpen {
			sendTradeStatus(p2.Session, 1)
		}
		clearTradeState(p2)
	}

	deps.Log.Debug("trade cancelled", zap.String("player", p1.Name))
}

// restoreTradeItems returns deducted items and gold back to a player's inventory.
// Called when trade is cancelled.
func restoreTradeItems(p *world.PlayerInfo, deps *Deps) {
	// Restore traded items
	for _, item := range p.TradeItems {
		itemInfo := deps.Items.Get(item.ItemID)
		stackable := false
		name := item.Name
		invGfx := item.InvGfx
		weight := item.Weight
		if itemInfo != nil {
			stackable = itemInfo.Stackable || item.ItemID == world.AdenaItemID
			name = itemInfo.Name
			invGfx = itemInfo.InvGfx
			weight = itemInfo.Weight
		}

		existing := p.Inv.FindByItemID(item.ItemID)
		wasExisting := existing != nil && stackable

		newItem := p.Inv.AddItem(item.ItemID, item.Count, name, invGfx, weight, stackable, item.EnchantLvl)
		if itemInfo != nil {
			newItem.UseType = itemInfo.UseTypeID
		}
		if wasExisting {
			sendItemCountUpdate(p.Session, newItem)
		} else {
			sendAddItem(p.Session, newItem)
		}
	}

	// Restore gold
	if p.TradeGold > 0 {
		adena := p.Inv.FindByItemID(world.AdenaItemID)
		if adena != nil {
			adena.Count += p.TradeGold
			sendItemCountUpdate(p.Session, adena)
		} else {
			newItem := p.Inv.AddItem(world.AdenaItemID, p.TradeGold, "金幣", 0, 0, true, 1)
			sendAddItem(p.Session, newItem)
		}
	}
	sendWeightUpdate(p.Session, p)
}

// clearTradeState resets all trade-related fields.
func clearTradeState(p *world.PlayerInfo) {
	p.TradePartnerID = 0
	p.TradeWindowOpen = false
	p.TradeOk = false
	p.TradeItems = nil
	p.TradeGold = 0
}

// cancelTradeIfActive cancels the trade if the player is currently in one.
// Called when player moves too far, opens shop, teleports, etc.
func cancelTradeIfActive(player *world.PlayerInfo, deps *Deps) {
	if player.TradePartnerID == 0 {
		return
	}
	partner := deps.World.GetByCharID(player.TradePartnerID)
	cancelTrade(player, partner, deps)
}

// --- Trade packets ---

// sendTradeOpen sends S_TRADE (opcode 52) — open trade window.
// Java: writeD(0) + writeS(partnerName)
func sendTradeOpen(sess *net.Session, partnerName string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TRADE)
	w.WriteD(0) // always 0 (Java: writeD(0))
	w.WriteS(partnerName)
	sess.Send(w.Bytes())
}

// sendTradeAddItem sends S_TRADEADDITEM (opcode 35) — item added to trade.
// Java format: [C type][H gfxId][S viewName][C bless][C statusLen][...status][H 0]
// type: 0=top panel (own items), 1=bottom panel (partner's items)
func sendTradeAddItem(sess *net.Session, gfxID uint16, viewName string, bless byte, panelType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TRADEADDITEM)
	w.WriteC(panelType)          // 0=top (own), 1=bottom (partner)
	w.WriteH(gfxID)              // item graphic ID
	w.WriteS(viewName)           // item name (includes enchant prefix + count suffix)
	w.WriteC(bless)              // bless status: 0=blessed, 1=normal, 2=cursed
	w.WriteC(0)                  // status bytes length = 0
	w.WriteH(0)                  // padding
	sess.Send(w.Bytes())
}

// sendTradeStatus sends S_TRADESTATUS (opcode 112) — trade state update.
// Java: 0 = trade complete (取引完了), 1 = trade cancelled (取引キャンセル)
func sendTradeStatus(sess *net.Session, status byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TRADESTATUS)
	w.WriteC(status)
	sess.Send(w.Bytes())
}
