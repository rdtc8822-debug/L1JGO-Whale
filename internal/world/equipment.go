package world

// EquipSlot identifies an equipment slot on a character.
type EquipSlot int

const (
	SlotNone    EquipSlot = 0
	SlotHelm    EquipSlot = 1
	SlotArmor   EquipSlot = 2
	SlotGlove   EquipSlot = 3
	SlotBoots   EquipSlot = 4
	SlotShield  EquipSlot = 5
	SlotCloak   EquipSlot = 6
	SlotRing1   EquipSlot = 7
	SlotRing2   EquipSlot = 8
	SlotAmulet  EquipSlot = 9
	SlotBelt    EquipSlot = 10
	SlotWeapon  EquipSlot = 11
	SlotEarring EquipSlot = 12
	SlotGuarder EquipSlot = 13
	SlotTShirt  EquipSlot = 14
	SlotMax     EquipSlot = 15
)

// Equipment tracks what a player currently has equipped.
// Each slot holds a pointer to an InvItem (nil = empty).
type Equipment struct {
	Slots [SlotMax]*InvItem
}

// Get returns the item in a slot, or nil.
func (e *Equipment) Get(slot EquipSlot) *InvItem {
	if slot <= SlotNone || slot >= SlotMax {
		return nil
	}
	return e.Slots[slot]
}

// Set places an item in a slot (or nil to clear).
func (e *Equipment) Set(slot EquipSlot, item *InvItem) {
	if slot > SlotNone && slot < SlotMax {
		e.Slots[slot] = item
	}
}

// Weapon returns the currently equipped weapon, or nil.
func (e *Equipment) Weapon() *InvItem {
	return e.Slots[SlotWeapon]
}

// ArmorSlotFromType maps an armor type string (from YAML) to an EquipSlot.
func ArmorSlotFromType(armorType string) EquipSlot {
	switch armorType {
	case "helm":
		return SlotHelm
	case "armor":
		return SlotArmor
	case "T", "t_shirts":
		return SlotTShirt
	case "cloak":
		return SlotCloak
	case "glove":
		return SlotGlove
	case "boots":
		return SlotBoots
	case "shield":
		return SlotShield
	case "guarder":
		return SlotGuarder
	case "ring":
		return SlotRing1 // caller should check Ring1 vs Ring2
	case "amulet", "necklace":
		return SlotAmulet
	case "earring":
		return SlotEarring
	case "belt":
		return SlotBelt
	default:
		return SlotNone
	}
}

// IsTwoHanded returns true for weapon types that use both hands (no shield).
func IsTwoHanded(weaponType string) bool {
	switch weaponType {
	case "tohandsword", "tohandblunt", "bow", "claw", "edoryu":
		return true
	}
	return false
}

// WeaponVisualID maps a weapon type string to the client's visual animation byte.
// This byte is sent in S_CHANGE_DESC (opcode 119) and S_PUT_OBJECT (opcode 87).
func WeaponVisualID(weaponType string) byte {
	switch weaponType {
	case "sword":
		return 4
	case "dagger":
		return 46
	case "tohandsword":
		return 50
	case "bow":
		return 20
	case "spear", "singlespear":
		return 24
	case "blunt", "tohandblunt":
		return 24
	case "staff":
		return 40
	case "claw":
		return 58
	case "edoryu":
		return 54
	default:
		return 0 // no weapon / fist
	}
}

// EquipStats holds the cumulative stat bonuses from all equipped items.
type EquipStats struct {
	AC        int
	HitMod    int
	DmgMod    int
	BowHitMod int
	BowDmgMod int
	AddStr    int
	AddDex    int
	AddCon    int
	AddInt    int
	AddWis    int
	AddCha    int
	AddHP     int
	AddMP     int
	AddHPR    int
	AddMPR    int
	AddSP     int
	MDef      int
}

// IsAccessorySlot returns true for slots where enchant level does NOT affect AC.
// Java: armor type 8-12 (amulet, ring, guarder/bracer, earring) are accessories.
func IsAccessorySlot(slot EquipSlot) bool {
	switch slot {
	case SlotAmulet, SlotRing1, SlotRing2, SlotGuarder, SlotEarring:
		return true
	}
	return false
}

// EquipClientIndex maps a Go EquipSlot to the client's equipment index byte.
// Used in S_EquipmentSlot packet (opcode 64). Matches Java L1PcInventory.setEquipmentOnOff().
func EquipClientIndex(slot EquipSlot) byte {
	switch slot {
	case SlotHelm:
		return 1
	case SlotArmor:
		return 2
	case SlotTShirt:
		return 3
	case SlotCloak:
		return 4
	case SlotGlove:
		return 6 // Java: type 5 (glove) → index 6
	case SlotBoots:
		return 5 // Java: type 6 (boots) → index 5
	case SlotShield:
		return 7
	case SlotBelt:
		return 11 // Java: type 10 (belt) → index 11
	case SlotWeapon:
		return 8
	case SlotAmulet:
		return 10
	case SlotGuarder:
		return 7 // Java: type 13 (guarder) → index 7, mutually exclusive with shield
	case SlotEarring:
		return 12
	case SlotRing1:
		return 18
	case SlotRing2:
		return 19 // Java: type 11 (ring2) → index 19
	}
	return 0
}
