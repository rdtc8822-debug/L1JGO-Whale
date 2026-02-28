package handler

import (
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

	if deps.Shop == nil {
		return
	}

	switch resultType {
	case 0:
		// Buy from NPC — player purchases items
		deps.Shop.BuyFromNpc(sess, r, count, player, shop)
	case 1:
		// Sell to NPC — player sells items
		deps.Shop.SellToNpc(sess, r, count, player, shop)
	}
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
	// 尾部固定 11 bytes（Java: S_AddItem 與 S_InvList 共用格式）
	w.WriteC(10) // 固定值 0x0A
	w.WriteH(0)
	w.WriteD(0)
	w.WriteD(0)
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
		w.WriteH(world.ItemDescID(item.ItemID))  // descId — Java: switch(itemId) for material items
		w.WriteC(item.UseType)                    // use type
		w.WriteC(0)                               // charge count
		w.WriteH(uint16(item.InvGfx))            // inv gfx
		w.WriteC(world.EffectiveBless(item))      // bless: 3=unidentified
		w.WriteD(item.Count)                      // count
		w.WriteC(itemStatusX(item, itemInfo))     // itemStatusX
		// 顯示名稱（含強化前綴、數量後綴、裝備後綴）
		viewName := buildViewName(item, itemInfo)
		w.WriteS(viewName)
		// 狀態欄位：僅已鑑定物品
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
		// 尾部固定 11 bytes（Java: S_InvList / S_AddItem 共用）
		w.WriteC(10) // 固定值 0x0A
		w.WriteH(0)
		w.WriteD(0)
		w.WriteD(0)
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
