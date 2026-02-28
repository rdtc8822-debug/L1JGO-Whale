package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
)

// HandlePetMenu processes C_PETMENU (opcode 103).
// Java: C_PetMenu — client requests to view pet inventory/equipment.
func HandlePetMenu(sess *net.Session, r *packet.Reader, deps *Deps) {
	petID := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	pet := deps.World.GetPet(petID)
	if pet == nil || pet.OwnerCharID != player.CharID {
		return
	}

	sendPetInventory(sess, pet)
}

// HandleUsePetItem processes C_USEPETITEM (opcode 104).
// Java: C_UsePetItem — toggle equip/unequip pet weapon or armor.
func HandleUsePetItem(sess *net.Session, r *packet.Reader, deps *Deps) {
	_ = r.ReadC()      // data byte (always 0x00)
	petID := r.ReadD()  // pet object ID
	listNo := r.ReadC() // item index in pet's inventory

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	pet := deps.World.GetPet(petID)
	if pet == nil || pet.OwnerCharID != player.CharID {
		return
	}

	if deps.PetLife != nil {
		deps.PetLife.UsePetItem(sess, pet, int(listNo))
	}
}
