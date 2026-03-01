package system

import (
	"fmt"
	"math"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

// CraftSystem 負責 NPC 製作業務邏輯（材料驗證、消耗、生產物品）。
// 實作 handler.CraftManager 介面。
type CraftSystem struct {
	deps *handler.Deps
}

// NewCraftSystem 建立製作系統。
func NewCraftSystem(deps *handler.Deps) *CraftSystem {
	return &CraftSystem{deps: deps}
}

// HandleCraftEntry 製作入口：檢查 NPC 限制、計算可製作套數、顯示批量對話或直接製作。
// Java: L1NpcMakeItemAction.execute()
func (s *CraftSystem) HandleCraftEntry(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, recipe *data.CraftRecipe, action string) {
	// NPC 限制：recipe.NpcID == 0 表示任意 NPC
	if recipe.NpcID != 0 && recipe.NpcID != npc.NpcID {
		return
	}

	// 計算可製作套數
	sets := countMaterialSets(player.Inv, recipe.Materials)
	if sets <= 0 {
		// 回報第一個不足的材料（Java: msg 337 + 物品名 + 缺少數量）
		for _, mat := range recipe.Materials {
			have := countUnequippedByID(player.Inv, mat.ItemID)
			if have < mat.Amount {
				shortage := mat.Amount - have
				itemInfo := s.deps.Items.Get(mat.ItemID)
				name := fmt.Sprintf("item#%d", mat.ItemID)
				if itemInfo != nil {
					name = itemInfo.Name
				}
				handler.SendServerMessageArgs(sess, 337, name, fmt.Sprintf("%d", shortage))
				return
			}
		}
		return
	}

	// 多套材料且配方支援批量輸入 → 顯示 spinner 對話框
	if sets > 1 && recipe.AmountInputable {
		handler.SendInputAmount(sess, npc.ID, sets, action)
		player.PendingCraftAction = action
		return
	}

	// 單次製作
	s.ExecuteCraft(sess, player, npc, recipe, 1)
}

// ExecuteCraft 執行製作：驗證材料、消耗、生產物品。
// Java: L1NpcMakeItemAction.makeItems()
func (s *CraftSystem) ExecuteCraft(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, recipe *data.CraftRecipe, amount int32) {
	if amount <= 0 {
		return
	}

	// 1. 材料檢查 — 驗證所有材料是否足夠（僅計算未裝備的）
	for _, mat := range recipe.Materials {
		have := countUnequippedByID(player.Inv, mat.ItemID)
		need := mat.Amount * amount
		if have < need {
			shortage := need - have
			itemInfo := s.deps.Items.Get(mat.ItemID)
			name := fmt.Sprintf("item#%d", mat.ItemID)
			if itemInfo != nil {
				name = itemInfo.Name
			}
			// msg 337: "%0が%1個不足しています"（不足：%0 還差 %1 個）
			handler.SendServerMessageArgs(sess, 337, name, fmt.Sprintf("%d", shortage))
			return
		}
	}

	// 2. 背包空間檢查（最多 180 格）
	newSlots := 0
	for _, out := range recipe.Items {
		outInfo := s.deps.Items.Get(out.ItemID)
		if outInfo != nil && outInfo.Stackable {
			existing := player.Inv.FindByItemID(out.ItemID)
			if existing == nil {
				newSlots++ // 新堆疊
			}
		} else {
			// 不可堆疊：每個物品佔一格
			newSlots += int(out.Amount) * int(amount)
		}
	}
	if player.Inv.Size()+newSlots > world.MaxInventorySize {
		// msg 263: "持有物品過多"
		handler.SendServerMessage(sess, 263)
		return
	}

	// 3. 負重檢查
	var addWeight int32
	for _, out := range recipe.Items {
		outInfo := s.deps.Items.Get(out.ItemID)
		if outInfo != nil {
			addWeight += outInfo.Weight * out.Amount * amount
		}
	}
	maxW := world.PlayerMaxWeight(player)
	if player.Inv.IsOverWeight(addWeight, maxW) {
		// msg 82: "超過角色可攜帶的物品重量"
		handler.SendServerMessage(sess, 82)
		return
	}

	// 4. 消耗材料
	for _, mat := range recipe.Materials {
		remaining := mat.Amount * amount
		for remaining > 0 {
			slot := findUnequippedByID(player.Inv, mat.ItemID)
			if slot == nil {
				break // 不應發生 — 上面已檢查
			}
			take := remaining
			if take > slot.Count {
				take = slot.Count
			}
			removed := player.Inv.RemoveItem(slot.ObjectID, take)
			if removed {
				handler.SendRemoveInventoryItem(sess, slot.ObjectID)
			} else {
				handler.SendItemCountUpdate(sess, slot)
			}
			remaining -= take
		}
	}

	// 5. 生產物品
	npcName := ""
	if npc != nil {
		npcInfo := s.deps.Npcs.Get(npc.NpcID)
		if npcInfo != nil {
			npcName = npcInfo.Name
		}
	}

	for _, out := range recipe.Items {
		outInfo := s.deps.Items.Get(out.ItemID)
		if outInfo == nil {
			continue
		}
		totalCount := out.Amount * amount

		if outInfo.Stackable {
			item := player.Inv.AddItem(out.ItemID, totalCount, outInfo.Name,
				outInfo.InvGfx, outInfo.Weight, true, byte(outInfo.Bless))
			item.UseType = data.UseTypeToID(outInfo.UseType)
			handler.SendAddItem(sess, item, outInfo)
		} else {
			for i := int32(0); i < totalCount; i++ {
				item := player.Inv.AddItem(out.ItemID, 1, outInfo.Name,
					outInfo.InvGfx, outInfo.Weight, false, byte(outInfo.Bless))
				item.UseType = data.UseTypeToID(outInfo.UseType)
				handler.SendAddItem(sess, item, outInfo)
			}
		}

		// msg 143: "%0 給了你 %1"（[NPC] 給了你 [物品]）
		if npcName != "" {
			handler.SendServerMessageArgs(sess, 143, npcName, outInfo.Name)
		}
	}

	handler.SendWeightUpdate(sess, player)

	s.deps.Log.Info(fmt.Sprintf("製作完成  角色=%s  配方=%s  數量=%d",
		player.Name, recipe.Action, amount))
}

// --- 私有輔助函式 ---

// countMaterialSets 計算玩家可提供幾套完整材料。
// 僅計算未裝備的物品。材料不足則回傳 0。
func countMaterialSets(inv *world.Inventory, materials []data.CraftMaterial) int32 {
	if len(materials) == 0 {
		return 0
	}
	var minSets int32 = math.MaxInt32
	for _, mat := range materials {
		have := countUnequippedByID(inv, mat.ItemID)
		if mat.Amount <= 0 {
			continue
		}
		sets := have / mat.Amount
		if sets < minSets {
			minSets = sets
		}
	}
	if minSets == math.MaxInt32 {
		return 0
	}
	return minSets
}

// countUnequippedByID 計算未裝備的指定物品總數量。
func countUnequippedByID(inv *world.Inventory, itemID int32) int32 {
	var total int32
	for _, it := range inv.Items {
		if it.ItemID == itemID && !it.Equipped {
			total += it.Count
		}
	}
	return total
}

// findUnequippedByID 找到第一個未裝備的指定物品。
func findUnequippedByID(inv *world.Inventory, itemID int32) *world.InvItem {
	for _, it := range inv.Items {
		if it.ItemID == itemID && !it.Equipped {
			return it
		}
	}
	return nil
}
