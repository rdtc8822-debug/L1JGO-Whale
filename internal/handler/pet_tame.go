package handler

import (
	"context"
	"math"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
)

// HandleGiveItem processes C_GIVE (opcode 45).
// Java: C_GiveItem — give an item to an NPC, pet, or summon.
// Routes to: taming, pet equipment, pet evolution.
func HandleGiveItem(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetID := r.ReadD()
	_ = r.ReadH() // x (for distance check)
	_ = r.ReadH() // y (for distance check)
	itemObjID := r.ReadD()
	count := r.ReadD()
	if count <= 0 {
		count = 1
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Find the item in player inventory
	invItem := player.Inv.FindByObjectID(itemObjID)
	if invItem == nil || invItem.Count <= 0 {
		return
	}

	// Cannot give equipped items
	if invItem.Equipped {
		sendServerMessage(sess, 141) // 裝備使用中的東西不可以給予他人
		return
	}

	// Check if target is player's own pet
	if pet := deps.World.GetPet(targetID); pet != nil {
		if pet.OwnerCharID != player.CharID {
			return
		}
		handleGiveToPet(sess, player, pet, invItem, deps)
		return
	}

	// Check if target is a wild NPC (for taming)
	npc := deps.World.GetNpc(targetID)
	if npc == nil || npc.Dead {
		return
	}

	// Distance check (must be within 3 tiles)
	dx := int32(math.Abs(float64(player.X - npc.X)))
	dy := int32(math.Abs(float64(player.Y - npc.Y)))
	if dx >= 3 || dy >= 3 {
		sendServerMessage(sess, 142) // 太遠了
		return
	}

	// Check if this NPC + item combination is a taming attempt
	petType := deps.PetTypes.Get(npc.NpcID)
	if petType != nil && petType.CanTame() && invItem.ItemID == petType.TamingItemID {
		// Consume taming item
		player.Inv.RemoveItem(itemObjID, 1)
		sendRemoveInvItem(sess, itemObjID)

		handleTameNpc(sess, player, npc, deps)
	}
}

// handleGiveToPet handles giving an item to the player's pet.
// Routes to: pet equipment or pet evolution.
func handleGiveToPet(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, invItem *world.InvItem, deps *Deps) {
	petType := deps.PetTypes.Get(pet.NpcID)
	if petType == nil {
		return
	}

	// Check if item is an evolution item
	if petType.CanEvolve() && invItem.ItemID == petType.EvolvItemID {
		evolvePet(sess, player, pet, invItem, deps)
		return
	}

	// Check if item is pet equipment
	if deps.PetItems != nil {
		petItemInfo := deps.PetItems.Get(invItem.ItemID)
		if petItemInfo != nil {
			// Check if this pet can equip items
			if !petType.CanEquip {
				return
			}
			if petNoEquipNpcIDs[pet.NpcID] {
				return
			}

			// Transfer item from player to pet
			player.Inv.RemoveItem(invItem.ObjectID, 1)
			sendRemoveInvItem(sess, invItem.ObjectID)

			pet.Items = append(pet.Items, &world.PetInvItem{
				ItemID:   invItem.ItemID,
				ObjectID: invItem.ObjectID,
				Name:     invItem.Name,
				GfxID:    invItem.InvGfx,
				Count:    1,
				Equipped: false,
				IsWeapon: petItemInfo.IsWeapon(),
				Bless:    invItem.Bless,
			})
			sendPetInventory(sess, pet)
		}
	}
}

// handleTameNpc attempts to tame a wild NPC.
// Java: C_GiveItem.tamePet — NPC must be below 1/3 HP, creates collar + DB record.
func handleTameNpc(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, deps *Deps) {
	ws := deps.World
	tmpl := deps.Npcs.Get(npc.NpcID)
	if tmpl == nil || !tmpl.Tameable {
		sendServerMessage(sess, 324) // 馴養失敗
		return
	}

	// Java: NPC must be below 1/3 max HP to be tamed
	if npc.HP > npc.MaxHP/3 {
		sendServerMessage(sess, 324) // 馴養失敗
		return
	}

	// Special case: Tiger Man (45313) has 1/16 chance even below 1/3 HP
	if npc.NpcID == 45313 {
		if world.RandInt(16) != 15 {
			sendServerMessage(sess, 324) // 馴養失敗
			return
		}
	}

	// CHA check
	usedCost := calcUsedPetCost(player.CharID, ws)
	availCHA := int(player.Cha) - usedCost
	if availCHA < 6 {
		sendServerMessage(sess, 489) // 你無法一次控制那麼多寵物
		return
	}

	// Inventory space check
	if player.Inv.Size() >= 180 {
		return
	}

	// Remove the wild NPC (broadcast before removing so nearby list is correct)
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	ws.RemoveNpc(npc.ID)
	for _, viewer := range nearby {
		SendRemoveObject(viewer.Session, npc.ID)
	}

	// Create collar item in player inventory
	var collarInfo *world.InvItem
	if deps.Items != nil {
		collarTmpl := deps.Items.Get(petCollarNormal)
		if collarTmpl != nil {
			collarInfo = player.Inv.AddItem(petCollarNormal, 1,
				collarTmpl.Name, collarTmpl.InvGfx, collarTmpl.Weight,
				false, 0)
			sendAddItem(sess, collarInfo)
		}
	}
	if collarInfo == nil {
		return
	}

	// Save pet to DB
	petType := deps.PetTypes.Get(npc.NpcID)
	petName := npc.Name
	if petType != nil && petType.Name != "" {
		petName = petType.Name
	}

	if deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		deps.PetRepo.Save(ctx, &persist.PetRow{
			ItemObjID: collarInfo.ObjectID,
			NpcID:     npc.NpcID,
			Name:      petName,
			Level:     npc.Level,
			HP:        npc.MaxHP,
			MaxHP:     npc.MaxHP,
			MP:        npc.MaxMP,
			MaxMP:     npc.MaxMP,
			Exp:       750, // Java default starting exp for tamed pets
			Lawful:    0,
		})
		cancel()
	}
}

// evolvePet processes pet evolution when given the evolution item.
// Java: L1PetInstance.evolvePet — transforms pet, halves stats, resets level.
func evolvePet(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, invItem *world.InvItem, deps *Deps) {
	ws := deps.World

	// Level check: must be >= 30
	if pet.Level < 30 {
		return
	}

	petType := deps.PetTypes.Get(pet.NpcID)
	if petType == nil || !petType.CanEvolve() {
		return
	}

	// Look up new NPC template
	newTmpl := deps.Npcs.Get(petType.EvolvNpcID)
	if newTmpl == nil {
		return
	}

	// Consume evolution item from player inventory
	player.Inv.RemoveItem(invItem.ObjectID, 1)
	sendRemoveInvItem(sess, invItem.ObjectID)

	// Remove old collar from player inventory
	oldCollarItem := player.Inv.FindByObjectID(pet.ItemObjID)
	if oldCollarItem != nil {
		player.Inv.RemoveItem(pet.ItemObjID, 0) // count=0 → remove entire slot
		sendRemoveInvItem(sess, pet.ItemObjID)
	}

	// Create higher-tier collar
	var newCollar *world.InvItem
	if deps.Items != nil {
		collarTmpl := deps.Items.Get(petCollarHigher)
		if collarTmpl != nil {
			newCollar = player.Inv.AddItem(petCollarHigher, 1,
				collarTmpl.Name, collarTmpl.InvGfx, collarTmpl.Weight,
				false, 0)
			sendAddItem(sess, newCollar)
		}
	}
	if newCollar == nil {
		return
	}

	// Delete old DB record
	if deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		deps.PetRepo.Delete(ctx, pet.ItemObjID)
		cancel()
	}

	// Broadcast removal of old appearance
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		SendRemoveObject(viewer.Session, pet.ID)
	}

	// Transform pet stats — Java: maxHP/2, maxMP/2, level=1, exp=0
	pet.NpcID = petType.EvolvNpcID
	pet.GfxID = newTmpl.GfxID
	pet.NameID = newTmpl.NameID
	pet.MaxHP = pet.MaxHP / 2
	if pet.MaxHP < 1 {
		pet.MaxHP = 1
	}
	pet.MaxMP = pet.MaxMP / 2
	pet.HP = pet.MaxHP
	pet.MP = pet.MaxMP
	pet.Level = 1
	pet.Exp = 0
	pet.ItemObjID = newCollar.ObjectID
	pet.AC = newTmpl.AC
	pet.STR = newTmpl.STR
	pet.DEX = newTmpl.DEX
	pet.MR = newTmpl.MR
	pet.AtkDmg = newTmpl.HP / 4
	pet.MoveSpeed = newTmpl.PassiveSpeed
	pet.Dirty = true

	// Unequip all pet equipment (stats change on evolution)
	if pet.WeaponObjID != 0 {
		unequipPetWeapon(pet, deps)
	}
	if pet.ArmorObjID != 0 {
		unequipPetArmor(pet, deps)
	}

	// Save new DB record with new collar object ID
	if deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		deps.PetRepo.Save(ctx, &persist.PetRow{
			ItemObjID: newCollar.ObjectID,
			NpcID:     pet.NpcID,
			Name:      pet.Name,
			Level:     pet.Level,
			HP:        pet.HP,
			MaxHP:     pet.MaxHP,
			MP:        pet.MP,
			MaxMP:     pet.MaxMP,
			Exp:       pet.Exp,
			Lawful:    pet.Lawful,
		})
		cancel()
	}

	// Broadcast new appearance + evolution effect
	nearby = ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		SendPetPack(viewer.Session, pet, isOwner, player.Name)
		sendCompanionEffect(viewer.Session, pet.ID, 2127) // level-up glow
	}

	// Refresh pet control panel
	sendPetCtrlMenu(sess, pet, true)
	sendPetHpMeter(sess, pet.ID, pet.HP, pet.MaxHP)
}
