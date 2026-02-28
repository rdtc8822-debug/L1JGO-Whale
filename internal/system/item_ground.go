package system

// item_ground.go — 物品地面操作系統（銷毀、掉落、撿取）。
// 業務邏輯由 handler/item.go 抽出，handler 只負責解封包 + 委派。

import (
	"fmt"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// ItemGroundSystem 實作 handler.ItemGroundManager。
type ItemGroundSystem struct {
	deps *handler.Deps
}

// NewItemGroundSystem 建立 ItemGroundSystem。
func NewItemGroundSystem(deps *handler.Deps) *ItemGroundSystem {
	return &ItemGroundSystem{deps: deps}
}

// DestroyItem 銷毀背包中的物品。
func (s *ItemGroundSystem) DestroyItem(sess *net.Session, player *world.PlayerInfo, objectID, count int32) {
	item := player.Inv.FindByObjectID(objectID)
	if item == nil {
		return
	}

	// 已裝備的物品不可銷毀
	if item.Equipped {
		return
	}

	if count <= 0 {
		count = item.Count
	}
	if count > item.Count {
		count = item.Count
	}

	removed := player.Inv.RemoveItem(objectID, count)
	if removed {
		handler.SendRemoveInventoryItem(sess, objectID)
	} else {
		handler.SendItemCountUpdate(sess, item)
	}
	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Debug("物品已銷毀",
		zap.String("player", player.Name),
		zap.Int32("item_id", item.ItemID),
		zap.Int32("count", count),
	)
}

// DropItem 將物品掉落至地面。
func (s *ItemGroundSystem) DropItem(sess *net.Session, player *world.PlayerInfo, objectID, count int32) {
	item := player.Inv.FindByObjectID(objectID)
	if item == nil {
		return
	}

	// 已裝備的物品不可掉落
	if item.Equipped {
		return
	}

	if count <= 0 {
		count = item.Count
	}
	if count > item.Count {
		count = item.Count
	}

	// 移除前先記錄物品資訊
	itemID := item.ItemID
	itemName := item.Name
	enchantLvl := item.EnchantLvl

	removed := player.Inv.RemoveItem(objectID, count)
	if removed {
		handler.SendRemoveInventoryItem(sess, objectID)
	} else {
		handler.SendItemCountUpdate(sess, item)
	}
	handler.SendWeightUpdate(sess, player)

	// 查詢地面圖示
	grdGfx := int32(0)
	itemInfo := s.deps.Items.Get(itemID)
	if itemInfo != nil {
		grdGfx = itemInfo.GrdGfx
	}

	// 建構顯示名稱
	displayName := itemName
	if enchantLvl > 0 {
		displayName = fmt.Sprintf("+%d %s", enchantLvl, displayName)
	} else if enchantLvl < 0 {
		displayName = fmt.Sprintf("%d %s", enchantLvl, displayName)
	}
	if count > 1 {
		displayName = fmt.Sprintf("%s (%d)", displayName, count)
	}

	// 在玩家位置建立地面物品
	gndItem := &world.GroundItem{
		ID:         world.NextGroundItemID(),
		ItemID:     itemID,
		Count:      count,
		EnchantLvl: enchantLvl,
		Name:       displayName,
		GrdGfx:     grdGfx,
		X:          player.X,
		Y:          player.Y,
		MapID:      player.MapID,
		OwnerID:    player.CharID,
		TTL:        5 * 60 * 5, // 5 分鐘（200ms tick）
	}
	s.deps.World.AddGroundItem(gndItem)

	// 廣播給附近玩家（含自己）
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		handler.SendDropItem(viewer.Session, gndItem)
	}

	s.deps.Log.Debug("物品掉落至地面",
		zap.String("player", player.Name),
		zap.Int32("item_id", itemID),
		zap.Int32("count", count),
		zap.Int32("ground_id", gndItem.ID),
	)
}

// PickupItem 從地面撿取物品。
func (s *ItemGroundSystem) PickupItem(sess *net.Session, player *world.PlayerInfo, objectID int32) {
	if player.Dead {
		return
	}

	gndItem := s.deps.World.GetGroundItem(objectID)
	if gndItem == nil {
		return
	}

	// 距離檢查（Chebyshev <= 3）
	dx := player.X - gndItem.X
	dy := player.Y - gndItem.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	dist := dx
	if dy > dist {
		dist = dy
	}
	if dist > 3 {
		return
	}

	// 地圖檢查
	if player.MapID != gndItem.MapID {
		return
	}

	// 背包空間檢查
	if player.Inv.IsFull() {
		handler.SendServerMessage(sess, 263) // 背包已滿
		return
	}

	// 負重檢查
	pickupInfo := s.deps.Items.Get(gndItem.ItemID)
	if pickupInfo != nil {
		addWeight := pickupInfo.Weight * gndItem.Count
		maxW := world.MaxWeight(player.Str, player.Con)
		if player.Inv.IsOverWeight(addWeight, maxW) {
			handler.SendServerMessage(sess, 82) // 此物品太重了，所以你無法攜帶。
			return
		}
	}

	// 從世界移除
	s.deps.World.RemoveGroundItem(objectID)

	// 廣播移除給附近玩家
	nearby := s.deps.World.GetNearbyPlayersAt(gndItem.X, gndItem.Y, gndItem.MapID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, gndItem.ID)
	}

	// 加入背包
	itemInfo := s.deps.Items.Get(gndItem.ItemID)
	itemName := gndItem.Name
	invGfx := int32(0)
	weight := int32(0)
	stackable := false
	if itemInfo != nil {
		itemName = itemInfo.Name
		invGfx = itemInfo.InvGfx
		weight = itemInfo.Weight
		stackable = itemInfo.Stackable || gndItem.ItemID == world.AdenaItemID
	}

	existing := player.Inv.FindByItemID(gndItem.ItemID)
	wasExisting := existing != nil && stackable

	bless := byte(0)
	if itemInfo != nil {
		bless = byte(itemInfo.Bless)
	}
	invItem := player.Inv.AddItem(
		gndItem.ItemID,
		gndItem.Count,
		itemName,
		invGfx,
		weight,
		stackable,
		bless,
	)
	invItem.EnchantLvl = gndItem.EnchantLvl
	if itemInfo != nil {
		invItem.UseType = itemInfo.UseTypeID
	}

	if wasExisting {
		handler.SendItemCountUpdate(sess, invItem)
	} else {
		handler.SendAddItem(sess, invItem)
	}

	// 更新負重條
	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Debug("撿取物品",
		zap.String("player", player.Name),
		zap.Int32("item_id", gndItem.ItemID),
		zap.Int32("count", gndItem.Count),
	)
}

// SendAddItem 需要的匯出確認 — 已有 handler.SendAddItem（預設不帶 itemInfo 時使用 item 內部資料）。
// 若未來需要自訂 itemInfo，可傳 optional 參數：handler.SendAddItem(sess, item, info)。
