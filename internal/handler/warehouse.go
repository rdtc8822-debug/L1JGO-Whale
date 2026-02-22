package handler

import (
	"context"
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// Warehouse types: DB storage value (wh_type column).
const (
	WhTypePersonal int16 = 3
	WhTypeElf      int16 = 4
	WhTypeClan     int16 = 5
)

// retrieveListType returns the S_RetrieveList type byte for the client.
// Personal=3, Clan=5, Elf=9 (elf uses 9 in packet, 4 in DB).
func retrieveListType(whType int16) byte {
	switch whType {
	case WhTypeElf:
		return 9
	default:
		return byte(whType) // 3 or 5
	}
}

// resultTypeToWhType maps C_Result resultType to warehouse DB type.
func resultTypeToWhType(resultType byte) (whType int16, isDeposit bool, ok bool) {
	switch resultType {
	case 2:
		return WhTypePersonal, true, true
	case 3:
		return WhTypePersonal, false, true
	case 4:
		return WhTypeClan, true, true
	case 5:
		return WhTypeClan, false, true
	case 8:
		return WhTypeElf, true, true
	case 9:
		return WhTypeElf, false, true
	default:
		return 0, false, false
	}
}

// OpenWarehouse loads warehouse items from DB and sends the retrieve list.
// Called from NPC action "retrieve", "retrieve-elven", "retrieve-pledge", "deposit".
func OpenWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16, deps *Deps) {
	ctx := context.Background()

	items, err := deps.WarehouseRepo.Load(ctx, sess.AccountName, whType)
	if err != nil {
		deps.Log.Error("倉庫載入失敗", zap.Error(err))
		return
	}

	// Build cache with temp objectIDs
	player.WarehouseItems = make([]*world.WarehouseCache, 0, len(items))
	player.WarehouseType = whType

	for _, it := range items {
		itemInfo := deps.Items.Get(it.ItemID)
		name := fmt.Sprintf("item#%d", it.ItemID)
		invGfx := int32(0)
		weight := int32(0)
		stackable := false
		var useType byte
		if itemInfo != nil {
			name = itemInfo.Name
			invGfx = itemInfo.InvGfx
			weight = itemInfo.Weight
			stackable = itemInfo.Stackable || it.ItemID == world.AdenaItemID
			useType = itemInfo.UseTypeID
		}

		wc := &world.WarehouseCache{
			TempObjID:  world.NextItemObjID(),
			DbID:       it.ID,
			ItemID:     it.ItemID,
			Count:      it.Count,
			EnchantLvl: it.EnchantLvl,
			Bless:      it.Bless,
			Stackable:  stackable,
			Identified: it.Identified,
			UseType:    useType,
			Name:       name,
			InvGfx:     invGfx,
			Weight:     weight,
		}
		player.WarehouseItems = append(player.WarehouseItems, wc)
	}

	// Send S_RETRIEVE_LIST (opcode 176) — matches Java S_RetrieveList format
	sendRetrieveList(sess, npcObjID, whType, player.WarehouseItems)

	deps.Log.Debug("warehouse opened",
		zap.String("player", player.Name),
		zap.Int16("type", whType),
		zap.Int("items", len(player.WarehouseItems)),
	)
}

// OpenWarehouseDeposit loads warehouse items (for stack-merge checking) and opens the deposit window.
// Called from NPC action "deposit", "deposit-elven", "deposit-pledge".
func OpenWarehouseDeposit(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16, deps *Deps) {
	ctx := context.Background()

	items, err := deps.WarehouseRepo.Load(ctx, sess.AccountName, whType)
	if err != nil {
		deps.Log.Error("倉庫載入失敗", zap.Error(err))
		return
	}

	// Build minimal cache — only fields needed for deposit stack-merge checking
	player.WarehouseItems = make([]*world.WarehouseCache, 0, len(items))
	player.WarehouseType = whType

	for _, it := range items {
		itemInfo := deps.Items.Get(it.ItemID)
		stackable := false
		if itemInfo != nil {
			stackable = itemInfo.Stackable || it.ItemID == world.AdenaItemID
		}

		wc := &world.WarehouseCache{
			TempObjID: world.NextItemObjID(),
			DbID:      it.ID,
			ItemID:    it.ItemID,
			Count:     it.Count,
			Stackable: stackable,
		}
		player.WarehouseItems = append(player.WarehouseItems, wc)
	}

	sendDepositWindow(sess, npcObjID)

	deps.Log.Debug("warehouse deposit opened",
		zap.String("player", player.Name),
		zap.Int16("type", whType),
		zap.Int("cached", len(player.WarehouseItems)),
	)
}

// sendRetrieveList sends S_RETRIEVE_LIST (opcode 176) — warehouse item list.
// Format matches Java S_RetrieveList / S_RetrieveElfList / S_RetrievePledgeList:
//
//	[C opcode=176][D npcObjID][H itemCount][C warehouseType]
//	Per item: [D objID][C useType][H invGfx][C bless][D count][C identified][S viewName]
//	Trailing: [D playerGold] (personal/clan only, omitted for elf)
//	          Client displays this as "全部: X" and updates it locally on withdraw/fee.
func sendRetrieveList(sess *net.Session, npcObjID int32, whType int16, items []*world.WarehouseCache) {
	rlType := retrieveListType(whType)

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_RETRIEVE_LIST)
	w.WriteD(npcObjID)
	w.WriteH(uint16(len(items)))
	w.WriteC(rlType) // warehouse type: 3=personal, 5=clan, 9=elf

	for _, it := range items {
		viewName := it.Name
		if it.EnchantLvl > 0 {
			viewName = fmt.Sprintf("+%d %s", it.EnchantLvl, viewName)
		}

		w.WriteD(it.TempObjID) // item object ID
		if whType == WhTypeElf {
			w.WriteC(0) // elf warehouse: useType always 0
		} else {
			w.WriteC(it.UseType) // personal/clan: actual useType
		}
		w.WriteH(uint16(it.InvGfx)) // inventory graphic
		w.WriteC(byte(it.Bless))     // bless state
		w.WriteD(it.Count)           // stack count
		if it.Identified {
			w.WriteC(1)
		} else {
			w.WriteC(0)
		}
		w.WriteS(viewName) // display name
	}

	// Trailing: per-item withdrawal fee (30 adena). Client displays at top-left.
	if whType != WhTypeElf {
		w.WriteD(0x1e) // 30 adena per item
	}

	sess.Send(w.Bytes())
}

// sendDepositWindow sends S_DEPOSIT (opcode 4) — tells client to show deposit window.
func sendDepositWindow(sess *net.Session, npcObjID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_DEPOSIT)
	w.WriteD(npcObjID)
	sess.Send(w.Bytes())
}

// HandleWarehouseResult processes warehouse operations from C_BUY_SELL (opcode 161).
// Called from HandleBuySell when resultType is 2-9.
//
// Java C_Result: [D npcObjID][C resultType][H count] then per item [D objID][D count]
// resultType: 2=personal deposit, 3=personal withdraw, 4=clan deposit,
//
//	5=clan withdraw, 8=elf deposit, 9=elf withdraw
func HandleWarehouseResult(sess *net.Session, r *packet.Reader, resultType byte, count int, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	whType, isDeposit, ok := resultTypeToWhType(resultType)
	if !ok {
		return
	}

	// Ensure warehouse is open and type matches
	if player.WarehouseItems == nil {
		return
	}
	if player.WarehouseType != whType {
		deps.Log.Debug("warehouse type mismatch",
			zap.Int16("expected", player.WarehouseType),
			zap.Int16("got", whType),
		)
		return
	}

	if isDeposit {
		handleWarehouseDeposit(sess, r, count, player, whType, deps)
	} else {
		handleWarehouseWithdraw(sess, r, count, player, whType, deps)
	}
}

// handleWarehouseDeposit moves items from player inventory to warehouse.
// Java C_Result resultType: 2 (personal), 4 (clan), 8 (elf).
func handleWarehouseDeposit(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, whType int16, deps *Deps) {
	if count <= 0 || count > 100 {
		return
	}

	type depositOrder struct {
		objectID int32
		qty      int32
	}
	orders := make([]depositOrder, 0, count)
	for i := 0; i < count; i++ {
		objID := r.ReadD()
		qty := r.ReadD()
		if qty <= 0 {
			qty = 1
		}
		orders = append(orders, depositOrder{objectID: objID, qty: qty})
	}

	ctx := context.Background()

	for _, o := range orders {
		invItem := player.Inv.FindByObjectID(o.objectID)
		if invItem == nil || invItem.Equipped {
			continue
		}

		qty := o.qty
		if qty > invItem.Count {
			qty = invItem.Count
		}

		// Clan warehouse: sealed items (bless >= 128) cannot be deposited
		if whType == WhTypeClan && invItem.Bless >= 128 {
			continue
		}

		itemInfo := deps.Items.Get(invItem.ItemID)
		stackable := false
		var useType byte
		if itemInfo != nil {
			stackable = itemInfo.Stackable || invItem.ItemID == world.AdenaItemID
			useType = itemInfo.UseTypeID
		}

		// Check if stackable item already exists in warehouse
		if stackable {
			found := false
			for _, wc := range player.WarehouseItems {
				if wc.ItemID == invItem.ItemID {
					err := deps.WarehouseRepo.AddToStack(ctx, wc.DbID, qty)
					if err != nil {
						deps.Log.Error("倉庫堆疊新增失敗", zap.Error(err))
						continue
					}
					wc.Count += qty
					found = true
					break
				}
			}
			if found {
				removed := player.Inv.RemoveItem(o.objectID, qty)
				if removed {
					sendRemoveInventoryItem(sess, o.objectID)
				} else {
					sendItemCountUpdate(sess, invItem)
				}
				continue
			}
		}

		// Insert new warehouse item
		whItem := persist.WarehouseItem{
			AccountName: sess.AccountName,
			CharName:    player.Name,
			WhType:      whType,
			ItemID:      invItem.ItemID,
			Count:       qty,
			EnchantLvl:  int16(invItem.EnchantLvl),
			Bless:       int16(invItem.Bless),
			Identified:  invItem.Identified,
		}

		dbID, err := deps.WarehouseRepo.Deposit(ctx, whItem)
		if err != nil {
			deps.Log.Error("倉庫存入失敗", zap.Error(err))
			continue
		}

		// Remove from inventory
		removed := player.Inv.RemoveItem(o.objectID, qty)
		if removed {
			sendRemoveInventoryItem(sess, o.objectID)
		} else {
			sendItemCountUpdate(sess, invItem)
		}

		// Add to local cache
		name := invItem.Name
		invGfx := invItem.InvGfx
		weight := invItem.Weight
		if itemInfo != nil {
			name = itemInfo.Name
			invGfx = itemInfo.InvGfx
			weight = itemInfo.Weight
		}

		wc := &world.WarehouseCache{
			TempObjID:  world.NextItemObjID(),
			DbID:       dbID,
			ItemID:     invItem.ItemID,
			Count:      qty,
			EnchantLvl: int16(invItem.EnchantLvl),
			Bless:      int16(invItem.Bless),
			Stackable:  stackable,
			Identified: invItem.Identified,
			UseType:    useType,
			Name:       name,
			InvGfx:     invGfx,
			Weight:     weight,
		}
		player.WarehouseItems = append(player.WarehouseItems, wc)
	}

	sendWeightUpdate(sess, player)

	deps.Log.Debug("warehouse deposit",
		zap.String("player", player.Name),
		zap.Int16("wh_type", whType),
		zap.Int("items", count),
	)
}

// handleWarehouseWithdraw moves items from warehouse to player inventory.
// Java C_Result resultType: 3 (personal), 5 (clan), 9 (elf).
// Withdrawal fee: personal/clan = 30 ADENA, elf = 2 Mithril (itemID 40494).
func handleWarehouseWithdraw(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, whType int16, deps *Deps) {
	if count <= 0 || count > 100 {
		return
	}

	type withdrawOrder struct {
		objectID int32
		qty      int32
	}
	orders := make([]withdrawOrder, 0, count)
	for i := 0; i < count; i++ {
		objID := r.ReadD()
		qty := r.ReadD()
		if qty <= 0 {
			qty = 1
		}
		orders = append(orders, withdrawOrder{objectID: objID, qty: qty})
	}

	if len(orders) == 0 {
		return
	}

	// Check withdrawal fee before processing
	const mithrilItemID = 40494
	if whType == WhTypeElf {
		mithril := player.Inv.FindByItemID(mithrilItemID)
		if mithril == nil || mithril.Count < 2 {
			sendServerMessage(sess, 189) // insufficient
			return
		}
	} else {
		if player.Inv.GetAdena() < 30 {
			sendServerMessage(sess, 189) // insufficient adena
			return
		}
	}

	ctx := context.Background()
	var transferred int

	for _, o := range orders {
		// Find warehouse cache entry by temp objectID
		var wc *world.WarehouseCache
		var wcIndex int
		for i, w := range player.WarehouseItems {
			if w.TempObjID == o.objectID {
				wc = w
				wcIndex = i
				break
			}
		}
		if wc == nil {
			continue
		}

		qty := o.qty
		if qty > wc.Count {
			qty = wc.Count
		}

		// Check inventory space
		if player.Inv.IsFull() {
			sendServerMessage(sess, 263) // inventory full
			break
		}

		// Remove from DB
		fullyRemoved, err := deps.WarehouseRepo.Withdraw(ctx, wc.DbID, qty)
		if err != nil {
			deps.Log.Error("倉庫取出失敗", zap.Error(err))
			continue
		}

		// Update or remove from cache
		if fullyRemoved || qty >= wc.Count {
			player.WarehouseItems = append(player.WarehouseItems[:wcIndex], player.WarehouseItems[wcIndex+1:]...)
		} else {
			wc.Count -= qty
		}

		// Add to player inventory
		existing := player.Inv.FindByItemID(wc.ItemID)
		wasExisting := existing != nil && wc.Stackable

		item := player.Inv.AddItem(
			wc.ItemID,
			qty,
			wc.Name,
			wc.InvGfx,
			wc.Weight,
			wc.Stackable,
			byte(wc.EnchantLvl),
		)
		item.UseType = wc.UseType

		if wasExisting {
			sendItemCountUpdate(sess, item)
		} else {
			sendAddItem(sess, item)
		}

		transferred++
	}

	// Deduct withdrawal fee once per operation (not per item)
	if transferred > 0 {
		if whType == WhTypeElf {
			mithril := player.Inv.FindByItemID(mithrilItemID)
			if mithril != nil {
				removed := player.Inv.RemoveItem(mithril.ObjectID, 2)
				if removed {
					sendRemoveInventoryItem(sess, mithril.ObjectID)
				} else {
					sendItemCountUpdate(sess, mithril)
				}
			}
		} else {
			adena := player.Inv.FindByItemID(world.AdenaItemID)
			if adena != nil {
				adena.Count -= 30
				if adena.Count <= 0 {
					player.Inv.RemoveItem(adena.ObjectID, 0)
					sendRemoveInventoryItem(sess, adena.ObjectID)
				} else {
					sendItemCountUpdate(sess, adena)
				}
			}
		}
	}
	sendWeightUpdate(sess, player)

	deps.Log.Debug("warehouse withdraw",
		zap.String("player", player.Name),
		zap.Int16("wh_type", whType),
		zap.Int("transferred", transferred),
	)
}
