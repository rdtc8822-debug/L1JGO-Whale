package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// HandleBuySell processes C_BUY_SELL (opcode 161) — player confirms a shop transaction.
// Java name: C_Result. Handles buy (0), sell (1), and warehouse operations (2-9).
func HandleBuySell(sess *net.Session, r *packet.Reader, deps *Deps) {
	npcObjID := r.ReadD()
	resultType := r.ReadC()
	count := int(r.ReadH())

	// Warehouse operations (resultType 2-9) route through warehouse handler.
	// Must check BEFORE NPC/shop lookup — warehouse NPCs have no shop data.
	if resultType >= 2 {
		HandleWarehouseResult(sess, r, resultType, count, deps)
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	npc := deps.World.GetNpc(npcObjID)
	if npc == nil {
		return
	}

	shop := deps.Shops.Get(npc.NpcID)
	if shop == nil {
		return
	}

	switch resultType {
	case 0:
		// Buy from NPC — player purchases items
		handleBuyFromNpc(sess, r, count, player, shop, deps)
	case 1:
		// Sell to NPC — player sells items
		handleSellToNpc(sess, r, count, player, shop, deps)
	}
}

// handleBuyFromNpc processes a purchase: deduct gold, give items, send packets.
func handleBuyFromNpc(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, shop *data.Shop, deps *Deps) {
	if count <= 0 || count > 100 {
		return
	}

	type buyOrder struct {
		orderIdx int32
		qty      int32
	}
	orders := make([]buyOrder, 0, count)
	for i := 0; i < count; i++ {
		idx := r.ReadD()
		qty := r.ReadD()
		if qty <= 0 {
			qty = 1
		}
		orders = append(orders, buyOrder{orderIdx: idx, qty: qty})
	}

	// Calculate total cost
	var totalCost int64
	type resolvedItem struct {
		itemID    int32
		name      string
		invGfx    int32
		weight    int32
		qty       int32
		bless     byte
		stack     bool
		useTypeID byte
		info      *data.ItemInfo
	}
	resolved := make([]resolvedItem, 0, len(orders))

	for _, o := range orders {
		if int(o.orderIdx) < 0 || int(o.orderIdx) >= len(shop.SellingItems) {
			continue
		}
		si := shop.SellingItems[o.orderIdx]
		itemInfo := deps.Items.Get(si.ItemID)
		if itemInfo == nil {
			continue
		}

		qty := o.qty * si.PackCount
		price := int64(si.SellingPrice) * int64(o.qty)
		totalCost += price

		resolved = append(resolved, resolvedItem{
			itemID:    si.ItemID,
			name:      itemInfo.Name,
			invGfx:    itemInfo.InvGfx,
			weight:    itemInfo.Weight,
			qty:       qty,
			bless:     byte(itemInfo.Bless),
			stack:     itemInfo.Stackable || si.ItemID == world.AdenaItemID,
			useTypeID: itemInfo.UseTypeID,
			info:      itemInfo,
		})
	}

	if len(resolved) == 0 {
		return
	}

	// Check gold
	currentGold := int64(player.Inv.GetAdena())
	if currentGold < totalCost {
		sendServerMessage(sess, 189) // "Adena insufficient"
		return
	}

	// Check inventory space
	newSlots := 0
	for _, ri := range resolved {
		if ri.stack {
			existing := player.Inv.FindByItemID(ri.itemID)
			if existing == nil {
				newSlots++
			}
		} else {
			newSlots += int(ri.qty)
		}
	}
	if player.Inv.Size()+newSlots > world.MaxInventorySize {
		sendServerMessage(sess, 263) // "Inventory full"
		return
	}

	// Deduct gold
	adenaItem := player.Inv.FindByItemID(world.AdenaItemID)
	if adenaItem != nil {
		adenaItem.Count -= int32(totalCost)
		if adenaItem.Count <= 0 {
			player.Inv.RemoveItem(adenaItem.ObjectID, 0)
			sendRemoveInventoryItem(sess, adenaItem.ObjectID)
		} else {
			sendItemCountUpdate(sess, adenaItem)
		}
	}

	// Give items
	for _, ri := range resolved {
		if ri.stack {
			// Stackable: add all at once (stacks with existing if present)
			existing := player.Inv.FindByItemID(ri.itemID)
			wasExisting := existing != nil

			item := player.Inv.AddItem(ri.itemID, ri.qty, ri.name, ri.invGfx, ri.weight, true, ri.bless)
			item.UseType = ri.useTypeID

			if wasExisting {
				sendItemCountUpdate(sess, item)
			} else {
				sendAddItem(sess, item, ri.info)
			}
		} else {
			// Non-stackable: each unit is a separate inventory slot
			for j := int32(0); j < ri.qty; j++ {
				item := player.Inv.AddItem(ri.itemID, 1, ri.name, ri.invGfx, ri.weight, false, ri.bless)
				item.UseType = ri.useTypeID
				sendAddItem(sess, item, ri.info)
			}
		}
	}
	sendWeightUpdate(sess, player)

	deps.Log.Info(fmt.Sprintf("商店購買完成  角色=%s  花費=%d  數量=%d", player.Name, totalCost, len(resolved)))
}

// handleSellToNpc processes a sell: remove items from player, give gold.
func handleSellToNpc(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, shop *data.Shop, deps *Deps) {
	if count <= 0 || count > 100 {
		return
	}

	type sellOrder struct {
		objectID int32
		qty      int32
	}
	orders := make([]sellOrder, 0, count)
	for i := 0; i < count; i++ {
		objID := r.ReadD()
		qty := r.ReadD()
		if qty <= 0 {
			qty = 1
		}
		orders = append(orders, sellOrder{objectID: objID, qty: qty})
	}

	var totalEarned int64

	for _, o := range orders {
		invItem := player.Inv.FindByObjectID(o.objectID)
		if invItem == nil {
			continue
		}

		// Find purchasing price for this item in the shop
		var purchPrice int32
		found := false
		for _, pi := range shop.PurchasingItems {
			if pi.ItemID == invItem.ItemID {
				purchPrice = pi.PurchasingPrice
				found = true
				break
			}
		}
		if !found {
			continue
		}

		sellQty := o.qty
		if sellQty > invItem.Count {
			sellQty = invItem.Count
		}

		earned := int64(purchPrice) * int64(sellQty)
		totalEarned += earned

		removed := player.Inv.RemoveItem(invItem.ObjectID, sellQty)
		if removed {
			sendRemoveInventoryItem(sess, invItem.ObjectID)
		} else {
			sendItemCountUpdate(sess, invItem)
		}
	}

	if totalEarned > 0 {
		// Give adena
		adena := player.Inv.FindByItemID(world.AdenaItemID)
		wasExisting := adena != nil

		adenaInfo := deps.Items.Get(world.AdenaItemID)
		adenaName := "Adena"
		adenaGfx := int32(318)
		if adenaInfo != nil {
			adenaName = adenaInfo.Name
			adenaGfx = adenaInfo.InvGfx
		}

		item := player.Inv.AddItem(world.AdenaItemID, int32(totalEarned), adenaName, adenaGfx, 0, true, 1)
		if wasExisting {
			sendItemCountUpdate(sess, item)
		} else {
			sendAddItem(sess, item)
		}
	}
	sendWeightUpdate(sess, player)

	deps.Log.Info(fmt.Sprintf("商店販賣完成  角色=%s  收入=%d  數量=%d", player.Name, totalEarned, count))
}

// --- Inventory packet helpers ---

// sendAddItem sends S_ADD_ITEM (opcode 15) — new item appears in inventory.
// Optional itemInfo enables status bytes (item stats) for identified items.
func sendAddItem(sess *net.Session, item *world.InvItem, optInfo ...*data.ItemInfo) {
	var itemInfo *data.ItemInfo
	if len(optInfo) > 0 {
		itemInfo = optInfo[0]
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ADD_ITEM)
	w.WriteD(item.ObjectID)                     // item object ID
	w.WriteH(world.ItemDescID(item.ItemID))     // descId — Java: switch(itemId) for material items
	w.WriteC(item.UseType)                 // use type
	w.WriteC(0)                            // charge count
	w.WriteH(uint16(item.InvGfx))         // inventory graphic ID
	w.WriteC(world.EffectiveBless(item))   // bless: 3=unidentified, else actual
	w.WriteD(item.Count)                   // stack count
	w.WriteC(itemStatusX(item, itemInfo))  // itemStatusX
	w.WriteS(buildViewName(item, itemInfo)) // display name
	// Status bytes: include item stats for identified items
	if item.Identified && itemInfo != nil {
		statusBytes := buildStatusBytes(item, itemInfo)
		if len(statusBytes) > 0 {
			w.WriteC(byte(len(statusBytes)))
			w.WriteBytes(statusBytes)
		} else {
			w.WriteC(0)
		}
	} else {
		w.WriteC(0)
	}
	w.WriteC(0x17)                         // unknown
	w.WriteC(0)                            // padding
	w.WriteH(0)                            // padding
	w.WriteH(0)                            // padding
	w.WriteC(byte(item.EnchantLvl))        // enchant level
	w.WriteD(item.ObjectID)                // world serial
	w.WriteD(0)                            // unknown
	w.WriteD(0)                            // unknown
	w.WriteD(7)                            // flags: 7=deletable
	w.WriteC(0)                            // trailing
	sess.Send(w.Bytes())
}

// sendRemoveInventoryItem sends S_REMOVE_INVENTORY (opcode 57) — item removed.
func sendRemoveInventoryItem(sess *net.Session, objectID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_INVENTORY)
	w.WriteD(objectID)
	sess.Send(w.Bytes())
}

// sendItemCountUpdate sends S_CHANGE_ITEM_USE (opcode 24) — update stack count.
func sendItemCountUpdate(sess *net.Session, item *world.InvItem) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_ITEM_USE)
	w.WriteD(item.ObjectID)
	w.WriteS(buildViewName(item, nil))
	w.WriteD(item.Count)
	w.WriteC(0) // status bytes length = 0
	sess.Send(w.Bytes())
}

// sendServerMessage sends S_MESSAGE_CODE (opcode 71) — system message by ID.
func sendServerMessage(sess *net.Session, msgID uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID) // message ID in client string table
	w.WriteC(0)     // no arguments
	sess.Send(w.Bytes())
}

// sendServerMessageArgs sends S_MESSAGE_CODE (opcode 71) with string arguments.
// The client substitutes %0, %1, ... with the provided args.
func sendServerMessageArgs(sess *net.Session, msgID uint16, args ...string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(byte(len(args)))
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// sendInvList sends S_ADD_INVENTORY_BATCH (opcode 5) — full inventory.
func sendInvList(sess *net.Session, inv *world.Inventory, items *data.ItemTable) {
	if inv == nil || len(inv.Items) == 0 {
		// Send empty list
		w := packet.NewWriterWithOpcode(packet.S_OPCODE_ADD_INVENTORY_BATCH)
		w.WriteC(0)
		sess.Send(w.Bytes())
		return
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ADD_INVENTORY_BATCH)
	w.WriteC(byte(len(inv.Items)))

	for _, item := range inv.Items {
		var itemInfo *data.ItemInfo
		if items != nil {
			itemInfo = items.Get(item.ItemID)
		}

		w.WriteD(item.ObjectID)
		w.WriteH(world.ItemDescID(item.ItemID))      // descId — Java: switch(itemId) for material items
		w.WriteC(item.UseType)                   // use type
		w.WriteC(0)                              // charge count
		w.WriteH(uint16(item.InvGfx))           // inv gfx
		w.WriteC(world.EffectiveBless(item))     // bless: 3=unidentified
		w.WriteD(item.Count)                     // count
		w.WriteC(itemStatusX(item, itemInfo))    // itemStatusX
		// Build view name (includes enchant prefix, count suffix, equipped suffix)
		w.WriteS(buildViewName(item, itemInfo)) // name
		// Status bytes: only for identified items
		if item.Identified && itemInfo != nil {
			statusBytes := buildStatusBytes(item, itemInfo)
			if len(statusBytes) > 0 {
				w.WriteC(byte(len(statusBytes)))
				w.WriteBytes(statusBytes)
			} else {
				w.WriteC(0)
			}
		} else {
			w.WriteC(0) // unidentified: no status bytes
		}
		w.WriteC(0x17) // unknown
		w.WriteC(0)
		w.WriteH(0)
		w.WriteH(0)
		w.WriteC(byte(item.EnchantLvl))  // enchant level
		w.WriteD(item.ObjectID)          // world serial
		w.WriteD(0)
		w.WriteD(0)
		w.WriteD(7)                // flags: deletable
		w.WriteC(0)
	}

	sess.Send(w.Bytes())
}

// --- 匯出封裝（供 system 套件使用） ---

// SendAddItem 匯出 sendAddItem — 供 system 套件發送新物品到背包。
func SendAddItem(sess *net.Session, item *world.InvItem, optInfo ...*data.ItemInfo) {
	sendAddItem(sess, item, optInfo...)
}

// SendItemCountUpdate 匯出 sendItemCountUpdate — 供 system 套件更新物品數量。
func SendItemCountUpdate(sess *net.Session, item *world.InvItem) {
	sendItemCountUpdate(sess, item)
}

// SendRemoveInventoryItem 匯出 sendRemoveInventoryItem — 供 system 套件移除背包物品。
func SendRemoveInventoryItem(sess *net.Session, objectID int32) {
	sendRemoveInventoryItem(sess, objectID)
}

// SendServerMessage 匯出 sendServerMessage — 供 system 套件發送系統訊息。
func SendServerMessage(sess *net.Session, msgID uint16) {
	sendServerMessage(sess, msgID)
}

// SendServerMessageArgs 匯出 sendServerMessageArgs — 供 system 套件發送帶參數系統訊息。
func SendServerMessageArgs(sess *net.Session, msgID uint16, args ...string) {
	sendServerMessageArgs(sess, msgID, args...)
}
