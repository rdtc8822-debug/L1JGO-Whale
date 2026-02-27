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

// TradeSystem 負責所有交易邏輯（發起、加物品、確認、取消、WAL 安全寫入）。
// 實作 handler.TradeManager 介面。
type TradeSystem struct {
	deps *handler.Deps
}

// NewTradeSystem 建立交易系統。
func NewTradeSystem(deps *handler.Deps) *TradeSystem {
	return &TradeSystem{deps: deps}
}

// InitiateTrade 向目標發送交易確認對話框。
// player 和 target 已由 handler 找到並驗證過。
func (s *TradeSystem) InitiateTrade(sess *net.Session, player, target *world.PlayerInfo) {
	// 目標已在交易中
	if target.TradePartnerID != 0 {
		handler.SendGlobalChat(sess, 9, fmt.Sprintf("%s 正在進行交易中。", target.Name))
		return
	}

	// 設定雙方交易狀態（Java 行為：立即設定，視窗在 YES 後才開啟）
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

	// 發送 S_Message_YN(252) 到目標進行確認
	target.PendingYesNoType = 252
	target.PendingYesNoData = player.CharID
	handler.SendYesNoDialog(target.Session, 252, player.Name)

	s.deps.Log.Debug("交易請求已發送",
		zap.String("player", player.Name),
		zap.String("target", target.Name),
	)
}

// HandleYesNo 處理目標的 Yes/No 回應。
func (s *TradeSystem) HandleYesNo(sess *net.Session, player *world.PlayerInfo, partnerID int32, accepted bool) {
	partner := s.deps.World.GetByCharID(partnerID)

	if !accepted {
		if partner != nil {
			handler.SendGlobalChat(partner.Session, 9, fmt.Sprintf("%s 拒絕了交易。", player.Name))
			clearTradeState(partner)
		}
		clearTradeState(player)
		return
	}

	// 接受 — 驗證夥伴仍在等待
	if partner == nil || partner.TradePartnerID != player.CharID {
		clearTradeState(player)
		return
	}

	// 開啟雙方交易視窗
	player.TradeWindowOpen = true
	partner.TradeWindowOpen = true

	sendTradeOpen(partner.Session, player.Name)
	sendTradeOpen(sess, partner.Name)

	s.deps.Log.Debug("交易已接受",
		zap.String("player", player.Name),
		zap.String("partner", partner.Name),
	)
}

// AddItem 將物品加入交易視窗。objectID==0 表示金幣。
func (s *TradeSystem) AddItem(sess *net.Session, player *world.PlayerInfo, objectID, count int32) {
	if player.TradePartnerID == 0 || !player.TradeWindowOpen {
		return
	}

	partner := s.deps.World.GetByCharID(player.TradePartnerID)
	if partner == nil {
		s.cancelTrade(player, nil)
		return
	}

	// 物品變更時重置雙方確認
	player.TradeOk = false
	partner.TradeOk = false

	// 金幣特殊處理
	if objectID == 0 {
		s.addGoldToTrade(sess, player, partner, count)
		return
	}

	invItem := player.Inv.FindByObjectID(objectID)
	if invItem == nil || invItem.Equipped {
		return
	}

	// 檢查可交易性 — YAML tradeable: false 表示不可交易
	itemInfo := s.deps.Items.Get(invItem.ItemID)
	if itemInfo != nil && !itemInfo.Tradeable {
		handler.SendGlobalChat(sess, 9, "此道具無法交易。")
		return
	}

	if count <= 0 {
		count = invItem.Count
	}
	if count > invItem.Count {
		count = invItem.Count
	}

	// 檢查是否已加入
	for _, ti := range player.TradeItems {
		if ti.ObjectID == objectID {
			return
		}
	}

	// 限制交易物品數量
	if len(player.TradeItems) >= 16 {
		return
	}

	// 儲存副本
	tradeCopy := *invItem
	tradeCopy.Count = count
	player.TradeItems = append(player.TradeItems, &tradeCopy)

	// 立即從背包扣除（Java 行為）
	removed := player.Inv.RemoveItem(invItem.ObjectID, count)
	if removed {
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, invItem)
	}
	handler.SendWeightUpdate(sess, player)

	// 建構顯示名稱
	viewName := tradeCopy.Name
	if tradeCopy.EnchantLvl > 0 {
		viewName = fmt.Sprintf("+%d %s", tradeCopy.EnchantLvl, viewName)
	} else if tradeCopy.EnchantLvl < 0 {
		viewName = fmt.Sprintf("%d %s", tradeCopy.EnchantLvl, viewName)
	}
	if count > 1 {
		viewName = fmt.Sprintf("%s (%d)", viewName, count)
	}

	// 通知雙方
	sendTradeAddItem(sess, uint16(tradeCopy.InvGfx), viewName, byte(tradeCopy.Bless), 0)
	sendTradeAddItem(partner.Session, uint16(tradeCopy.InvGfx), viewName, byte(tradeCopy.Bless), 1)

	s.deps.Log.Debug("交易物品加入",
		zap.String("player", player.Name),
		zap.Int32("item_id", tradeCopy.ItemID),
		zap.Int32("count", count),
	)
}

// addGoldToTrade 將金幣加入交易視窗。
func (s *TradeSystem) addGoldToTrade(sess *net.Session, player, partner *world.PlayerInfo, count int32) {
	if count <= 0 {
		return
	}

	// 先歸還之前的金幣（若修改金額）
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

	// 立即從背包扣除
	adena := player.Inv.FindByItemID(world.AdenaItemID)
	if adena != nil {
		adena.Count -= count
		if adena.Count <= 0 {
			player.Inv.RemoveItem(adena.ObjectID, 0)
			handler.SendRemoveInventoryItem(sess, adena.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, adena)
		}
	}

	// 通知雙方
	goldName := fmt.Sprintf("金幣 (%d)", count)
	sendTradeAddItem(sess, 0, goldName, 1, 0)
	sendTradeAddItem(partner.Session, 0, goldName, 1, 1)
}

// Accept 確認交易。雙方都確認後執行交換。
func (s *TradeSystem) Accept(sess *net.Session, player *world.PlayerInfo) {
	if player.TradePartnerID == 0 || !player.TradeWindowOpen {
		return
	}

	partner := s.deps.World.GetByCharID(player.TradePartnerID)
	if partner == nil {
		s.cancelTrade(player, nil)
		return
	}

	player.TradeOk = true

	if player.TradeOk && partner.TradeOk {
		s.executeTrade(player, partner)
	}
}

// Cancel 取消交易。
func (s *TradeSystem) Cancel(player *world.PlayerInfo) {
	partner := s.deps.World.GetByCharID(player.TradePartnerID)
	s.cancelTrade(player, partner)
}

// CancelIfActive 若玩家正在交易中則取消。
func (s *TradeSystem) CancelIfActive(player *world.PlayerInfo) {
	if player.TradePartnerID == 0 {
		return
	}
	partner := s.deps.World.GetByCharID(player.TradePartnerID)
	s.cancelTrade(player, partner)
}

// executeTrade 執行物品+金幣交換。物品已在 AddItem 時從來源扣除。
func (s *TradeSystem) executeTrade(p1, p2 *world.PlayerInfo) {
	// 建構 WAL 條目
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

	// 先寫入 WAL（安全保障）
	if len(walEntries) > 0 && s.deps.WALRepo != nil {
		ctx := context.Background()
		if err := s.deps.WALRepo.WriteWAL(ctx, walEntries); err != nil {
			s.deps.Log.Error("交易 WAL 寫入失敗，取消交易", zap.Error(err))
			s.cancelTrade(p1, p2)
			return
		}
	}

	// WAL 成功 — 物品已從來源扣除，現在加入接收方

	for _, item := range p1.TradeItems {
		s.addTradeItemToPlayer(p2, item)
	}
	for _, item := range p2.TradeItems {
		s.addTradeItemToPlayer(p1, item)
	}

	if p1.TradeGold > 0 {
		s.addGoldToPlayer(p2, p1.TradeGold)
	}
	if p2.TradeGold > 0 {
		s.addGoldToPlayer(p1, p2.TradeGold)
	}

	// 關閉交易視窗（0 = 交易完成）
	sendTradeStatus(p1.Session, 0)
	sendTradeStatus(p2.Session, 0)

	clearTradeState(p1)
	clearTradeState(p2)

	s.deps.Log.Info(fmt.Sprintf("交易完成  玩家1=%s  玩家2=%s", p1.Name, p2.Name))
}

// addTradeItemToPlayer 將交易物品加入接收方背包。
func (s *TradeSystem) addTradeItemToPlayer(receiver *world.PlayerInfo, item *world.InvItem) {
	itemInfo := s.deps.Items.Get(item.ItemID)
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

	newItem := receiver.Inv.AddItem(item.ItemID, item.Count, name, invGfx, weight, stackable, item.Bless)
	newItem.EnchantLvl = item.EnchantLvl
	if itemInfo != nil {
		newItem.UseType = itemInfo.UseTypeID
	}
	if wasExisting {
		handler.SendItemCountUpdate(receiver.Session, newItem)
	} else {
		handler.SendAddItem(receiver.Session, newItem)
	}
	handler.SendWeightUpdate(receiver.Session, receiver)
}

// addGoldToPlayer 將金幣加入接收方（來源已扣除）。
func (s *TradeSystem) addGoldToPlayer(receiver *world.PlayerInfo, amount int32) {
	adena := receiver.Inv.FindByItemID(world.AdenaItemID)
	if adena != nil {
		adena.Count += amount
		handler.SendItemCountUpdate(receiver.Session, adena)
	} else {
		newItem := receiver.Inv.AddItem(world.AdenaItemID, amount, "金幣", 0, 0, true, 1)
		handler.SendAddItem(receiver.Session, newItem)
	}
	handler.SendWeightUpdate(receiver.Session, receiver)
}

// cancelTrade 取消交易，歸還物品，清除狀態。
func (s *TradeSystem) cancelTrade(p1 *world.PlayerInfo, p2 *world.PlayerInfo) {
	s.restoreTradeItems(p1)
	if p1.TradeWindowOpen {
		sendTradeStatus(p1.Session, 1)
	}
	clearTradeState(p1)

	if p2 != nil {
		s.restoreTradeItems(p2)
		if p2.TradeWindowOpen {
			sendTradeStatus(p2.Session, 1)
		}
		clearTradeState(p2)
	}

	s.deps.Log.Debug("交易已取消", zap.String("player", p1.Name))
}

// restoreTradeItems 歸還已扣除的交易物品和金幣回玩家背包。
func (s *TradeSystem) restoreTradeItems(p *world.PlayerInfo) {
	for _, item := range p.TradeItems {
		itemInfo := s.deps.Items.Get(item.ItemID)
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

		newItem := p.Inv.AddItem(item.ItemID, item.Count, name, invGfx, weight, stackable, item.Bless)
		newItem.EnchantLvl = item.EnchantLvl
		if itemInfo != nil {
			newItem.UseType = itemInfo.UseTypeID
		}
		if wasExisting {
			handler.SendItemCountUpdate(p.Session, newItem)
		} else {
			handler.SendAddItem(p.Session, newItem)
		}
	}

	if p.TradeGold > 0 {
		adena := p.Inv.FindByItemID(world.AdenaItemID)
		if adena != nil {
			adena.Count += p.TradeGold
			handler.SendItemCountUpdate(p.Session, adena)
		} else {
			newItem := p.Inv.AddItem(world.AdenaItemID, p.TradeGold, "金幣", 0, 0, true, 1)
			handler.SendAddItem(p.Session, newItem)
		}
	}
	handler.SendWeightUpdate(p.Session, p)
}

// clearTradeState 重置所有交易相關欄位。
func clearTradeState(p *world.PlayerInfo) {
	p.TradePartnerID = 0
	p.TradeWindowOpen = false
	p.TradeOk = false
	p.TradeItems = nil
	p.TradeGold = 0
}

// --- 交易專用封包 ---

// sendTradeOpen 發送 S_TRADE (opcode 52) — 開啟交易視窗。
func sendTradeOpen(sess *net.Session, partnerName string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TRADE)
	w.WriteD(0)
	w.WriteS(partnerName)
	sess.Send(w.Bytes())
}

// sendTradeAddItem 發送 S_TRADEADDITEM (opcode 35) — 交易物品加入。
func sendTradeAddItem(sess *net.Session, gfxID uint16, viewName string, bless byte, panelType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TRADEADDITEM)
	w.WriteC(panelType)
	w.WriteH(gfxID)
	w.WriteS(viewName)
	w.WriteC(bless)
	w.WriteC(0)
	w.WriteH(0)
	sess.Send(w.Bytes())
}

// sendTradeStatus 發送 S_TRADESTATUS (opcode 112) — 交易狀態更新。
// 0=交易完成, 1=交易取消
func sendTradeStatus(sess *net.Session, status byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TRADESTATUS)
	w.WriteC(status)
	sess.Send(w.Bytes())
}
