package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
)

// Pet collar item IDs (amulets that store pet data).
const (
	petCollarNormal int32 = 40314
	petCollarHigher int32 = 40316
)

// isPetCollar returns true if the item is a pet collar (amulet).
func isPetCollar(itemID int32) bool {
	return itemID == petCollarNormal || itemID == petCollarHigher
}

// handleUsePetCollar processes using a pet collar item to summon a pet from DB.
// Java: C_ItemUSe → triggers L1PetInstance creation from stored pet data.
func handleUsePetCollar(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, deps *Deps) {
	ws := deps.World

	// Check if this collar already has a pet spawned
	if ws.GetPetByItemObjID(invItem.ObjectID) != nil {
		// Pet already out — collect it back (toggle behavior)
		pet := ws.GetPetByItemObjID(invItem.ObjectID)
		if pet != nil && pet.OwnerCharID == player.CharID {
			CollectPet(pet, player, deps)
		}
		return
	}

	// Check map allows pets
	if deps.MapData != nil {
		md := deps.MapData.GetInfo(player.MapID)
		if md != nil && !md.RecallPets {
			sendServerMessage(sess, 353) // "此處無法召喚。"
			return
		}
	}

	// CHA check
	usedCost := calcUsedPetCost(player.CharID, ws)
	availCHA := int(player.Cha) - usedCost
	if availCHA < 6 {
		sendServerMessage(sess, 319) // "你的魅力值不夠。"
		return
	}

	// Load pet from DB
	if deps.PetRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	petRow, err := deps.PetRepo.LoadByItemObjID(ctx, invItem.ObjectID)
	if err != nil || petRow == nil {
		// No pet data for this collar (empty collar or DB error)
		return
	}

	// Look up NPC template for the pet's current form
	tmpl := deps.Npcs.Get(petRow.NpcID)
	if tmpl == nil {
		return
	}

	// Look up pet type for level/stat info
	petType := deps.PetTypes.Get(petRow.NpcID)

	// Spawn position near master (±2 tiles)
	spawnX := player.X + int32(world.RandInt(5)) - 2
	spawnY := player.Y + int32(world.RandInt(5)) - 2

	// Compute MaxHP/MaxMP: use DB-stored values, fallback to template
	maxHP := petRow.MaxHP
	if maxHP <= 0 {
		maxHP = petRow.HP // legacy: before MaxHP column was added
	}
	if maxHP <= 0 {
		maxHP = tmpl.HP
	}
	maxMP := petRow.MaxMP
	if maxMP <= 0 {
		maxMP = petRow.MP
	}
	if maxMP <= 0 {
		maxMP = tmpl.MP
	}

	// Dead pet recovery: re-summoned pets always come back at full HP (Java behavior)
	hp := petRow.HP
	mp := petRow.MP
	if hp <= 0 {
		hp = maxHP
	}
	if mp <= 0 {
		mp = maxMP
	}

	pet := &world.PetInfo{
		ID:          world.NextNpcID(),
		OwnerCharID: player.CharID,
		ItemObjID:   invItem.ObjectID,
		NpcID:       petRow.NpcID,
		Name:        petRow.Name,
		Level:       petRow.Level,
		HP:          hp,
		MaxHP:       maxHP,
		MP:          mp,
		MaxMP:       maxMP,
		Exp:         petRow.Exp,
		Lawful:      petRow.Lawful,
		GfxID:       tmpl.GfxID,
		NameID:      tmpl.NameID,
		MoveSpeed:   tmpl.PassiveSpeed,
		X:           spawnX,
		Y:           spawnY,
		MapID:       player.MapID,
		Heading:     player.Heading,
		Status:      world.PetStatusRest,
		AC:          tmpl.AC,
		STR:         tmpl.STR,
		DEX:         tmpl.DEX,
		MR:          tmpl.MR,
		AtkDmg:      tmpl.HP / 4, // approximate base attack from HP
		AtkSpeed:    tmpl.AtkSpeed,
		Ranged:      tmpl.Ranged,
	}

	// Register in world
	ws.AddPet(pet)

	// Broadcast appearance to nearby players
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		SendPetPack(viewer.Session, pet, isOwner, player.Name)
	}

	// Send pet control panel to owner
	sendPetCtrlMenu(sess, pet, true)
	sendPetHpMeter(sess, pet.ID, pet.HP, pet.MaxHP)

	// Broadcast pet level message
	if petType != nil {
		msgID := petType.LevelUpMsgID(int(pet.Level))
		if msgID > 0 {
			broadcastNpcChat(ws, pet.ID, pet.X, pet.Y, pet.MapID, fmt.Sprintf("$%d", msgID))
		}
	}
}

// handlePetAction processes pet control commands from C_HACTION.
// Java: L1ActionPet.action() — handles all pet behavior commands.
func handlePetAction(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, action string, deps *Deps) {
	switch action {
	case "aggressive":
		changePetStatus(sess, player, pet, world.PetStatusAggressive, deps)
	case "defensive":
		changePetStatus(sess, player, pet, world.PetStatusDefensive, deps)
	case "stay":
		changePetStatus(sess, player, pet, world.PetStatusRest, deps)
		pet.AggroTarget = 0
		pet.AggroPlayerID = 0
	case "extend":
		changePetStatus(sess, player, pet, world.PetStatusExtend, deps)
	case "alert":
		if changePetStatus(sess, player, pet, world.PetStatusAlert, deps) {
			pet.HomeX = pet.X
			pet.HomeY = pet.Y
		}
	case "dismiss":
		DismissPet(pet, player, deps)
	case "attackchr":
		// Send targeting cursor to client
		sendSelectTarget(sess, pet.ID)
	case "getitem":
		collectPetItems(sess, player, pet, deps)
	case "changename":
		// Send yes/no dialog for name change (msg 325 = "寵物新名字是？")
		sendYesNoDialog(sess, 325, pet.Name)
	}
}

// changePetStatus changes a pet's AI status with level check.
// Java: master level must be >= pet level to change status (1-5), else defy message.
// Returns true if status was changed.
func changePetStatus(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, newStatus world.PetStatus, deps *Deps) bool {
	// Level check — master must be >= pet level to issue commands
	if player.Level < pet.Level {
		// Pet defies master — broadcast defy message
		petType := deps.PetTypes.Get(pet.NpcID)
		if petType != nil && petType.DefyMsgID > 0 {
			broadcastNpcChat(deps.World, pet.ID, pet.X, pet.Y, pet.MapID,
				fmt.Sprintf("$%d", petType.DefyMsgID))
		}
		return false
	}
	pet.Status = newStatus
	return true
}

// DismissPet liberates a pet (converts back to wild NPC).
// Java: L1PetInstance.liberate() — removes pet from world, deletes DB record,
// creates wild NPC at pet's position, removes collar from inventory.
func DismissPet(pet *world.PetInfo, player *world.PlayerInfo, deps *Deps) {
	ws := deps.World

	// Remove from world
	ws.RemovePet(pet.ID)

	// Broadcast removal
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		SendRemoveObject(viewer.Session, pet.ID)
	}

	// Close pet control panel
	sendPetCtrlMenu(player.Session, pet, false)

	// Convert to wild NPC at pet's position
	tmpl := deps.Npcs.Get(pet.NpcID)
	if tmpl != nil {
		npc := &world.NpcInfo{
			ID:        world.NextNpcID(),
			NpcID:     pet.NpcID,
			Impl:      tmpl.Impl,
			GfxID:     tmpl.GfxID,
			Name:      tmpl.Name,
			NameID:    tmpl.NameID,
			Level:     pet.Level,
			HP:        pet.HP,
			MaxHP:     pet.MaxHP,
			MP:        pet.MP,
			MaxMP:     pet.MaxMP,
			AC:        tmpl.AC,
			STR:       tmpl.STR,
			DEX:       tmpl.DEX,
			Exp:       tmpl.Exp,
			Lawful:    tmpl.Lawful,
			Size:      tmpl.Size,
			MR:        tmpl.MR,
			PoisonAtk: tmpl.PoisonAtk,
			X:         pet.X,
			Y:         pet.Y,
			MapID:     pet.MapID,
			SpawnX:    pet.X,
			SpawnY:    pet.Y,
			SpawnMapID: pet.MapID,
		}
		ws.AddNpc(npc)
		for _, viewer := range nearby {
			SendNpcPack(viewer.Session, npc)
		}
	}

	// Delete pet from DB
	if deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		deps.PetRepo.Delete(ctx, pet.ItemObjID)
		cancel()
	}

	// Remove collar from player inventory
	collarItem := player.Inv.FindByObjectID(pet.ItemObjID)
	if collarItem != nil {
		player.Inv.RemoveItem(pet.ItemObjID, 0) // count=0 → remove entire slot
		sendRemoveInvItem(player.Session, pet.ItemObjID)
	}
}

// CollectPet stores a pet back into its collar (save to DB, remove from world).
// Java: L1PetInstance.collect(false) — saves HP/MP/Exp/Level, despawns.
func CollectPet(pet *world.PetInfo, player *world.PlayerInfo, deps *Deps) {
	ws := deps.World

	// Save to DB
	if deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		deps.PetRepo.Save(ctx, &persist.PetRow{
			ItemObjID: pet.ItemObjID,
			ObjID:     pet.ID,
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

	// Remove from world
	ws.RemovePet(pet.ID)

	// Broadcast removal
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		SendRemoveObject(viewer.Session, pet.ID)
	}

	// Close pet control panel
	sendPetCtrlMenu(player.Session, pet, false)
}

// PetDie handles pet death: -5% exp penalty, death animation, tile release.
// Java: L1PetInstance.death() — broadcasts death animation, applies exp loss.
// The pet remains in-world (Dead=true) so master can collect the body.
func PetDie(pet *world.PetInfo, ws *world.State) {
	pet.Dead = true
	pet.HP = 0
	pet.AggroTarget = 0
	pet.AggroPlayerID = 0

	// -5% exp penalty
	penalty := pet.Exp / 20
	pet.Exp -= penalty
	if pet.Exp < 0 {
		pet.Exp = 0
	}
	pet.Dirty = true

	// Release tile (dead pet doesn't block movement)
	ws.PetDied(pet)

	// Broadcast death animation
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, pet.ID, 8) // ACTION_Die = 8
	}
}

// AddPetExp adds experience to a pet and handles level-up.
// Java: L1PetInstance.addExp — pet levels use same exp table as characters.
func AddPetExp(pet *world.PetInfo, expGain int32, deps *Deps) {
	if expGain <= 0 || deps.Scripting == nil {
		return
	}
	pet.Exp += expGain

	maxLevel := int16(50) // Java: pet max level = 50
	for {
		nextLevelExp := int32(deps.Scripting.ExpForLevel(int(pet.Level) + 1))
		if pet.Exp < nextLevelExp || pet.Level >= maxLevel {
			break
		}
		pet.Level++
		petType := deps.PetTypes.Get(pet.NpcID)
		if petType != nil {
			hpGain := petType.HPUpMin + world.RandInt(petType.HPUpMax-petType.HPUpMin+1)
			mpGain := petType.MPUpMin + world.RandInt(petType.MPUpMax-petType.MPUpMin+1)
			pet.MaxHP += int32(hpGain)
			pet.MaxMP += int32(mpGain)
		}
		pet.HP = pet.MaxHP
		pet.MP = pet.MaxMP
	}
	pet.Dirty = true
}

// petExpPercent computes the EXP progress percentage (0-100) for a pet.
// Uses the Lua exp_for_level table (same table as player characters).
func petExpPercent(pet *world.PetInfo, deps *Deps) int {
	if deps.Scripting == nil {
		return 0
	}
	expForCurrent := deps.Scripting.ExpForLevel(int(pet.Level))
	expForNext := deps.Scripting.ExpForLevel(int(pet.Level) + 1)
	if expForNext <= expForCurrent {
		return 100
	}
	pct := 100 * (int(pet.Exp) - expForCurrent) / (expForNext - expForCurrent)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// collectPetItems collects all items from pet back to master's inventory.
// Java: transfers all pet inventory items to master then refreshes pet inventory.
func collectPetItems(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, deps *Deps) {
	if len(pet.Items) == 0 {
		return
	}
	for _, petItem := range pet.Items {
		// Unequip first if equipped
		if petItem.Equipped {
			if petItem.IsWeapon {
				unequipPetWeapon(pet, deps)
			} else {
				unequipPetArmor(pet, deps)
			}
		}
		// Look up item template for weight/name/gfx
		name := petItem.Name
		gfx := petItem.GfxID
		var weight int32
		if deps.Items != nil {
			tmpl := deps.Items.Get(petItem.ItemID)
			if tmpl != nil {
				name = tmpl.Name
				gfx = tmpl.InvGfx
				weight = tmpl.Weight
			}
		}
		invItem := player.Inv.AddItemWithID(petItem.ObjectID, petItem.ItemID, petItem.Count,
			name, gfx, weight, false, petItem.Bless)
		sendAddItem(sess, invItem)
	}
	pet.Items = nil
	sendPetInventory(sess, pet)
}

// broadcastNpcChat sends an NPC chat message to nearby players.
func broadcastNpcChat(ws *world.State, npcID int32, x, y int32, mapID int16, msg string) {
	nearby := ws.GetNearbyPlayersAt(x, y, mapID)
	for _, viewer := range nearby {
		sendNpcChatPacket(viewer.Session, npcID, msg)
	}
}

// sendNpcChatPacket sends S_SAY (opcode 81) for an NPC message.
func sendNpcChatPacket(sess *net.Session, npcID int32, msg string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SAY)
	w.WriteD(npcID)
	w.WriteC(0x02) // type: NPC say
	w.WriteS(msg)
	sess.Send(w.Bytes())
}

// sendRemoveInvItem sends S_REMOVE_INVENTORY (opcode 57) to remove an item from client inventory.
func sendRemoveInvItem(sess *net.Session, objID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_INVENTORY)
	w.WriteD(objID)
	sess.Send(w.Bytes())
}

// handlePetNameChange processes the name change confirmation for a pet.
// Called from the yes/no dialog response handler when type = pet name change.
func handlePetNameChange(sess *net.Session, player *world.PlayerInfo, petID int32, newName string, deps *Deps) {
	pet := deps.World.GetPet(petID)
	if pet == nil || pet.OwnerCharID != player.CharID {
		return
	}
	newName = strings.TrimSpace(newName)
	if newName == "" || len(newName) > 16 {
		return
	}
	pet.Name = newName
	pet.Dirty = true

	// Re-broadcast updated appearance
	nearby := deps.World.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		SendPetPack(viewer.Session, pet, isOwner, player.Name)
	}
}
