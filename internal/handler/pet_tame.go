package handler

import (
	"math"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
)

// HandleGiveItem processes C_GIVE (opcode 45).
// Java: C_GiveItem — give an item to an NPC, pet, or summon.
// Routes to: taming, pet equipment, pet evolution.
func HandleGiveItem(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetID := r.ReadD()
	_ = r.ReadH() // x
	_ = r.ReadH() // y
	itemObjID := r.ReadD()
	count := r.ReadD()
	if count <= 0 {
		count = 1
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	invItem := player.Inv.FindByObjectID(itemObjID)
	if invItem == nil || invItem.Count <= 0 {
		return
	}

	// 已裝備的物品不可給予
	if invItem.Equipped {
		sendServerMessage(sess, 141) // 裝備使用中的東西不可以給予他人
		return
	}

	// 目標是自己的寵物 → 委派給 PetLife
	if pet := deps.World.GetPet(targetID); pet != nil {
		if pet.OwnerCharID == player.CharID && deps.PetLife != nil {
			deps.PetLife.GiveToPet(sess, player, pet, invItem)
		}
		return
	}

	// 目標是野生 NPC → 馴服嘗試
	npc := deps.World.GetNpc(targetID)
	if npc == nil || npc.Dead {
		return
	}

	// 距離檢查（3 格內）
	dx := int32(math.Abs(float64(player.X - npc.X)))
	dy := int32(math.Abs(float64(player.Y - npc.Y)))
	if dx >= 3 || dy >= 3 {
		sendServerMessage(sess, 142) // 太遠了
		return
	}

	// 檢查 NPC + 物品組合是否為馴服嘗試
	petType := deps.PetTypes.Get(npc.NpcID)
	if petType != nil && petType.CanTame() && invItem.ItemID == petType.TamingItemID {
		// 消耗馴服物品
		player.Inv.RemoveItem(itemObjID, 1)
		sendRemoveInventoryItem(sess, itemObjID)

		if deps.PetLife != nil {
			deps.PetLife.TameNpc(sess, player, npc)
		}
	}
}
