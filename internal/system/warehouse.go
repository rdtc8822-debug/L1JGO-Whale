package system

import (
	"context"
	"fmt"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// WarehouseSystem 負責倉庫業務邏輯（存入/領出、DB 操作、血盟鎖定）。
// 實作 handler.WarehouseManager 介面。
type WarehouseSystem struct {
	deps *handler.Deps
}

// NewWarehouseSystem 建立倉庫系統。
func NewWarehouseSystem(deps *handler.Deps) *WarehouseSystem {
	return &WarehouseSystem{deps: deps}
}

// OpenWarehouse 從 DB 載入倉庫物品並發送列表封包。
// 由 NPC 動作 "retrieve"、"retrieve-elven"、"retrieve-char" 呼叫。
func (s *WarehouseSystem) OpenWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16) {
	if err := s.loadWarehouseCache(sess, player, whType); err != nil {
		s.deps.Log.Error("倉庫載入失敗", zap.Error(err))
		return
	}

	handler.SendWarehouseList(sess, npcObjID, whType, player.WarehouseItems, int32(s.deps.Config.Gameplay.WarehousePersonalFee))

	s.deps.Log.Debug("warehouse opened",
		zap.String("player", player.Name),
		zap.Int16("type", whType),
		zap.Int("items", len(player.WarehouseItems)),
	)
}

// OpenWarehouseDeposit 開啟倉庫存入介面。
// 3.80C 客戶端倉庫視窗內建提取/存入 tab，伺服器發送相同的 S_RetrieveList。
func (s *WarehouseSystem) OpenWarehouseDeposit(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16) {
	s.OpenWarehouse(sess, player, npcObjID, whType)
}

// OpenClanWarehouse 開啟血盟倉庫，含權限驗證與單人使用鎖定。
// Java: S_RetrievePledgeList 建構函數中執行鎖定。
func (s *WarehouseSystem) OpenClanWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32) {
	if player.ClanID == 0 {
		handler.SendServerMessage(sess, 208) // 必須加入血盟
		return
	}
	// Java: 禁止 rank=7（一般成員）和 rank=2（聯盟一般）
	if player.ClanRank == world.ClanRankPublic || player.ClanRank == world.ClanRankLeaguePublic {
		handler.SendServerMessage(sess, 728) // 等級不符
		return
	}

	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		handler.SendServerMessage(sess, 208)
		return
	}

	// 單人使用鎖定（Java: S_RetrievePledgeList 行 20-28）
	if clan.WarehouseUsingCharID != 0 && clan.WarehouseUsingCharID != player.CharID {
		handler.SendServerMessage(sess, 209) // 血盟倉庫正被他人使用
		return
	}

	if err := s.loadWarehouseCache(sess, player, handler.WhTypeClan); err != nil {
		s.deps.Log.Error("血盟倉庫載入失敗", zap.Error(err))
		return
	}

	// 標記此玩家正在使用
	clan.WarehouseUsingCharID = player.CharID

	handler.SendWarehouseList(sess, npcObjID, handler.WhTypeClan, player.WarehouseItems, int32(s.deps.Config.Gameplay.WarehousePersonalFee))

	s.deps.Log.Debug("clan warehouse opened",
		zap.String("player", player.Name),
		zap.Int32("clan_id", player.ClanID),
		zap.Int("items", len(player.WarehouseItems)),
	)
}

// HandleWarehouseOp 處理倉庫存入/領出操作。
// 由 handler.HandleWarehouseResult 在封包解析後呼叫。
func (s *WarehouseSystem) HandleWarehouseOp(sess *net.Session, r *packet.Reader, resultType byte, count int, player *world.PlayerInfo) {
	whType, isDeposit, ok := handler.ResultTypeToWhType(resultType)
	if !ok {
		return
	}

	// Java: 血盟倉庫 Cancel/ESC 時 count=0，必須立即解除單人鎖定。
	if count == 0 && whType == handler.WhTypeClan {
		s.releaseClanWarehouseLock(player)
		return
	}

	// 3.80C 客戶端的 "storage" 對話框「存放物品」按鈕會直接開啟存入介面，
	// 不經過 NPC 動作（不送 "retrieve"），因此 WarehouseItems 可能尚未載入。
	if player.WarehouseItems == nil {
		if !isDeposit {
			return
		}
		if err := s.loadWarehouseCache(sess, player, whType); err != nil {
			s.deps.Log.Error("倉庫自動載入失敗", zap.Error(err))
			return
		}
	}
	if player.WarehouseType != whType {
		if !isDeposit {
			s.deps.Log.Debug("warehouse type mismatch",
				zap.Int16("expected", player.WarehouseType),
				zap.Int16("got", whType),
			)
			return
		}
		if err := s.loadWarehouseCache(sess, player, whType); err != nil {
			s.deps.Log.Error("倉庫重新載入失敗", zap.Error(err))
			return
		}
	}

	if isDeposit {
		s.handleWarehouseDeposit(sess, r, count, player, whType)
	} else {
		s.handleWarehouseWithdraw(sess, r, count, player, whType)
	}

	// 血盟倉庫操作完成後解除鎖定（Java: clan.setWarehouseUsingChar(0)）
	if whType == handler.WhTypeClan {
		s.releaseClanWarehouseLock(player)
	}
}

// SendClanWarehouseHistory 發送血盟倉庫歷史記錄。
// Java: S_PledgeWarehouseHistory — opcode=S_OPCODE_EVENT(250), subtype=117
func (s *WarehouseSystem) SendClanWarehouseHistory(sess *net.Session, clanID int32) {
	ctx := context.Background()
	entries, err := s.deps.WarehouseRepo.LoadClanWarehouseHistory(ctx, clanID)
	if err != nil {
		s.deps.Log.Error("血盟倉庫歷史載入失敗", zap.Error(err))
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

// ========================================================================
//  內部函式
// ========================================================================

// loadWarehouseCache 從 DB 載入倉庫物品並填充玩家快取。
func (s *WarehouseSystem) loadWarehouseCache(sess *net.Session, player *world.PlayerInfo, whType int16) error {
	ctx := context.Background()

	var items []persist.WarehouseItem
	var err error
	switch whType {
	case handler.WhTypeCharacter:
		items, err = s.deps.WarehouseRepo.LoadByCharName(ctx, player.Name, whType)
	case handler.WhTypeClan:
		items, err = s.deps.WarehouseRepo.Load(ctx, player.ClanName, whType)
	default: // Personal, Elf
		items, err = s.deps.WarehouseRepo.Load(ctx, sess.AccountName, whType)
	}
	if err != nil {
		return err
	}

	player.WarehouseItems = make([]*world.WarehouseCache, 0, len(items))
	player.WarehouseType = whType

	for _, it := range items {
		itemInfo := s.deps.Items.Get(it.ItemID)
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

// handleWarehouseDeposit 將物品從玩家背包移至倉庫。
func (s *WarehouseSystem) handleWarehouseDeposit(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, whType int16) {
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

	// 決定 DB 存入的 account_name 鍵
	dbAccountName := sess.AccountName
	if whType == handler.WhTypeClan {
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
		if whType == handler.WhTypeClan && invItem.Bless >= 128 {
			continue
		}

		itemInfo := s.deps.Items.Get(invItem.ItemID)
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
					err := s.deps.WarehouseRepo.AddToStack(ctx, wc.DbID, qty)
					if err != nil {
						s.deps.Log.Error("倉庫堆疊新增失敗", zap.Error(err))
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
					handler.SendRemoveInventoryItem(sess, o.objectID)
				} else {
					handler.SendItemCountUpdate(sess, invItem)
				}
				if whType == handler.WhTypeClan {
					_ = s.deps.WarehouseRepo.InsertClanWarehouseHistory(
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

		dbID, err := s.deps.WarehouseRepo.Deposit(ctx, whItem)
		if err != nil {
			s.deps.Log.Error("倉庫存入失敗", zap.Error(err))
			continue
		}

		// 從背包移除
		removed := player.Inv.RemoveItem(o.objectID, qty)
		if removed {
			handler.SendRemoveInventoryItem(sess, o.objectID)
		} else {
			handler.SendItemCountUpdate(sess, invItem)
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

		if whType == handler.WhTypeClan {
			_ = s.deps.WarehouseRepo.InsertClanWarehouseHistory(
				ctx, player.ClanID, player.Name, 0, itemName, qty)
		}
	}

	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Debug("warehouse deposit",
		zap.String("player", player.Name),
		zap.Int16("wh_type", whType),
		zap.Int("items", count),
	)
}

// handleWarehouseWithdraw 將物品從倉庫移至玩家背包。
func (s *WarehouseSystem) handleWarehouseWithdraw(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, whType int16) {
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

	// 領出費用驗證
	const mithrilItemID = 40494
	elfFee := int32(s.deps.Config.Gameplay.WarehouseElfFee)
	personalFee := int32(s.deps.Config.Gameplay.WarehousePersonalFee)
	if whType == handler.WhTypeElf {
		mithril := player.Inv.FindByItemID(mithrilItemID)
		if mithril == nil || mithril.Count < elfFee {
			handler.SendServerMessage(sess, 189)
			return
		}
	} else {
		if player.Inv.GetAdena() < personalFee {
			handler.SendServerMessage(sess, 189)
			return
		}
	}

	ctx := context.Background()
	var transferred int

	for _, o := range orders {
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

		if player.Inv.IsFull() {
			handler.SendServerMessage(sess, 263)
			break
		}

		fullyRemoved, err := s.deps.WarehouseRepo.Withdraw(ctx, wc.DbID, qty)
		if err != nil {
			s.deps.Log.Error("倉庫取出失敗", zap.Error(err))
			continue
		}

		if fullyRemoved || qty >= wc.Count {
			player.WarehouseItems = append(player.WarehouseItems[:wcIndex], player.WarehouseItems[wcIndex+1:]...)
		} else {
			wc.Count -= qty
		}

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
			handler.SendItemCountUpdate(sess, item)
		} else {
			handler.SendAddItem(sess, item)
		}

		if whType == handler.WhTypeClan {
			_ = s.deps.WarehouseRepo.InsertClanWarehouseHistory(
				ctx, player.ClanID, player.Name, 1, wc.Name, qty)
		}

		transferred++
	}

	// 每次操作扣一次費用（非每物品）
	if transferred > 0 {
		if whType == handler.WhTypeElf {
			mithril := player.Inv.FindByItemID(mithrilItemID)
			if mithril != nil {
				removed := player.Inv.RemoveItem(mithril.ObjectID, elfFee)
				if removed {
					handler.SendRemoveInventoryItem(sess, mithril.ObjectID)
				} else {
					handler.SendItemCountUpdate(sess, mithril)
				}
			}
		} else {
			adena := player.Inv.FindByItemID(world.AdenaItemID)
			if adena != nil {
				adena.Count -= personalFee
				if adena.Count <= 0 {
					player.Inv.RemoveItem(adena.ObjectID, 0)
					handler.SendRemoveInventoryItem(sess, adena.ObjectID)
				} else {
					handler.SendItemCountUpdate(sess, adena)
				}
			}
		}
	}
	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Debug("warehouse withdraw",
		zap.String("player", player.Name),
		zap.Int16("wh_type", whType),
		zap.Int("transferred", transferred),
	)
}

// releaseClanWarehouseLock 解除血盟倉庫單人使用鎖定。
func (s *WarehouseSystem) releaseClanWarehouseLock(player *world.PlayerInfo) {
	if player.ClanID == 0 {
		return
	}
	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan != nil && clan.WarehouseUsingCharID == player.CharID {
		clan.WarehouseUsingCharID = 0
	}
}
