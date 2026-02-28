package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// ========================================================================
//  Summon packets — Java: S_NPCPack_Summon
// ========================================================================

// SendSummonPack sends S_PUT_OBJECT (opcode 87) for a summon to the viewer.
// Protocol matches Java S_NPCPack_Summon exactly.
// HP percentage is shown only to the summon's master (others see 0xFF = unknown).
func SendSummonPack(viewer *net.Session, sum *world.SummonInfo, viewerIsOwner bool, masterName string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(sum.X))
	w.WriteH(uint16(sum.Y))
	w.WriteD(sum.ID)
	w.WriteH(uint16(sum.GfxID))
	w.WriteC(0)                   // status (action sprite)
	w.WriteC(byte(sum.Heading))
	w.WriteC(0)                   // light size
	w.WriteC(0)                   // move speed (0=normal)
	w.WriteD(0)                   // exp (always 0)
	w.WriteH(0)                   // lawful (always 0)
	w.WriteS(sum.NameID)          // name string key
	w.WriteS("")                  // title (empty for summons)

	// Status flags — 1 if poisoned (not implemented yet, always 0)
	w.WriteC(0x00)

	w.WriteD(0)    // reserved
	w.WriteS("")   // reserved string

	// Master name — shown under the summon's name
	w.WriteS(masterName)

	w.WriteC(0x00) // object classification

	// HP percentage: owner sees actual %, others see 0xFF (unknown)
	if viewerIsOwner {
		hp := byte(0xFF)
		if sum.MaxHP > 0 {
			hp = byte(sum.HP * 100 / sum.MaxHP)
		}
		w.WriteC(hp)
	} else {
		w.WriteC(0xFF)
	}

	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0xFF)
	w.WriteC(0xFF)

	viewer.Send(w.Bytes())
}

// sendSummonMenu sends S_HYPERTEXT (opcode 39) with the summon control menu.
// Java: S_PetMenuPacket with htmlID = "moncom" and 6 parameter strings.
func sendSummonMenu(sess *net.Session, sum *world.SummonInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(sum.ID)
	w.WriteS("moncom")
	w.WriteC(0x00)
	w.WriteH(0x06) // 6 parameter strings

	// Parameter 1: current status text
	switch sum.Status {
	case world.SummonAggressive:
		w.WriteS("$469") // 攻擊態勢
	case world.SummonDefensive:
		w.WriteS("$470") // 防禦態勢
	case world.SummonAlert:
		w.WriteS("$472") // 警戒
	default:
		w.WriteS("$471") // 休憩 (default)
	}

	// Parameters 2-6: HP, MaxHP, MP, MaxMP, Level
	w.WriteS(fmt.Sprintf("%d", sum.HP))
	w.WriteS(fmt.Sprintf("%d", sum.MaxHP))
	w.WriteS(fmt.Sprintf("%d", sum.MP))
	w.WriteS(fmt.Sprintf("%d", sum.MaxMP))
	w.WriteS(fmt.Sprintf("%d", sum.Level))

	sess.Send(w.Bytes())
}

// SendSummonMenu 發送召喚獸控制選單。Exported for system package usage.
func SendSummonMenu(sess *net.Session, sum *world.SummonInfo) {
	sendSummonMenu(sess, sum)
}

// sendSummonHpMeter sends S_HP_METER (opcode 237) for a summon — only to master.
// Java: S_HPMeter only sent to the summon's owner.
func sendSummonHpMeter(sess *net.Session, summonID int32, hp, maxHP int32) {
	ratio := int16(0xFF)
	if maxHP > 0 {
		ratio = int16(hp * 100 / maxHP)
		if ratio > 100 {
			ratio = 100
		}
	}
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HP_METER)
	w.WriteD(summonID)
	w.WriteC(byte(ratio))
	sess.Send(w.Bytes())
}

// ========================================================================
//  Doll packets — Java: S_NPCPack_Doll
// ========================================================================

// SendDollPack sends S_PUT_OBJECT (opcode 87) for a doll to the viewer.
// Protocol matches Java S_NPCPack_Doll exactly.
// Dolls always show HP as 0xFF (unknown to everyone).
func SendDollPack(viewer *net.Session, doll *world.DollInfo, masterName string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(doll.X))
	w.WriteH(uint16(doll.Y))
	w.WriteD(doll.ID)
	w.WriteH(uint16(doll.GfxID))
	w.WriteC(0)                   // status
	w.WriteC(byte(doll.Heading))
	w.WriteC(0)                   // light size (always 0 for dolls)
	w.WriteC(0)                   // move speed
	w.WriteD(0)                   // exp (always 0)
	w.WriteH(0)                   // lawful (always 0)
	w.WriteS(doll.NameID)         // name
	w.WriteS("")                  // title
	w.WriteC(0x00)                // status flags (always 0 for dolls)
	w.WriteD(0)                   // reserved
	w.WriteS("")                  // reserved string
	w.WriteS(masterName)          // master name
	w.WriteC(0x00)                // object classification
	w.WriteC(0xFF)                // HP (always unknown)
	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0xFF)
	w.WriteC(0xFF)

	viewer.Send(w.Bytes())
}

// sendDollTimer sends S_PacketBox (opcode 250) subtype 56 — doll duration timer UI.
// Java: S_PacketBox(56, remaining_seconds). Send 0 to clear.
func sendDollTimer(sess *net.Session, seconds int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteH(56)       // subtype 56 = doll timer
	w.WriteD(seconds)  // remaining seconds (0 = clear)
	sess.Send(w.Bytes())
}

// ========================================================================
//  Follower packets — Java: S_FollowerPack
// ========================================================================

// SendFollowerPack sends S_PUT_OBJECT (opcode 87) for a follower to the viewer.
// Protocol matches Java S_FollowerPack exactly.
// Followers show no master name and HP is always 0xFF.
func SendFollowerPack(viewer *net.Session, f *world.FollowerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(f.X))
	w.WriteH(uint16(f.Y))
	w.WriteD(f.ID)
	w.WriteH(uint16(f.GfxID))
	w.WriteC(0)                   // status
	w.WriteC(byte(f.Heading))
	w.WriteC(0)                   // light size
	w.WriteC(0)                   // move speed
	w.WriteD(0)                   // exp (always 0)
	w.WriteH(0)                   // lawful (always 0)
	w.WriteS(f.NameID)            // name
	w.WriteS("")                  // title
	w.WriteC(0x00)                // status flags
	w.WriteD(0)                   // reserved
	w.WriteS("")                  // reserved string 1
	w.WriteS("")                  // reserved string 2 (no master name for followers)
	w.WriteC(0x00)                // object classification
	w.WriteC(0xFF)                // HP (always unknown)
	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0xFF)
	w.WriteC(0xFF)

	viewer.Send(w.Bytes())
}

// ========================================================================
//  Pet packets — Java: S_NPCPack_Pet, S_PetMenuPacket, S_PetCtrlMenu,
//                       S_PetInventory, S_PetEquipment, S_PetList,
//                       S_SelectTarget
// ========================================================================

// SendPetPack sends S_PUT_OBJECT (opcode 87) for a pet to the viewer.
// Protocol matches Java S_NPCPack_Pet — includes exp and lawful fields
// (unlike summons which always write 0 for these).
func SendPetPack(viewer *net.Session, pet *world.PetInfo, viewerIsOwner bool, masterName string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(pet.X))
	w.WriteH(uint16(pet.Y))
	w.WriteD(pet.ID)
	w.WriteH(uint16(pet.GfxID))

	status := byte(0)
	if pet.Dead {
		status = 8
	}
	w.WriteC(status)                  // action status
	w.WriteC(byte(pet.Heading))
	w.WriteC(0)                       // light size
	w.WriteC(byte(pet.MoveSpeed))     // move speed
	w.WriteD(pet.Exp)                 // experience (pet-specific)
	w.WriteH(uint16(pet.Lawful))      // lawful (pet-specific)
	w.WriteS(pet.Name)                // pet name (custom)
	w.WriteS("")                      // title (empty)
	w.WriteC(0x00)                    // status flags (poison etc.)
	w.WriteD(0)                       // reserved
	w.WriteS("")                      // reserved string
	w.WriteS(masterName)              // master display name

	w.WriteC(0x00) // object classification

	// HP percentage: owner sees actual %, others see 0xFF
	if viewerIsOwner {
		hp := byte(0xFF)
		if pet.MaxHP > 0 {
			hp = byte(pet.HP * 100 / pet.MaxHP)
		}
		w.WriteC(hp)
	} else {
		w.WriteC(0xFF)
	}

	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0x00)
	w.WriteC(0xFF)
	w.WriteC(0xFF)

	viewer.Send(w.Bytes())
}

// sendPetMenu sends S_HYPERTEXT (opcode 39) with the pet control menu.
// Java: S_PetMenuPacket with htmlID = "anicom" and 10 parameter strings.
// Different from summon menu ("moncom") — pets have more stats displayed.
func sendPetMenu(sess *net.Session, pet *world.PetInfo, expPercent int) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(pet.ID)
	w.WriteS("anicom") // pet dialog (vs "moncom" for summons)
	w.WriteC(0x00)
	w.WriteH(0x0a) // 10 parameter strings

	// Parameter 1: current status text
	switch pet.Status {
	case world.PetStatusAggressive:
		w.WriteS("$469") // 攻擊態勢
	case world.PetStatusDefensive:
		w.WriteS("$470") // 防禦態勢
	case world.PetStatusAlert:
		w.WriteS("$472") // 警戒
	default:
		w.WriteS("$471") // 休憩 (default for rest/extend/whistle)
	}

	// Parameters 2-6: HP, MaxHP, MP, MaxMP, Level
	w.WriteS(fmt.Sprintf("%d", pet.HP))
	w.WriteS(fmt.Sprintf("%d", pet.MaxHP))
	w.WriteS(fmt.Sprintf("%d", pet.MP))
	w.WriteS(fmt.Sprintf("%d", pet.MaxMP))
	w.WriteS(fmt.Sprintf("%d", pet.Level))

	// Parameter 7: pet name
	w.WriteS(pet.Name)

	// Parameter 8: "$611" (satiation label — fixed by client)
	w.WriteS("$611")

	// Parameter 9: experience percentage (0-100)
	w.WriteS(fmt.Sprintf("%d", expPercent))

	// Parameter 10: lawful value
	w.WriteS(fmt.Sprintf("%d", pet.Lawful))

	sess.Send(w.Bytes())
}

// SendPetHpMeter 匯出 sendPetHpMeter — 供 system 套件發送寵物 HP 條更新。
func SendPetHpMeter(sess *net.Session, petID int32, hp, maxHP int32) {
	sendPetHpMeter(sess, petID, hp, maxHP)
}

// sendPetHpMeter sends S_HP_METER (opcode 237) for a pet — only to master.
func sendPetHpMeter(sess *net.Session, petID int32, hp, maxHP int32) {
	ratio := byte(0xFF)
	if maxHP > 0 {
		r := hp * 100 / maxHP
		if r > 100 {
			r = 100
		}
		ratio = byte(r)
	}
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HP_METER)
	w.WriteD(petID)
	w.WriteC(ratio)
	sess.Send(w.Bytes())
}

// sendPetCtrlMenu sends S_PetCtrlMenu (opcode 64) to open/close the pet control panel UI.
// Java: S_PetCtrlMenu uses S_OPCODE_CHARRESET.
func sendPetCtrlMenu(sess *net.Session, pet *world.PetInfo, open bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_VOICE_CHAT) // opcode 64
	w.WriteC(0x0c) // sub-opcode for pet control

	if open {
		w.WriteH(0x03)                // control mode (open)
		w.WriteD(0)                   // reserved
		w.WriteD(pet.ID)              // pet object ID
		w.WriteD(0)                   // reserved
		w.WriteH(uint16(pet.X))
		w.WriteH(uint16(pet.Y))
		w.WriteS(pet.Name)
	} else {
		w.WriteH(0x00)                // control mode (close)
		w.WriteD(1)                   // close indicator
		w.WriteD(pet.ID)
		w.WriteS("")                  // empty name
	}

	sess.Send(w.Bytes())
}

// sendPetInventory sends S_PetInventory (opcode 176) listing pet's held items.
// Java: S_PetInventory uses S_OPCODE_SHOWRETRIEVELIST.
// Packet: D(petID) H(itemCount) C(0x0b) [items...] C(petAC)
func sendPetInventory(sess *net.Session, pet *world.PetInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_RETRIEVE_LIST)
	w.WriteD(pet.ID)
	w.WriteH(uint16(len(pet.Items)))
	w.WriteC(0x0b) // pet item type indicator

	for _, item := range pet.Items {
		w.WriteD(item.ObjectID)        // unique item object ID
		w.WriteC(0x16)                 // type marker (always 0x16 for pet items — Java hardcoded)
		w.WriteH(uint16(item.GfxID))   // graphics ID
		w.WriteC(item.Bless)           // bless level
		w.WriteD(item.Count)           // item count
		if item.Equipped {
			w.WriteC(3) // equipped + identified
		} else {
			w.WriteC(1) // not equipped + identified
		}
		w.WriteS(item.Name) // display name
	}

	w.WriteC(byte(-pet.AC)) // pet AC value (displayed as positive)
	sess.Send(w.Bytes())
}

// sendPetEquipUpdate sends S_PetEquipment (opcode 250) subtype 0x25.
// Java: S_PetEquipment via S_OPCODE_PACKETBOX.
func sendPetEquipUpdate(sess *net.Session, pet *world.PetInfo, equipMode byte, equipStatus byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x25)       // subtype: pet equipment
	w.WriteC(equipMode)  // equipment type/mode
	w.WriteD(pet.ID)     // pet object ID
	w.WriteC(equipStatus)
	w.WriteC(byte(-pet.AC)) // pet AC
	sess.Send(w.Bytes())
}

// sendPetList sends S_PetList (opcode 83) listing available pet amulets for withdrawal.
// Java: S_PetList uses S_OPCODE_SELECTLIST with price constant 0x46.
func sendPetList(sess *net.Session, amulets []*world.InvItem) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SELECTLIST)
	w.WriteD(0x46) // price constant (always 0x46 for pets)
	w.WriteH(uint16(len(amulets)))
	for _, item := range amulets {
		w.WriteD(item.ObjectID)
		w.WriteC(1) // count (always 1 per amulet)
	}
	sess.Send(w.Bytes())
}

// sendSelectTarget sends S_SelectTarget (opcode 236) to client.
// Prompts the client to enter target selection mode for pet attack command.
// Java: S_SelectTarget — D(petID) C(0) C(0) C(2).
func sendSelectTarget(sess *net.Session, petID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SELECT_TARGET)
	w.WriteD(petID)
	w.WriteC(0) // reserved
	w.WriteC(0) // reserved
	w.WriteC(2) // type (2 = pet targeting)
	sess.Send(w.Bytes())
}

// ========================================================================
//  Shared companion packet helpers
// ========================================================================

// sendCompanionMove sends S_MOVE_OBJECT (opcode 10) for a companion entity.
// Java S_MoveCharPacket constructor 2 (NPC/AI): [C op][D id][H locX][H locY][C heading]
func sendCompanionMove(viewer *net.Session, objID int32, prevX, prevY int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MOVE_OBJECT)
	w.WriteD(objID)
	w.WriteH(uint16(prevX))
	w.WriteH(uint16(prevY))
	w.WriteC(byte(heading))
	viewer.Send(w.Bytes())
}

// sendCompanionEffect sends S_SkillSoundGFX (opcode 55) for a companion entity.
// Used for summon sound (169 = disappear), doll sounds (5935 = summon, 5936 = unsummon).
func sendCompanionEffect(viewer *net.Session, objID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(objID)
	w.WriteH(uint16(gfxID))
	viewer.Send(w.Bytes())
}

// SendCompanionEffect 廣播伴侶特效。Exported for system package usage.
func SendCompanionEffect(viewer *net.Session, objID int32, gfxID int32) {
	sendCompanionEffect(viewer, objID, gfxID)
}

// SendPetCtrlMenu 匯出 sendPetCtrlMenu — 供 system 套件開啟/關閉寵物控制面板。
func SendPetCtrlMenu(sess *net.Session, pet *world.PetInfo, open bool) {
	sendPetCtrlMenu(sess, pet, open)
}

// SendPetInventory 匯出 sendPetInventory — 供 system 套件發送寵物背包列表。
func SendPetInventory(sess *net.Session, pet *world.PetInfo) {
	sendPetInventory(sess, pet)
}

// SendSelectTarget 匯出 sendSelectTarget — 供 system 套件發送目標選擇游標。
func SendSelectTarget(sess *net.Session, petID int32) {
	sendSelectTarget(sess, petID)
}

// SendPetEquipUpdate 匯出 sendPetEquipUpdate — 供 system 套件發送寵物裝備狀態更新。
func SendPetEquipUpdate(sess *net.Session, pet *world.PetInfo, equipMode byte, equipStatus byte) {
	sendPetEquipUpdate(sess, pet, equipMode, equipStatus)
}

// SendDollTimer 匯出 sendDollTimer — 供 system 套件發送魔法娃娃計時器。
func SendDollTimer(sess *net.Session, seconds int32) {
	sendDollTimer(sess, seconds)
}
