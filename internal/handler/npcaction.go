package handler

import (
	"fmt"
	"math"
	"strings"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// HandleNpcAction processes C_HACTION (opcode 125) — player clicks a button in NPC dialog.
// Also handles S_Message_YN (yes/no dialog) responses — client sends objectID=yesNoCount.
// The action string determines what to do: "buy", "sell", "teleportURL", etc.
func HandleNpcAction(sess *net.Session, r *packet.Reader, deps *Deps) {
	objID := r.ReadD()
	action := r.ReadS()

	deps.Log.Debug("C_NpcAction",
		zap.Int32("objID", objID),
		zap.String("action", action),
	)

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	npc := deps.World.GetNpc(objID)
	if npc == nil {
		// Not an NPC — check for S_Message_YN (yes/no dialog) response
		if player.PendingYesNoType != 0 {
			lAction := strings.ToLower(action)
			accepted := lAction != "no" && lAction != "n"
			handleYesNoResponse(sess, player, accepted, deps)
		}
		return
	}
	dx := int32(math.Abs(float64(player.X - npc.X)))
	dy := int32(math.Abs(float64(player.Y - npc.Y)))
	if dx > 5 || dy > 5 {
		return
	}

	lowerAction := strings.ToLower(action)

	// Auto-cancel trade when interacting with NPC
	cancelTradeIfActive(player, deps)

	switch lowerAction {
	case "buy":
		handleShopBuy(sess, npc.NpcID, objID, deps)
	case "sell":
		handleShopSell(sess, npc.NpcID, objID, deps)
	case "buyskill":
		openSpellShop(sess, deps)
	case "teleporturl", "teleporturla", "teleporturlb", "teleporturlc",
		"teleporturld", "teleporturle", "teleporturlf", "teleporturlg",
		"teleporturlh", "teleporturli", "teleporturlj", "teleporturlk":
		handleTeleportURLGeneric(sess, npc.NpcID, objID, action, deps)

	// Warehouse
	case "retrieve":
		OpenWarehouse(sess, player, objID, WhTypePersonal, deps)
	case "retrieve-elven":
		OpenWarehouse(sess, player, objID, WhTypeElf, deps)
	case "retrieve-pledge":
		OpenWarehouse(sess, player, objID, WhTypeClan, deps)
	case "deposit":
		OpenWarehouseDeposit(sess, player, objID, WhTypePersonal, deps)
	case "deposit-elven":
		OpenWarehouseDeposit(sess, player, objID, WhTypeElf, deps)
	case "deposit-pledge":
		OpenWarehouseDeposit(sess, player, objID, WhTypeClan, deps)

	// EXP recovery / PK redemption (stub)
	case "exp":
		sendHypertext(sess, objID, "expr")
	case "pk":
		sendHypertext(sess, objID, "pkr")

	// Close dialog (empty string or explicit close)
	case "":
		// Do nothing — dialog closes

	default:
		// Check if this is a teleport action (e.g. "teleport gludin-kent")
		if strings.HasPrefix(lowerAction, "teleport ") {
			handleTeleport(sess, player, npc.NpcID, action, deps)
			return
		}

		deps.Log.Debug("unhandled NPC action",
			zap.String("action", action),
			zap.Int32("npc_id", npc.NpcID),
		)
	}
}

// handleShopBuy — player presses "Buy" — show items NPC sells.
// Sends S_SELL_LIST (opcode 70) = S_ShopSellList in Java (items NPC sells to player).
func handleShopBuy(sess *net.Session, npcID, objID int32, deps *Deps) {
	shop := deps.Shops.Get(npcID)
	if shop == nil || len(shop.SellingItems) == 0 {
		sendNoSell(sess, objID)
		return
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SELL_LIST) // opcode 70
	w.WriteD(objID)
	w.WriteH(uint16(len(shop.SellingItems)))

	for i, si := range shop.SellingItems {
		itemInfo := deps.Items.Get(si.ItemID)
		name := fmt.Sprintf("item#%d", si.ItemID)
		gfxID := int32(0)
		if itemInfo != nil {
			name = itemInfo.Name
			gfxID = itemInfo.InvGfx
		}

		// Append pack count to name if > 1
		if si.PackCount > 1 {
			name = fmt.Sprintf("%s (%d)", name, si.PackCount)
		}

		price := si.SellingPrice

		w.WriteD(int32(i))       // order index
		w.WriteH(uint16(gfxID)) // inventory graphic ID
		w.WriteD(price)          // price
		w.WriteS(name)           // item name

		// Status bytes: show item stats (damage, AC, class restrictions) like Java
		if itemInfo != nil {
			status := buildShopStatusBytes(itemInfo)
			w.WriteC(byte(len(status)))
			w.WriteBytes(status)
		} else {
			w.WriteC(0)
		}
	}

	w.WriteH(0x0007) // currency type: 7 = adena

	sess.Send(w.Bytes())
}

// handleShopSell — player presses "Sell" — show items NPC will buy from player.
// Sends S_SHOP_SELL_LIST (opcode 65) with assessed prices for player's items.
func handleShopSell(sess *net.Session, npcID, objID int32, deps *Deps) {
	shop := deps.Shops.Get(npcID)
	if shop == nil || len(shop.PurchasingItems) == 0 {
		sendNoSell(sess, objID)
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Inv == nil {
		sendNoSell(sess, objID)
		return
	}

	// Build purchasing price lookup
	purchMap := make(map[int32]int32, len(shop.PurchasingItems))
	for _, pi := range shop.PurchasingItems {
		purchMap[pi.ItemID] = pi.PurchasingPrice
	}

	// Find sellable items in player's inventory
	type assessedItem struct {
		objectID int32
		price    int32
	}
	var items []assessedItem
	for _, invItem := range player.Inv.Items {
		price, ok := purchMap[invItem.ItemID]
		if !ok {
			continue
		}
		if invItem.EnchantLvl != 0 || invItem.Bless >= 128 {
			continue // skip enchanted/sealed
		}
		items = append(items, assessedItem{objectID: invItem.ObjectID, price: price})
	}

	if len(items) == 0 {
		sendNoSell(sess, objID)
		return
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SHOP_SELL_LIST) // opcode 65
	w.WriteD(objID)
	w.WriteH(uint16(len(items)))
	for _, it := range items {
		w.WriteD(it.objectID)
		w.WriteD(it.price)
	}
	w.WriteH(0x0007) // currency: adena
	sess.Send(w.Bytes())
}

// handleTeleportURLGeneric shows the NPC's teleport page with data values (prices).
// Handles teleportURL, teleportURLA, teleportURLB, etc.
func handleTeleportURLGeneric(sess *net.Session, npcID, objID int32, action string, deps *Deps) {
	// Look up HTML data (contains htmlID + data values for price display)
	htmlData := deps.TeleportHtml.Get(npcID, action)
	if htmlData != nil {
		sendHypertextWithData(sess, objID, htmlData.HtmlID, htmlData.Data)
		return
	}

	// Fallback: try NpcAction table for teleport_url / teleport_urla
	npcAction := deps.NpcActions.Get(npcID)
	if npcAction == nil {
		return
	}
	lowerAction := strings.ToLower(action)
	switch lowerAction {
	case "teleporturl":
		if npcAction.TeleportURL != "" {
			sendHypertext(sess, objID, npcAction.TeleportURL)
		}
	case "teleporturla":
		if npcAction.TeleportURLA != "" {
			sendHypertext(sess, objID, npcAction.TeleportURLA)
		}
	}
}

// sendHypertext sends S_HYPERTEXT (opcode 39) to show an HTML dialog (no data values).
func sendHypertext(sess *net.Session, objID int32, htmlID string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(objID)
	w.WriteS(htmlID)
	w.WriteH(0x00)
	w.WriteH(0)
	sess.Send(w.Bytes())
}

// sendHypertextWithData sends S_HYPERTEXT with data values injected into the HTML template.
// Data values replace %0, %1, %2... placeholders in the client's built-in HTML.
func sendHypertextWithData(sess *net.Session, objID int32, htmlID string, data []string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(objID)
	w.WriteS(htmlID)
	if len(data) > 0 {
		w.WriteH(0x01) // has data flag
		w.WriteH(uint16(len(data)))
		for _, val := range data {
			w.WriteS(val)
		}
	} else {
		w.WriteH(0x00)
		w.WriteH(0)
	}
	sess.Send(w.Bytes())
}

// sendNoSell sends S_HYPERTEXT with "nosell" HTML to indicate NPC doesn't trade.
func sendNoSell(sess *net.Session, objID int32) {
	sendHypertext(sess, objID, "nosell")
}

// handleTeleport processes a "teleport xxx" action from the NPC dialog.
// Looks up the destination, checks adena cost, and teleports the player.
func handleTeleport(sess *net.Session, player *world.PlayerInfo, npcID int32, action string, deps *Deps) {
	dest := deps.Teleports.Get(npcID, action)
	if dest == nil {
		deps.Log.Debug("teleport destination not found",
			zap.String("action", action),
			zap.Int32("npc_id", npcID),
		)
		return
	}

	// Check adena cost
	if dest.Price > 0 {
		currentGold := player.Inv.GetAdena()
		if currentGold < dest.Price {
			sendServerMessage(sess, 189) // "金幣不足" (Insufficient adena)
			return
		}

		// Deduct adena
		adenaItem := player.Inv.FindByItemID(world.AdenaItemID)
		if adenaItem != nil {
			adenaItem.Count -= dest.Price
			if adenaItem.Count <= 0 {
				player.Inv.RemoveItem(adenaItem.ObjectID, 0)
				sendRemoveInventoryItem(sess, adenaItem.ObjectID)
			} else {
				sendItemCountUpdate(sess, adenaItem)
			}
		}
	}

	teleportPlayer(sess, player, dest.X, dest.Y, dest.MapID, dest.Heading, deps)

	deps.Log.Info(fmt.Sprintf("玩家傳送  角色=%s  動作=%s  x=%d  y=%d  地圖=%d  花費=%d", player.Name, action, dest.X, dest.Y, dest.MapID, dest.Price))
}

// teleportPlayer moves a player to a new location with full AOI updates.
// Used by NPC teleport, death restart, GM commands, etc.
func teleportPlayer(sess *net.Session, player *world.PlayerInfo, x, y int32, mapID, heading int16, deps *Deps) {
	// Update tile collision (clear old, set new)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, player.X, player.Y, false)
		deps.MapData.SetImpassable(mapID, x, y, true)
	}

	// Remove from old location for nearby players
	oldNearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range oldNearby {
		sendRemoveObject(other.Session, player.CharID)
	}

	// Update position in world state
	deps.World.UpdatePosition(sess.ID, x, y, mapID, heading)

	// Send map ID (always — even if same map, client needs it for teleport)
	sendMapID(sess, uint16(mapID), false)

	// Send own char at new position
	sendPutObject(sess, player)

	// Send status update
	sendPlayerStatus(sess, player)

	// Show nearby players at new location
	newNearby := deps.World.GetNearbyPlayers(x, y, mapID, sess.ID)
	for _, other := range newNearby {
		sendPutObject(other.Session, player) // they see us
		sendPutObject(sess, other)           // we see them
	}

	// Show nearby NPCs at new location
	nearbyNpcs := deps.World.GetNearbyNpcs(x, y, mapID)
	for _, npc := range nearbyNpcs {
		sendNpcPack(sess, npc)
	}

	// Show nearby ground items at new location
	nearbyGnd := deps.World.GetNearbyGroundItems(x, y, mapID)
	for _, g := range nearbyGnd {
		sendDropItem(sess, g)
	}

	// Send weather
	sendWeather(sess, 0)
}

// handleYesNoResponse processes S_Message_YN dialog responses.
// Routes to trade or party accept/decline based on PendingYesNoType.
func handleYesNoResponse(sess *net.Session, player *world.PlayerInfo, accepted bool, deps *Deps) {
	msgType := player.PendingYesNoType
	data := player.PendingYesNoData
	player.PendingYesNoType = 0
	player.PendingYesNoData = 0

	switch msgType {
	case 252: // Trade confirmation
		handleTradeYesNo(sess, player, data, accepted, deps)
	}
}
