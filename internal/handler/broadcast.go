package handler

import (
	"sync/atomic"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// arrowSeqNum is a global sequential number for arrow projectile packets (matches Java AtomicInteger).
var arrowSeqNum atomic.Int32

// sendOwnCharPackPlayer sends S_PUT_OBJECT (opcode 87) for the player's OWN character.
// Uses S_OwnCharPack format (different trailing bytes from S_OtherCharPacks).
// Must be used when sending the character pack to the player themselves (teleport, map change).
// Using S_OtherCharPacks format for own char ID causes the client to misparse → invisible/grey model.
func sendOwnCharPackPlayer(sess *net.Session, p *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(p.X))
	w.WriteH(uint16(p.Y))
	w.WriteD(p.CharID)
	w.WriteH(uint16(PlayerGfx(p)))
	w.WriteC(p.CurrentWeapon)
	w.WriteC(byte(p.Heading))
	w.WriteC(0)           // light size
	w.WriteC(p.MoveSpeed) // move speed
	w.WriteD(1)           // unknown (always 1)
	w.WriteH(uint16(p.Lawful))
	w.WriteS(p.Name)
	w.WriteS(p.Title)
	status := byte(0x04) // PC flag
	status |= p.BraveSpeed * 16
	w.WriteC(status)
	w.WriteD(0) // clan emblem ID
	w.WriteS(p.ClanName)
	w.WriteS("") // null
	// Clan rank (OwnCharPack specific — OtherCharPacks always writes 0)
	if p.ClanRank > 0 {
		w.WriteC(byte(p.ClanRank << 4))
	} else {
		w.WriteC(0xb0)
	}
	partyHP := byte(0xff)
	if p.PartyID > 0 {
		partyHP = world.CalcPartyHP(p.HP, p.MaxHP)
	}
	w.WriteC(partyHP)
	w.WriteC(0x00) // third speed
	w.WriteC(0x00) // PC = 0
	w.WriteC(0x00) // unknown
	w.WriteC(0xff) // unknown
	w.WriteC(0xff) // unknown
	w.WriteS("")   // null
	w.WriteC(0x00) // end
	sess.Send(w.Bytes())
}

// SendPutObject sends S_PUT_OBJECT (opcode 87) to show another player to the viewer.
// Matches Java S_OtherCharPacks format exactly.
func SendPutObject(viewer *net.Session, p *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(p.X))
	w.WriteH(uint16(p.Y))
	w.WriteD(p.CharID)
	w.WriteH(uint16(PlayerGfx(p))) // use polymorph GFX if active
	w.WriteC(p.CurrentWeapon)    // current weapon visual
	w.WriteC(byte(p.Heading))
	w.WriteC(0)                  // light size
	w.WriteC(p.MoveSpeed)        // move speed: 0=normal, 1=haste
	w.WriteD(1)                  // unknown (always 1)
	w.WriteH(uint16(p.Lawful))
	w.WriteS(p.Name)
	w.WriteS(p.Title)
	status := byte(0x04)         // bit 2 = PC flag
	status |= p.BraveSpeed * 16  // brave speed encoded in bits 4-5
	w.WriteC(status)             // status flags
	w.WriteD(0)                  // clan emblem ID
	w.WriteS(p.ClanName)
	w.WriteS("")                 // null
	w.WriteC(0)                  // unknown (always 0 for other PCs)
	partyHP := byte(0xff)        // 0xff = not in party
	if p.PartyID > 0 {
		partyHP = world.CalcPartyHP(p.HP, p.MaxHP)
	}
	w.WriteC(partyHP)            // party HP bar (0-10, proportional)
	w.WriteC(0x00)               // third speed
	w.WriteC(0x00)               // PC = 0, NPC = level
	w.WriteS("")                 // private shop / null
	w.WriteC(0xff)               // unknown
	w.WriteC(0xff)               // unknown
	viewer.Send(w.Bytes())
}

// SendRemoveObject sends S_REMOVE_OBJECT (opcode 120) to remove an entity from view.
func SendRemoveObject(viewer *net.Session, charID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_OBJECT)
	w.WriteD(charID)
	viewer.Send(w.Bytes())
}

// sendMoveObject sends S_MOVE_OBJECT (opcode 10) to animate PC movement.
// Sends the PREVIOUS position + heading — client calculates destination.
// Java S_MoveCharPacket constructor 1: [C op][D id][H locX][H locY][C heading][H 0]
func sendMoveObject(viewer *net.Session, charID int32, prevX, prevY int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MOVE_OBJECT)
	w.WriteD(charID)
	w.WriteH(uint16(prevX))
	w.WriteH(uint16(prevY))
	w.WriteC(byte(heading))
	w.WriteH(0) // Java: writeH(0) — trailing padding
	viewer.Send(w.Bytes())
}

// sendChangeHeading sends S_CHANGEHEADING (opcode 122) — direction change to nearby players.
// Format: [D objectId][C heading]
func sendChangeHeading(viewer *net.Session, charID int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGEHEADING)
	w.WriteD(charID)
	w.WriteC(byte(heading))
	viewer.Send(w.Bytes())
}

// sendWeather sends S_WEATHER (opcode 115).
func sendWeather(sess *net.Session, weather byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_WEATHER)
	w.WriteC(weather)
	sess.Send(w.Bytes())
}

// sendGameTime sends S_GameTime (opcode 123) — current game time in seconds.
func sendGameTime(sess *net.Session, gameTimeSec int) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TIME)
	w.WriteD(int32(gameTimeSec))
	sess.Send(w.Bytes())
}

// sendMagicStatus sends S_MAGIC_STATUS (opcode 37) — SP and MR.
func sendMagicStatus(sess *net.Session, sp byte, mr uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MAGIC_STATUS)
	w.WriteC(sp)
	w.WriteH(mr)
	sess.Send(w.Bytes())
}

// SendNpcPack sends S_PUT_OBJECT (opcode 87) for an NPC to the viewer.
func SendNpcPack(viewer *net.Session, npc *world.NpcInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(npc.X))
	w.WriteH(uint16(npc.Y))
	w.WriteD(npc.ID)
	w.WriteH(uint16(npc.GfxID))
	w.WriteC(0)                   // status (0 = normal)
	w.WriteC(byte(npc.Heading))
	w.WriteC(0)                   // light
	w.WriteC(0)                   // move speed
	w.WriteD(npc.Exp)             // experience reward
	w.WriteH(0)                   // lawful
	w.WriteS(npc.NameID)
	w.WriteS("")                  // title
	w.WriteC(0x00)                // ext status: NO PC flag
	w.WriteD(0)                   // reserved
	w.WriteS("")                  // no clan
	w.WriteS("")                  // no master
	w.WriteC(0x00)                // hidden = 0 (normal)
	w.WriteC(0xFF)                // HP% (0xFF = full for initial)
	w.WriteC(0x00)                // reserved
	w.WriteC(byte(npc.Level))     // level
	w.WriteC(0xFF)                // reserved
	w.WriteC(0xFF)                // reserved
	w.WriteC(0x00)                // reserved
	viewer.Send(w.Bytes())
}

// sendAttackPacket sends S_ATTACK (opcode 30) — attack animation.
// Format: [C opcode][C actionId][D attackerID][D targetID][H damage][C heading][D 0][C effectFlags]
func sendAttackPacket(viewer *net.Session, attackerID, targetID, damage int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(1)                    // actionId: 1 = normal melee
	w.WriteD(attackerID)
	w.WriteD(targetID)
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(0)                    // reserved
	w.WriteC(0)                    // effect flags (0 = none)
	viewer.Send(w.Bytes())
}

// sendArrowAttackPacket sends S_UseArrowSkill (same opcode 30) — ranged attack with arrow projectile.
// Java: S_UseArrowSkill uses actionId=1 + sequential number + projectile GFX + coordinates.
func sendArrowAttackPacket(viewer *net.Session, attackerID, targetID, damage int32, heading int16, ax, ay, tx, ty int32) {
	seq := arrowSeqNum.Add(1)
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(1)                    // actionId: 1 = PC attack (same as melee per Java)
	w.WriteD(attackerID)
	w.WriteD(targetID)
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(seq)                  // sequential number (must be non-zero, incrementing)
	w.WriteH(66)                   // arrowGfxId: 66 = normal arrow projectile
	w.WriteC(0)                    // use_type: 0 = arrow/projectile
	w.WriteH(uint16(ax))          // attacker X
	w.WriteH(uint16(ay))          // attacker Y
	w.WriteH(uint16(tx))          // target X
	w.WriteH(uint16(ty))          // target Y
	w.WriteC(0)                    // effect flags
	w.WriteC(0)
	w.WriteC(0)
	viewer.Send(w.Bytes())
}

// sendUseAttackSkill sends S_UseAttackSkill (opcode 30) — magic attack with projectile.
// Matches Java S_UseAttackSkill format exactly.
// actionId: 18 = ACTION_SkillAttack
// useType: 6 = ranged magic, 8 = ranged AoE magic
func sendUseAttackSkill(viewer *net.Session, casterID, targetID int32, damage int16, heading int16, gfxID int32, useType byte, cx, cy, tx, ty int32) {
	seq := arrowSeqNum.Add(1)
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(18)                   // actionId: 18 = ACTION_SkillAttack
	w.WriteD(casterID)             // caster char ID (non-zero = show cast motion)
	w.WriteD(targetID)             // target object ID
	w.WriteH(uint16(damage))      // damage
	w.WriteC(byte(heading))        // heading toward target
	w.WriteD(seq)                  // sequential number
	w.WriteH(uint16(gfxID))       // spell GFX ID
	w.WriteC(useType)              // 6=ranged magic, 8=AoE magic
	w.WriteH(uint16(cx))          // caster X
	w.WriteH(uint16(cy))          // caster Y
	w.WriteH(uint16(tx))          // target X
	w.WriteH(uint16(ty))          // target Y
	w.WriteC(0)                    // padding
	w.WriteC(0)                    // padding
	w.WriteC(0)                    // effect flags
	viewer.Send(w.Bytes())
}

// sendHpMeter sends S_HP_METER (opcode 237) — NPC HP bar.
// Format: [C opcode][D objectID][H hpRatio(0-100)]
func sendHpMeter(viewer *net.Session, objectID int32, hpRatio int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HP_METER)
	w.WriteD(objectID)
	w.WriteH(uint16(hpRatio))
	viewer.Send(w.Bytes())
}

// sendActionGfx sends S_ACTION (opcode 158) — action animation (death, etc.).
// Format: [C opcode][D objectID][C actionCode]
func sendActionGfx(viewer *net.Session, objectID int32, actionCode byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ACTION)
	w.WriteD(objectID)
	w.WriteC(actionCode)
	viewer.Send(w.Bytes())
}

// sendExpUpdate sends S_EXP (opcode 113) — level + cumulative exp.
// Format: [C opcode][C level][D totalExp]
func sendExpUpdate(sess *net.Session, level int16, totalExp int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EXP)
	w.WriteC(byte(level))
	w.WriteD(totalExp)
	sess.Send(w.Bytes())
}

// sendPlayerStatus sends S_STATUS (opcode 8) — full character status update.
// Same format as enterworld sendOwnCharStatus but built from PlayerInfo.
// SendPlayerStatus sends S_STATUS to a player. Exported for system package usage.
func SendPlayerStatus(sess *net.Session, p *world.PlayerInfo) {
	sendPlayerStatus(sess, p)
}

func sendPlayerStatus(sess *net.Session, p *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_STATUS)
	w.WriteD(p.CharID)
	level := p.Level
	if level < 1 {
		level = 1
	} else if level > 127 {
		level = 127
	}
	w.WriteC(byte(level))
	w.WriteD(p.Exp)
	w.WriteC(byte(p.Str))
	w.WriteC(byte(p.Intel))
	w.WriteC(byte(p.Wis))
	w.WriteC(byte(p.Dex))
	w.WriteC(byte(p.Con))
	w.WriteC(byte(p.Cha))
	w.WriteH(uint16(p.HP))
	w.WriteH(uint16(p.MaxHP))
	w.WriteH(uint16(p.MP))
	w.WriteH(uint16(p.MaxMP))
	w.WriteC(byte(p.AC))

	gameTime := int32(time.Now().Unix())
	gameTime = gameTime - (gameTime%300)
	w.WriteD(gameTime)

	w.WriteC(byte(p.Food))
	maxW := world.MaxWeight(p.Str, p.Con)
	w.WriteC(p.Inv.Weight242(maxW))
	w.WriteH(uint16(p.Lawful))
	w.WriteH(uint16(p.FireRes))
	w.WriteH(uint16(p.WaterRes))
	w.WriteH(uint16(p.WindRes))
	w.WriteH(uint16(p.EarthRes))
	w.WriteD(0) // monster kills (TODO: load from DB)
	sess.Send(w.Bytes())
}

// sendSkillEffect sends S_EFFECT (opcode 55) — GFX effect on an entity.
func sendSkillEffect(viewer *net.Session, objectID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(objectID)
	w.WriteH(uint16(gfxID))
	viewer.Send(w.Bytes())
}

// SendDropItem sends S_PUT_OBJECT (opcode 87) for a ground item.
// Same opcode as S_CharPack, but client distinguishes by the status byte (0x00 = item vs 0x04 = PC).
// Matches Java S_DropItem packet format.
func SendDropItem(viewer *net.Session, item *world.GroundItem) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(item.X))
	w.WriteH(uint16(item.Y))
	w.WriteD(item.ID)
	w.WriteH(uint16(item.GrdGfx)) // ground graphic ID
	w.WriteC(0)                    // status
	w.WriteC(0)                    // heading
	w.WriteC(0)                    // light
	w.WriteC(0)                    // speed
	w.WriteD(item.Count)           // item count
	w.WriteH(0)                    // lawful
	w.WriteS(item.Name)            // item display name
	w.WriteS("")                   // title
	w.WriteC(0x00)                 // status flags: 0 = item (not PC)
	w.WriteD(0)                    // reserved
	w.WriteS("")                   // no clan
	w.WriteS("")                   // no master
	w.WriteC(0x00)                 // hidden
	w.WriteC(0xFF)                 // reserved
	w.WriteC(0x00)                 // reserved
	w.WriteC(0x00)                 // level
	w.WriteC(0xFF)                 // reserved
	w.WriteC(0xFF)                 // reserved
	w.WriteC(0x00)                 // reserved
	viewer.Send(w.Bytes())
}

// ==================== Buff Icon Packets ====================

// sendIconShield sends S_SkillIconShield (opcode 216) — AC buff icon.
// Java: [C opcode=216][H time][C type][D 0]
// Types: 2=Shield, 3=ShadowArmor, 6=EarthSkin, 7=EarthBless, 10=IronSkin
// Send time=0 to cancel.
func sendIconShield(sess *net.Session, durationSec uint16, iconType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SKILLICONSHIELD)
	w.WriteH(durationSec)
	w.WriteC(iconType)
	w.WriteD(0)
	sess.Send(w.Bytes())
}

// sendIconStrup sends S_Strup (opcode 166) — STR buff icon.
// Java: [C opcode=166][H time][C currentStr][C weightPercent][C type][H 0]
// Types: 2=DressMighty, 5=PhysicalEnchantSTR
// Send time=0 to cancel.
func sendIconStrup(sess *net.Session, durationSec uint16, currentStr byte, iconType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_STRUP)
	w.WriteH(durationSec)
	w.WriteC(currentStr)
	w.WriteC(0) // weight percent (placeholder)
	w.WriteC(iconType)
	w.WriteH(0)
	sess.Send(w.Bytes())
}

// sendIconDexup sends S_Dexup (opcode 188) — DEX buff icon.
// Java: [C opcode=188][H time][C currentDex][C type][C 0][C 0][C 0]
// Types: 2=DressDexterity, 5=PhysicalEnchantDEX
// Send time=0 to cancel.
func sendIconDexup(sess *net.Session, durationSec uint16, currentDex byte, iconType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_DEXUP)
	w.WriteH(durationSec)
	w.WriteC(currentDex)
	w.WriteC(iconType)
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

// sendIconAura sends S_SkillIconAura (opcode 250, sub-opcode 0x16) — aura buff icon.
// Java: [C opcode=250][C 0x16][C iconId][H time]
// iconId uses the Java skill constant (= our skill_id - 1 for aura/elf skills).
// Send time=0 to cancel.
func sendIconAura(sess *net.Session, iconID byte, durationSec uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x16) // sub-opcode: aura icon
	w.WriteC(iconID)
	w.WriteH(durationSec)
	sess.Send(w.Bytes())
}

// sendIconGfx sends S_SkillIconGFX (opcode 250) — general buff icon.
// Java: [C opcode=250][C iconId][H time]
// iconId: 34=green potion, 40=Immune to Harm, etc.
// Send time=0 to cancel.
func sendIconGfx(sess *net.Session, iconID byte, durationSec uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(iconID)
	w.WriteH(durationSec)
	sess.Send(w.Bytes())
}

// sendWisdomPotionIcon sends S_SkillIconWisdomPotion (opcode 250) — wisdom potion buff icon.
// Java: S_SkillIconWisdomPotion: [C opcode=250][C 0x39][C 0x2c][C time/4]
// Send time=0 to cancel icon.
func sendWisdomPotionIcon(sess *net.Session, timeSec uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x39)
	w.WriteC(0x2c)
	w.WriteC(byte(timeSec / 4))
	sess.Send(w.Bytes())
}

// sendBluePotionIcon sends S_SkillIconGFX (opcode 250, icon 34) — blue potion buff icon.
// Java: S_SkillIconGFX(34, time): [C opcode=250][C 34][H time]
// Send time=0 to cancel icon.
func sendBluePotionIcon(sess *net.Session, timeSec uint16) {
	sendIconGfx(sess, 34, timeSec)
}

// sendWeightUpdate sends S_PacketBox(WEIGHT) (opcode 250, subcode 10) — lightweight weight bar update.
// Java: S_PacketBox.WEIGHT = 10; format: [C opcode=250][C 10][C weight242]
// Sent after every inventory add/remove/count change.
func sendWeightUpdate(sess *net.Session, p *world.PlayerInfo) {
	maxW := world.MaxWeight(p.Str, p.Con)
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(10) // subcode: WEIGHT
	w.WriteC(p.Inv.Weight242(maxW))
	sess.Send(w.Bytes())
}

// sendFoodUpdate sends S_PacketBox(FOOD) (opcode 250, subcode 11) — lightweight food bar update.
// Java: S_PacketBox.FOOD = 11; format: [C opcode=250][C 11][C food]
func sendFoodUpdate(sess *net.Session, food int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(11) // subcode: FOOD
	w.WriteC(byte(food))
	sess.Send(w.Bytes())
}

// sendInvisible sends S_Invis (opcode 171) — invisibility state.
// Java: [C opcode=171][D objectId][C type]
// type: 0=visible, 1=invisible
func sendInvisible(sess *net.Session, objectID int32, invisible bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_INVISIBLE)
	w.WriteD(objectID)
	if invisible {
		w.WriteC(1)
	} else {
		w.WriteC(0)
	}
	sess.Send(w.Bytes())
}

// ==================== 狀態異常封包 ====================

// S_Paralysis 子類型常數（Java: S_Paralysis.java）
const (
	ParalysisApply     byte = 0x02 // 麻痺施加
	ParalysisRemove    byte = 0x03 // 麻痺解除
	ParalysisMobApply  byte = 0x04 // 怪物麻痺毒施加
	ParalysisMobRemove byte = 0x05 // 怪物麻痺毒解除
	TeleportLock       byte = 0x06 // 傳送鎖定
	TeleportUnlock     byte = 0x07 // 傳送解鎖（已用於 sendTeleportUnlock）
	SleepApply         byte = 0x0A // 睡眠施加
	SleepRemove        byte = 0x0B // 睡眠解除
	FreezeApply        byte = 0x0C // 凍結施加
	FreezeRemove       byte = 0x0D // 凍結解除
	StunApply          byte = 0x16 // 暈眩施加
	StunRemove         byte = 0x17 // 暈眩解除
	BindApply          byte = 0x18 // 束縛施加
	BindRemove         byte = 0x19 // 束縛解除
)

// sendParalysis 發送 S_Paralysis (opcode 202) 到目標玩家。
// Java 格式：[C opcode=202][C subtype]
// 用於暈眩/凍結/睡眠/麻痺/束縛的施加與解除。
func sendParalysis(sess *net.Session, subtype byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PARALYSIS)
	w.WriteC(subtype)
	sess.Send(w.Bytes())
}

// sendPoison 發送 S_Poison (opcode 165) — 中毒/凍結色調視覺效果。
// Java 格式：[C opcode=165][D objectId][C byte1][C byte2]
// poisonType: 0=治癒（清除色調）, 1=綠色（傷害毒）, 2=灰色（麻痺/凍結）
func sendPoison(viewer *net.Session, objectID int32, poisonType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_POISON)
	w.WriteD(objectID)
	switch poisonType {
	case 1: // 綠色（傷害毒）
		w.WriteC(0x01)
		w.WriteC(0x00)
	case 2: // 灰色（麻痺/凍結）
		w.WriteC(0x00)
		w.WriteC(0x01)
	default: // 治癒
		w.WriteC(0x00)
		w.WriteC(0x00)
	}
	viewer.Send(w.Bytes())
}

// sendCurseBlind 發送 S_CurseBlind (opcode 47) — 致盲螢幕遮罩。
// Java 格式：[C opcode=47][H type]
// type: 0=解除, 1=施加, 2=減弱施加
func sendCurseBlind(sess *net.Session, blindType uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CURSEBLIND)
	w.WriteH(blindType)
	sess.Send(w.Bytes())
}
