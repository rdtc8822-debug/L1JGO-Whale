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
	WhTypePersonal  int16 = 3
	WhTypeElf       int16 = 4
	WhTypeClan      int16 = 5
	WhTypeCharacter int16 = 6 // 角色專屬倉庫（每角色獨立，非帳號共享）
)

// retrieveListType 回傳 S_RetrieveList 封包中的 type 字節。
// Java: Personal=3, Clan=5, Elf=9, Character=18
func retrieveListType(whType int16) byte {
	switch whType {
	case WhTypeElf:
		return 9
	case WhTypeCharacter:
		return 18 // Java S_RetrieveChaList: writeC(18)
	default:
		return byte(whType) // 3 or 5
	}
}

// resultTypeToWhType maps C_Result resultType to warehouse DB type.
// Java C_Result: 2/3=personal, 4/5=clan, 8/9=elf, 17/18=character
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
	case 17:
		return WhTypeCharacter, true, true
	case 18:
		return WhTypeCharacter, false, true
	default:
		return 0, false, false
	}
}

// loadWarehouseCache 從 DB 載入倉庫物品並填充玩家快取。不發送任何封包。
// 3.80C 客戶端的「存放物品」按鈕會直接開啟存入介面（不經過 NPC 動作），
// 因此存入操作到達時 WarehouseItems 可能尚未載入。此函式用於延遲初始化。
//
// 不同倉庫類型使用不同的 DB 查詢鍵：
//   - Personal/Elf: account_name（帳號共享）
//   - Clan: clan_name（存在 account_name 欄位，全盟共享）
//   - Character: char_name（角色專屬）
func loadWarehouseCache(sess *net.Session, player *world.PlayerInfo, whType int16, deps *Deps) error {
	ctx := context.Background()

	var items []persist.WarehouseItem
	var err error
	switch whType {
	case WhTypeCharacter:
		items, err = deps.WarehouseRepo.LoadByCharName(ctx, player.Name, whType)
	case WhTypeClan:
		items, err = deps.WarehouseRepo.Load(ctx, player.ClanName, whType)
	default: // Personal, Elf
		items, err = deps.WarehouseRepo.Load(ctx, sess.AccountName, whType)
	}
	if err != nil {
		return err
	}

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
	return nil
}

// OpenWarehouse loads warehouse items from DB and sends the retrieve list.
// Called from NPC action "retrieve", "retrieve-elven", "retrieve-pledge".
func OpenWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16, deps *Deps) {
	if err := loadWarehouseCache(sess, player, whType, deps); err != nil {
		deps.Log.Error("倉庫載入失敗", zap.Error(err))
		return
	}

	// Send S_RETRIEVE_LIST (opcode 176) — matches Java S_RetrieveList
	sendWarehouseList(sess, npcObjID, whType, player.WarehouseItems, int32(deps.Config.Gameplay.WarehousePersonalFee))

	deps.Log.Debug("warehouse opened",
		zap.String("player", player.Name),
		zap.Int16("type", whType),
		zap.Int("items", len(player.WarehouseItems)),
	)
}

// OpenWarehouseDeposit opens the warehouse window for deposit operations.
// Called from NPC action "deposit", "deposit-elven", "deposit-pledge".
//
// In Java, there is NO separate "deposit" NPC action — the 3.80C client's warehouse
// window has both withdraw and deposit tabs built in. The server sends the same
// S_OPCODE_RETRIEVE_LIST (type 3) regardless, and the client handles mode switching.
// S_OPCODE_DEPOSIT (opcode 4) is castle treasury only — must NOT be used for warehouse.
func OpenWarehouseDeposit(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16, deps *Deps) {
	OpenWarehouse(sess, player, npcObjID, whType, deps)
}

// sendWarehouseList sends S_RETRIEVE_LIST (opcode 176) — warehouse item list.
// Format matches Java S_RetrieveList / S_RetrieveElfList / S_RetrievePledgeList:
//
//	[C opcode=176][D npcObjID][H itemCount][C warehouseType]
//	Per item: [D objID][C useType][H invGfx][C bless][D count][C identified][S viewName]
//	Trailing: [D fee] (personal/clan only, omitted for elf)
//
// Type byte: 3=personal, 5=clan, 9=elf.
// The 3.80C client warehouse window has both withdraw and deposit tabs built in.
func sendWarehouseList(sess *net.Session, npcObjID int32, whType int16, items []*world.WarehouseCache, fee int32) {
	typeCode := retrieveListType(whType)

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_RETRIEVE_LIST)
	w.WriteD(npcObjID)
	w.WriteH(uint16(len(items)))
	w.WriteC(typeCode)

	for _, it := range items {
		viewName := it.Name
		if it.EnchantLvl > 0 {
			viewName = fmt.Sprintf("+%d %s", it.EnchantLvl, viewName)
		} else if it.EnchantLvl < 0 {
			viewName = fmt.Sprintf("%d %s", it.EnchantLvl, viewName)
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
		w.WriteD(fee)
	}

	sess.Send(w.Bytes())
}

// HandleWarehouseResult processes warehouse operations from C_BUY_SELL (opcode 161).
// Called from HandleBuySell when resultType >= 2.
//
// Java C_Result: [D npcObjID][C resultType][H count] then per item [D objID][D count]
// resultType: 2/3=personal, 4/5=clan, 8/9=elf, 17/18=character (even=deposit, odd=withdraw)
func HandleWarehouseResult(sess *net.Session, r *packet.Reader, resultType byte, count int, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	whType, isDeposit, ok := resultTypeToWhType(resultType)
	if !ok {
		return
	}

	// Java: 血盟倉庫 Cancel/ESC 時 count=0，必須立即解除單人鎖定。
	// C_Result case 4/5: if (size == 0) { clan.setWarehouseUsingChar(0); }
	if count == 0 && whType == WhTypeClan {
		releaseClanWarehouseLock(player, deps)
		return
	}

	// 3.80C 客戶端的 "storage" 對話框「存放物品」按鈕會直接開啟存入介面，
	// 不經過 NPC 動作（不送 "retrieve"），因此 WarehouseItems 可能尚未載入。
	// 存入操作時自動從 DB 載入快取；領取操作必須先透過 NPC 動作開啟倉庫
	//（客戶端需要 S_RetrieveList 中的 tempObjID 才能選擇物品）。
	if player.WarehouseItems == nil {
		if !isDeposit {
			return
		}
		if err := loadWarehouseCache(sess, player, whType, deps); err != nil {
			deps.Log.Error("倉庫自動載入失敗", zap.Error(err))
			return
		}
	}
	if player.WarehouseType != whType {
		if !isDeposit {
			deps.Log.Debug("warehouse type mismatch",
				zap.Int16("expected", player.WarehouseType),
				zap.Int16("got", whType),
			)
			return
		}
		if err := loadWarehouseCache(sess, player, whType, deps); err != nil {
			deps.Log.Error("倉庫重新載入失敗", zap.Error(err))
			return
		}
	}

	if isDeposit {
		handleWarehouseDeposit(sess, r, count, player, whType, deps)
	} else {
		handleWarehouseWithdraw(sess, r, count, player, whType, deps)
	}

	// 血盟倉庫操作完成後解除鎖定（Java: clan.setWarehouseUsingChar(0)）
	if whType == WhTypeClan {
		releaseClanWarehouseLock(player, deps)
	}
}

// releaseClanWarehouseLock 解除血盟倉庫單人使用鎖定。
func releaseClanWarehouseLock(player *world.PlayerInfo, deps *Deps) {
	if player.ClanID == 0 {
		return
	}
	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan != nil && clan.WarehouseUsingCharID == player.CharID {
		clan.WarehouseUsingCharID = 0
	}
}

// handleWarehouseDeposit moves items from player inventory to warehouse.
// Java C_Result resultType: 2 (personal), 4 (clan), 8 (elf), 17 (character).
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

	// 決定 DB 存入的 account_name 鍵：
	// Personal/Elf/Character = 帳號名，Clan = 血盟名
	dbAccountName := sess.AccountName
	if whType == WhTypeClan {
		dbAccountName = player.ClanName
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

		// 血盟倉庫：封印物品（bless >= 128）不可存入
		if whType == WhTypeClan && invItem.Bless >= 128 {
			continue
		}

		itemInfo := deps.Items.Get(invItem.ItemID)
		stackable := false
		var useType byte
		itemName := invItem.Name
		if itemInfo != nil {
			stackable = itemInfo.Stackable || invItem.ItemID == world.AdenaItemID
			useType = itemInfo.UseTypeID
			itemName = itemInfo.Name
		}

		// 檢查倉庫中是否已有同種可堆疊物品
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
				// 血盟倉庫歷史記錄（type=0 存入）
				if whType == WhTypeClan {
					_ = deps.WarehouseRepo.InsertClanWarehouseHistory(
						ctx, player.ClanID, player.Name, 0, itemName, qty)
				}
				continue
			}
		}

		// 新增倉庫物品
		whItem := persist.WarehouseItem{
			AccountName: dbAccountName,
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

		// 從背包移除
		removed := player.Inv.RemoveItem(o.objectID, qty)
		if removed {
			sendRemoveInventoryItem(sess, o.objectID)
		} else {
			sendItemCountUpdate(sess, invItem)
		}

		// 新增到本地快取
		invGfx := invItem.InvGfx
		weight := invItem.Weight
		if itemInfo != nil {
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
			Name:       itemName,
			InvGfx:     invGfx,
			Weight:     weight,
		}
		player.WarehouseItems = append(player.WarehouseItems, wc)

		// 血盟倉庫歷史記錄（type=0 存入）
		if whType == WhTypeClan {
			_ = deps.WarehouseRepo.InsertClanWarehouseHistory(
				ctx, player.ClanID, player.Name, 0, itemName, qty)
		}
	}

	sendWeightUpdate(sess, player)

	deps.Log.Debug("warehouse deposit",
		zap.String("player", player.Name),
		zap.Int16("wh_type", whType),
		zap.Int("items", count),
	)
}

// handleWarehouseWithdraw moves items from warehouse to player inventory.
// Java C_Result resultType: 3 (personal), 5 (clan), 9 (elf), 18 (character).
// Withdrawal fee: personal/clan/character = 30 ADENA, elf = 2 Mithril (itemID 40494).
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
	elfFee := int32(deps.Config.Gameplay.WarehouseElfFee)
	personalFee := int32(deps.Config.Gameplay.WarehousePersonalFee)
	if whType == WhTypeElf {
		mithril := player.Inv.FindByItemID(mithrilItemID)
		if mithril == nil || mithril.Count < elfFee {
			sendServerMessage(sess, 189) // insufficient
			return
		}
	} else {
		if player.Inv.GetAdena() < personalFee {
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
			byte(wc.Bless),
		)
		item.EnchantLvl = int8(wc.EnchantLvl)
		item.Identified = wc.Identified
		item.UseType = wc.UseType

		if wasExisting {
			sendItemCountUpdate(sess, item)
		} else {
			sendAddItem(sess, item)
		}

		// 血盟倉庫歷史記錄（type=1 領出）
		if whType == WhTypeClan {
			_ = deps.WarehouseRepo.InsertClanWarehouseHistory(
				ctx, player.ClanID, player.Name, 1, wc.Name, qty)
		}

		transferred++
	}

	// Deduct withdrawal fee once per operation (not per item)
	if transferred > 0 {
		if whType == WhTypeElf {
			mithril := player.Inv.FindByItemID(mithrilItemID)
			if mithril != nil {
				removed := player.Inv.RemoveItem(mithril.ObjectID, elfFee)
				if removed {
					sendRemoveInventoryItem(sess, mithril.ObjectID)
				} else {
					sendItemCountUpdate(sess, mithril)
				}
			}
		} else {
			adena := player.Inv.FindByItemID(world.AdenaItemID)
			if adena != nil {
				adena.Count -= personalFee
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

// OpenClanWarehouse 開啟血盟倉庫，含單人使用鎖定驗證。
// Java: S_RetrievePledgeList 建構函數中執行鎖定。
func OpenClanWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32, deps *Deps) {
	if player.ClanID == 0 {
		sendServerMessage(sess, 208) // 必須加入血盟
		return
	}
	// Java: 禁止 rank=7（一般成員）和 rank=2（聯盟一般）
	if player.ClanRank == world.ClanRankPublic || player.ClanRank == world.ClanRankLeaguePublic {
		sendServerMessage(sess, 728) // 等級不符
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		sendServerMessage(sess, 208)
		return
	}

	// 單人使用鎖定（Java: S_RetrievePledgeList 行 20-28）
	if clan.WarehouseUsingCharID != 0 && clan.WarehouseUsingCharID != player.CharID {
		sendServerMessage(sess, 209) // 血盟倉庫正被他人使用
		return
	}

	if err := loadWarehouseCache(sess, player, WhTypeClan, deps); err != nil {
		deps.Log.Error("血盟倉庫載入失敗", zap.Error(err))
		return
	}

	// 標記此玩家正在使用
	clan.WarehouseUsingCharID = player.CharID

	sendWarehouseList(sess, npcObjID, WhTypeClan, player.WarehouseItems, int32(deps.Config.Gameplay.WarehousePersonalFee))

	deps.Log.Debug("clan warehouse opened",
		zap.String("player", player.Name),
		zap.Int32("clan_id", player.ClanID),
		zap.Int("items", len(player.WarehouseItems)),
	)
}

// SendClanWarehouseHistory 發送血盟倉庫歷史記錄。
// Java: S_PledgeWarehouseHistory — opcode=S_OPCODE_EVENT(250), subtype=117
func SendClanWarehouseHistory(sess *net.Session, clanID int32, deps *Deps) {
	ctx := context.Background()
	entries, err := deps.WarehouseRepo.LoadClanWarehouseHistory(ctx, clanID)
	if err != nil {
		deps.Log.Error("血盟倉庫歷史載入失敗", zap.Error(err))
		return
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(117) // S_PacketBox.HTML_CLAN_WARHOUSE_RECORD
	w.WriteD(int32(len(entries)))
	for _, e := range entries {
		w.WriteS(e.CharName)
		w.WriteC(byte(e.Type)) // 0=存入, 1=領出
		w.WriteS(e.ItemName)
		w.WriteD(e.ItemCount)
		w.WriteD(e.MinutesAgo) // 距今幾分鐘
	}
	sess.Send(w.Bytes())
}
