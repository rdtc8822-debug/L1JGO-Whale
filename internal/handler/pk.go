package handler

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

// HandleCheckPK processes C_CHECK_PK (opcode 51).
// Server responds with S_ServerMessage(562) containing the player's PK count.
func HandleCheckPK(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}
	// Message 562: 你的PK次數為%0次。
	sendServerMessageN(sess, 562, player.PKCount)
}

// ---------- Pink Name ----------

// triggerPinkName checks if the attacker should become pink-named from attacking a blue player.
// Conditions (matching Java L1PinkName.onAction):
// - Victim is blue (lawful >= 0) and not pink
// - Attacker is blue (lawful >= 0) and not pink
// - Both in normal zone (not safe zone or combat zone)
func triggerPinkName(attacker, victim *world.PlayerInfo, deps *Deps) {
	if attacker.PinkName {
		return // already pink
	}
	if attacker.Lawful < 0 {
		return // already red-named
	}
	if victim.Lawful < 0 || victim.PinkName {
		return // victim is red/pink — attacking them is "justified"
	}

	// Set pink name for 180 seconds (900 ticks at 200ms)
	attacker.PinkName = true
	attacker.PinkNameTicks = 900

	// Send S_PinkName to attacker and nearby
	sendPinkName(attacker.Session, attacker.CharID, 180)
	nearby := deps.World.GetNearbyPlayers(attacker.X, attacker.Y, attacker.MapID, attacker.SessionID)
	for _, other := range nearby {
		sendPinkName(other.Session, attacker.CharID, 180)
	}
}

// ---------- Lawful from Monster Kill ----------

// addLawfulFromNpc adds lawful alignment to the killer based on the NPC's lawful value.
// Java formula: add_lawful = npc.lawful * RATE_LA * -1
// Monsters typically have negative lawful (e.g. -2), so killing them gives POSITIVE lawful.
func addLawfulFromNpc(killer *world.PlayerInfo, npcLawful int32, deps *Deps) {
	if npcLawful == 0 {
		return
	}
	rate := deps.Config.Rates.LawfulRate
	if rate <= 0 {
		rate = 1.0
	}
	addLawful := int32(float64(npcLawful) * rate * -1)
	if addLawful == 0 {
		return
	}
	killer.Lawful += addLawful
	clampLawful(&killer.Lawful)

	// Send lawful update to killer and nearby
	sendLawful(killer.Session, killer.CharID, killer.Lawful)
	nearby := deps.World.GetNearbyPlayers(killer.X, killer.Y, killer.MapID, killer.SessionID)
	for _, other := range nearby {
		sendLawful(other.Session, killer.CharID, killer.Lawful)
	}
}

// ---------- PK Kill Logic ----------

// processPKKill handles the PK consequences when a player kills another player.
// - Increments PKCount if victim was blue-named and not pink
// - Decreases killer's lawful based on level-based formula
// - Triggers item drops from victim based on victim's lawful
func processPKKill(killer, victim *world.PlayerInfo, deps *Deps) {
	// Cancel pink name on the killer (already turned red or died)
	if killer.PinkName {
		killer.PinkName = false
		killer.PinkNameTicks = 0
		sendPinkName(killer.Session, killer.CharID, 0)
		nearby := deps.World.GetNearbyPlayers(killer.X, killer.Y, killer.MapID, killer.SessionID)
		for _, other := range nearby {
			sendPinkName(other.Session, killer.CharID, 0)
		}
	}

	// Only count as PK if victim was blue-named (lawful >= 0) and not pink
	if victim.Lawful >= 0 && !victim.PinkName {
		// PKCount incremented if killer's lawful < 30000 (Java condition)
		if killer.Lawful < 30000 {
			killer.PKCount++
		}

		// Calculate lawful decrease based on killer's level
		// Java: Level < 50: -(Level^2 * 4); Level >= 50: -(Level^3 * 0.08)
		var newLawful int32
		lvl := float64(killer.Level)
		if killer.Level < 50 {
			newLawful = -1 * int32(math.Pow(lvl, 2)*4)
		} else {
			newLawful = -1 * int32(math.Pow(lvl, 3)*0.08)
		}

		// Clamp: if current_lawful - 1000 is already lower than calculated, use that instead
		if killer.Lawful-1000 < newLawful {
			newLawful = killer.Lawful - 1000
		}
		if newLawful < -32768 {
			newLawful = -32768
		}
		killer.Lawful = newLawful
		clampLawful(&killer.Lawful)

		// Send lawful update
		sendLawful(killer.Session, killer.CharID, killer.Lawful)
		nearby := deps.World.GetNearbyPlayers(killer.X, killer.Y, killer.MapID, killer.SessionID)
		for _, other := range nearby {
			sendLawful(other.Session, killer.CharID, killer.Lawful)
		}

		// Send PK status messages
		sendPlayerStatus(killer.Session, killer)

		// Warning at PKCount 5-9: message 551 (你的PK次數為%0，達到%1次會被丟進地獄)
		// Java uses S_RedMessage (center screen red text) with 2 args: PKCount and "10"
		if killer.PKCount >= 5 && killer.PKCount < 10 {
			sendRedMessage(killer.Session, 551, fmt.Sprintf("%d", killer.PKCount), "10")
		}

		deps.Log.Info(fmt.Sprintf("PK 擊殺  擊殺者=%s  受害者=%s  PK次數=%d  正義值=%d", killer.Name, victim.Name, killer.PKCount, killer.Lawful))
	}

	// Item drop from victim based on victim's lawful
	dropItemsOnPKDeath(victim, deps)
}

// ---------- Item Drop on PK Death ----------

// dropItemsOnPKDeath drops random items from the victim based on their lawful value.
// Java formula: lostRate = ((lawful + 32768) / 1000 - 65) * 4
// Only drops for red-named (lawful < 0) players, doubled rate.
func dropItemsOnPKDeath(victim *world.PlayerInfo, deps *Deps) {
	if victim.Lawful >= 0 {
		return // blue-named players don't drop items on death
	}

	lostRate := int((float64(victim.Lawful+32768)/1000.0 - 65.0) * 4.0)
	if lostRate >= 0 {
		return
	}
	lostRate = -lostRate
	// Double rate for red-named
	if victim.Lawful < 0 {
		lostRate *= 2
	}

	// Roll the dice (out of 1000)
	rnd := rand.Intn(1000) + 1
	if rnd > lostRate {
		return // no drop
	}

	// Determine how many items to drop
	count := 1
	if victim.Lawful <= -30000 {
		count = rand.Intn(4) + 1
	} else if victim.Lawful <= -20000 {
		count = rand.Intn(3) + 1
	} else if victim.Lawful <= -10000 {
		count = rand.Intn(2) + 1
	}

	for i := 0; i < count; i++ {
		dropOneItem(victim, deps)
	}
}

// dropOneItem picks a random item from the victim's inventory and drops it on the ground.
func dropOneItem(victim *world.PlayerInfo, deps *Deps) {
	if len(victim.Inv.Items) == 0 {
		return
	}

	idx := rand.Intn(len(victim.Inv.Items))
	item := victim.Inv.Items[idx]

	// Skip adena
	if item.ItemID == world.AdenaItemID {
		return
	}

	// Skip non-tradeable items
	itemInfo := deps.Items.Get(item.ItemID)
	if itemInfo == nil || !itemInfo.Tradeable {
		return
	}

	// Unequip if equipped
	if item.Equipped {
		slot := findEquippedSlot(victim, item)
		if slot != world.SlotNone {
			unequipSlot(victim.Session, victim, slot, deps)
		}
	}

	// Drop the item to ground
	dropCount := int32(1)
	if item.Stackable && item.Count > 0 {
		dropCount = item.Count
	}

	gndItem := &world.GroundItem{
		ID:         item.ObjectID,
		ItemID:     item.ItemID,
		Count:      dropCount,
		EnchantLvl: item.EnchantLvl,
		Name:       itemInfo.Name,
		GrdGfx:     itemInfo.GrdGfx,
		X:          victim.X,
		Y:          victim.Y,
		MapID:      victim.MapID,
	}
	deps.World.AddGroundItem(gndItem)

	// Remove from victim's inventory
	victim.Inv.RemoveItem(item.ObjectID, 0) // remove all
	sendRemoveInventoryItem(victim.Session, item.ObjectID)
	sendWeightUpdate(victim.Session, victim)

	// Show ground item to nearby players
	nearby := deps.World.GetNearbyPlayersAt(victim.X, victim.Y, victim.MapID)
	for _, viewer := range nearby {
		sendDropItem(viewer.Session, gndItem)
	}

	// Message 638: %0を失いました (你失去了%0)
	sendServerMessageStr(victim.Session, 638, itemInfo.Name)

	deps.Log.Info(fmt.Sprintf("PK 死亡掉落物品  受害者=%s  道具=%s  數量=%d", victim.Name, itemInfo.Name, dropCount))
}

// ---------- PvP Attack ----------

// handlePvPAttack processes melee attack against another player.
func handlePvPAttack(attacker, target *world.PlayerInfo, deps *Deps) {
	if target.Dead {
		return
	}

	// Face the target
	attacker.Heading = calcHeading(attacker.X, attacker.Y, target.X, target.Y)

	// Trigger pink name if applicable
	triggerPinkName(attacker, target, deps)

	// Simple PvP damage calculation
	weaponDmg := 4 // fist
	if wpn := attacker.Equip.Weapon(); wpn != nil {
		if info := deps.Items.Get(wpn.ItemID); info != nil {
			if info.DmgSmall > 0 {
				weaponDmg = info.DmgSmall
			}
		}
	}

	// Use Lua combat formula — equipment stats are already in player fields
	ctx := scripting.CombatContext{
		AttackerLevel:  int(attacker.Level),
		AttackerSTR:    int(attacker.Str),
		AttackerDEX:    int(attacker.Dex),
		AttackerWeapon: weaponDmg,
		AttackerHitMod: int(attacker.HitMod),
		AttackerDmgMod: int(attacker.DmgMod),
		TargetAC:       int(target.AC),
		TargetLevel:    int(target.Level),
		TargetMR:       0,
	}
	result := deps.Scripting.CalcMeleeAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	// Broadcast attack animation to all nearby
	nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearby {
		sendAttackPacket(viewer.Session, attacker.CharID, target.CharID, damage, attacker.Heading)
	}
	// Also send to attacker if not in nearby list
	sendAttackPacket(attacker.Session, attacker.CharID, target.CharID, damage, attacker.Heading)

	if damage > 0 {
		target.HP -= int16(damage)
		if target.HP < 0 {
			target.HP = 0
		}
		sendHpUpdate(target.Session, target)

		if target.HP <= 0 {
			KillPlayer(target, deps)
			processPKKill(attacker, target, deps)
		}
	}
}

// handlePvPFarAttack processes ranged attack against another player.
func handlePvPFarAttack(attacker, target *world.PlayerInfo, deps *Deps) {
	if target.Dead {
		return
	}

	attacker.Heading = calcHeading(attacker.X, attacker.Y, target.X, target.Y)

	// Range check
	dx := attacker.X - target.X
	dy := attacker.Y - target.Y
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
	if dist > 10 {
		return
	}

	triggerPinkName(attacker, target, deps)

	// Find and consume arrow
	arrow := findArrow(attacker, deps)
	if arrow == nil {
		sendGlobalChat(attacker.Session, 9, "\\f3沒有箭矢。")
		return
	}
	arrowRemoved := attacker.Inv.RemoveItem(arrow.ObjectID, 1)
	if arrowRemoved {
		sendRemoveInventoryItem(attacker.Session, arrow.ObjectID)
	} else {
		sendItemCountUpdate(attacker.Session, arrow)
	}

	arrowDmg := 0
	if arrowInfo := deps.Items.Get(arrow.ItemID); arrowInfo != nil {
		arrowDmg = arrowInfo.DmgSmall
	}

	bowDmg := 1
	if wpn := attacker.Equip.Weapon(); wpn != nil {
		if info := deps.Items.Get(wpn.ItemID); info != nil {
			if info.DmgSmall > 0 {
				bowDmg = info.DmgSmall
			}
		}
	}

	// Equipment stats are already in player fields
	ctx := scripting.RangedCombatContext{
		AttackerLevel:     int(attacker.Level),
		AttackerSTR:       int(attacker.Str),
		AttackerDEX:       int(attacker.Dex),
		AttackerBowDmg:    bowDmg,
		AttackerArrowDmg:  arrowDmg,
		AttackerBowHitMod: int(attacker.BowHitMod),
		AttackerBowDmgMod: int(attacker.BowDmgMod),
		TargetAC:          int(target.AC),
		TargetLevel:       int(target.Level),
		TargetMR:          0,
	}
	result := deps.Scripting.CalcRangedAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	// Broadcast arrow attack animation
	sendArrowAttackPacket(attacker.Session, attacker.CharID, target.CharID, damage, attacker.Heading,
		attacker.X, attacker.Y, target.X, target.Y)
	nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearby {
		if viewer.SessionID == attacker.SessionID {
			continue
		}
		sendArrowAttackPacket(viewer.Session, attacker.CharID, target.CharID, damage, attacker.Heading,
			attacker.X, attacker.Y, target.X, target.Y)
	}

	if damage > 0 {
		target.HP -= int16(damage)
		if target.HP < 0 {
			target.HP = 0
		}
		sendHpUpdate(target.Session, target)

		if target.HP <= 0 {
			KillPlayer(target, deps)
			processPKKill(attacker, target, deps)
		}
	}
}

// ---------- Packet Helpers ----------

// sendPinkName sends S_PinkName (opcode 60).
// Format: [D objID][D timeSec] — timeSec=180 to enable, 0 to remove.
func sendPinkName(sess *net.Session, charID int32, timeSec int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PINKNAME)
	w.WriteD(charID)
	w.WriteD(timeSec)
	sess.Send(w.Bytes())
}

// sendLawful sends S_Lawful (opcode 34).
// Format: [D objID][H lawful][D 0]
func sendLawful(sess *net.Session, charID int32, lawful int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_LAWFUL)
	w.WriteD(charID)
	w.WriteH(uint16(int16(lawful))) // int16 range
	w.WriteD(0)                     // padding (matches Java)
	sess.Send(w.Bytes())
}

// sendServerMessageN sends S_ServerMessage with a numeric parameter.
// Format: [H msgID][C argCount][S arg1]
// Used for messages like "你的PK次數為%0次" where %0 is a number.
func sendServerMessageN(sess *net.Session, msgID uint16, value int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(1) // 1 argument
	w.WriteS(fmt.Sprintf("%d", value))
	sess.Send(w.Bytes())
}

// sendServerMessageStr sends S_ServerMessage with one string parameter.
// Format: [H msgID][C 1][S arg]
func sendServerMessageStr(sess *net.Session, msgID uint16, arg string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(1)
	w.WriteS(arg)
	sess.Send(w.Bytes())
}

// sendRedMessage sends S_RedMessage (opcode 105) — center screen red text warning.
// Wire format identical to S_ServerMessage: [H msgID][C argCount][S args...]
func sendRedMessage(sess *net.Session, msgID uint16, args ...string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REDMESSAGE)
	w.WriteH(msgID)
	w.WriteC(byte(len(args)))
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// clampLawful clamps lawful value to int16 range [-32768, 32767].
func clampLawful(lawful *int32) {
	if *lawful > 32767 {
		*lawful = 32767
	} else if *lawful < -32768 {
		*lawful = -32768
	}
}
