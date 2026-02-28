package system

import (
	"fmt"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// ShopSystem 負責 NPC 商店交易業務邏輯（購買/販賣、金幣驗證、背包管理）。
// 實作 handler.ShopManager 介面。
type ShopSystem struct {
	deps *handler.Deps
}

// NewShopSystem 建立商店系統。
func NewShopSystem(deps *handler.Deps) *ShopSystem {
	return &ShopSystem{deps: deps}
}

// BuyFromNpc 處理玩家從 NPC 購買物品：扣金幣、給物品、發封包。
func (s *ShopSystem) BuyFromNpc(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, shop *data.Shop) {
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

	// 計算總花費
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
		itemInfo := s.deps.Items.Get(si.ItemID)
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

	// 檢查金幣
	currentGold := int64(player.Inv.GetAdena())
	if currentGold < totalCost {
		handler.SendServerMessage(sess, 189) // "金幣不足"
		return
	}

	// 檢查背包空間
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
		handler.SendServerMessage(sess, 263) // "背包已滿"
		return
	}

	// 扣除金幣
	adenaItem := player.Inv.FindByItemID(world.AdenaItemID)
	if adenaItem != nil {
		adenaItem.Count -= int32(totalCost)
		if adenaItem.Count <= 0 {
			player.Inv.RemoveItem(adenaItem.ObjectID, 0)
			handler.SendRemoveInventoryItem(sess, adenaItem.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, adenaItem)
		}
	}

	// 給予物品
	for _, ri := range resolved {
		if ri.stack {
			// 可堆疊：一次加全部（與已有堆疊合併）
			existing := player.Inv.FindByItemID(ri.itemID)
			wasExisting := existing != nil

			item := player.Inv.AddItem(ri.itemID, ri.qty, ri.name, ri.invGfx, ri.weight, true, ri.bless)
			item.UseType = ri.useTypeID

			if wasExisting {
				handler.SendItemCountUpdate(sess, item)
			} else {
				handler.SendAddItem(sess, item, ri.info)
			}
		} else {
			// 不可堆疊：每個單位獨立一格
			for j := int32(0); j < ri.qty; j++ {
				item := player.Inv.AddItem(ri.itemID, 1, ri.name, ri.invGfx, ri.weight, false, ri.bless)
				item.UseType = ri.useTypeID
				handler.SendAddItem(sess, item, ri.info)
			}
		}
	}
	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Info(fmt.Sprintf("商店購買完成  角色=%s  花費=%d  數量=%d", player.Name, totalCost, len(resolved)))
}

// SellToNpc 處理玩家向 NPC 販賣物品：移除物品、給金幣、發封包。
func (s *ShopSystem) SellToNpc(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, shop *data.Shop) {
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

		// 查詢該物品的收購價格
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
			handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, invItem)
		}
	}

	if totalEarned > 0 {
		// 給予金幣
		adena := player.Inv.FindByItemID(world.AdenaItemID)
		wasExisting := adena != nil

		adenaInfo := s.deps.Items.Get(world.AdenaItemID)
		adenaName := "Adena"
		adenaGfx := int32(318)
		if adenaInfo != nil {
			adenaName = adenaInfo.Name
			adenaGfx = adenaInfo.InvGfx
		}

		item := player.Inv.AddItem(world.AdenaItemID, int32(totalEarned), adenaName, adenaGfx, 0, true, 1)
		if wasExisting {
			handler.SendItemCountUpdate(sess, item)
		} else {
			handler.SendAddItem(sess, item)
		}
	}
	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Info(fmt.Sprintf("商店販賣完成  角色=%s  收入=%d  數量=%d", player.Name, totalEarned, count))
}
