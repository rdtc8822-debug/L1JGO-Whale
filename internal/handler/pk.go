package handler

import (
	"fmt"
	"math/rand"

	"github.com/l1jgo/server/internal/core/event"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

// HandleDuel processes C_DUEL (opcode 5).
// In 3.80C, this toggles the player's PK mode (fight stance).
// Client-side: enables/disables targeting players for attack.
// Server-side: tracks PKMode flag as defense-in-depth against modified clients.
func HandleDuel(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}
	player.PKMode = !player.PKMode
}

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

	// Set pink name — duration from Lua
	attacker.PinkName = true
	attacker.PinkNameTicks = deps.Scripting.GetPKTimers().PinkNameTicks

	// Send S_PinkName to attacker and nearby
	sendPinkName(attacker.Session, attacker.CharID, 180)
	nearby := deps.World.GetNearbyPlayers(attacker.X, attacker.Y, attacker.MapID, attacker.SessionID)
	for _, other := range nearby {
		sendPinkName(other.Session, attacker.CharID, 180)
	}

	// Alert nearby guards — Java: L1PcInstance.receiveDamage checks isPinkName
	// and sets all visible L1GuardInstance to target the attacker.
	nearbyNpcs := deps.World.GetNearbyNpcs(attacker.X, attacker.Y, attacker.MapID)
	for _, guard := range nearbyNpcs {
		if guard.Impl == "L1Guard" && !guard.Dead && guard.AggroTarget == 0 {
			guard.AggroTarget = attacker.SessionID
		}
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
		// Set wanted status — guards will hunt this player (duration from Lua)
		killer.WantedTicks = deps.Scripting.GetPKTimers().WantedTicks

		// PKCount incremented if killer's lawful < 30000 (Java condition)
		if killer.Lawful < 30000 {
			killer.PKCount++
		}

		// Calculate lawful decrease via Lua (level-based formula + clamping)
		pkResult := deps.Scripting.CalcPKLawfulPenalty(int(killer.Level), killer.Lawful)
		killer.Lawful = pkResult.NewLawful

		// Send lawful update
		sendLawful(killer.Session, killer.CharID, killer.Lawful)
		nearby := deps.World.GetNearbyPlayers(killer.X, killer.Y, killer.MapID, killer.SessionID)
		for _, other := range nearby {
			sendLawful(other.Session, killer.CharID, killer.Lawful)
		}

		// Send PK status messages
		sendPlayerStatus(killer.Session, killer)

		// Warning at PKCount thresholds (from Lua): message 551
		pkThresh := deps.Scripting.GetPKThresholds()
		if killer.PKCount >= pkThresh.Warning && killer.PKCount < pkThresh.Punish {
			sendRedMessage(killer.Session, 551, fmt.Sprintf("%d", killer.PKCount), fmt.Sprintf("%d", pkThresh.Punish))
		}

		deps.Log.Info(fmt.Sprintf("PK 擊殺  擊殺者=%s  受害者=%s  PK次數=%d  正義值=%d", killer.Name, victim.Name, killer.PKCount, killer.Lawful))
	}

	// Emit PlayerKilled event (PvP-specific, in addition to PlayerDied from KillPlayer)
	if deps.Bus != nil {
		event.Emit(deps.Bus, event.PlayerKilled{
			KillerCharID: killer.CharID,
			VictimCharID: victim.CharID,
			MapID:        victim.MapID,
			X:            victim.X,
			Y:            victim.Y,
		})
	}

	// Item drop from victim based on victim's lawful
	dropItemsOnPKDeath(victim, deps)
}

// ---------- Item Drop on PK Death ----------

// dropItemsOnPKDeath drops random items from the victim based on Lua formula.
func dropItemsOnPKDeath(victim *world.PlayerInfo, deps *Deps) {
	dropResult := deps.Scripting.CalcPKItemDrop(victim.Lawful)
	if !dropResult.ShouldDrop || dropResult.Count <= 0 {
		return
	}

	for i := 0; i < dropResult.Count; i++ {
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
		SendDropItem(viewer.Session, gndItem)
	}

	// Message 638: %0を失いました (你失去了%0)
	sendServerMessageStr(victim.Session, 638, itemInfo.Name)

	deps.Log.Info(fmt.Sprintf("PK 死亡掉落物品  受害者=%s  道具=%s  數量=%d", victim.Name, itemInfo.Name, dropCount))
}

// ---------- PvP Attack ----------

// inSafetyZone returns true if the player is standing in a safety zone tile.
func inSafetyZone(p *world.PlayerInfo, deps *Deps) bool {
	if deps.MapData == nil {
		return false
	}
	return deps.MapData.IsSafetyZone(p.MapID, p.X, p.Y)
}

// handlePvPAttack processes melee attack against another player.
func handlePvPAttack(attacker, target *world.PlayerInfo, deps *Deps) {
	if target.Dead {
		return
	}

	// Server-side PK mode check: must have PK button enabled to attack players
	if !attacker.PKMode {
		return
	}

	// Face the target
	attacker.Heading = calcHeading(attacker.X, attacker.Y, target.X, target.Y)

	// Java: if either attacker or target is in safety zone, play animation only (no damage)
	if inSafetyZone(attacker, deps) || inSafetyZone(target, deps) {
		nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
		for _, viewer := range nearby {
			sendAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading)
		}
		return
	}

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

	// Broadcast attack animation to all nearby (includes attacker — they're in melee range)
	nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearby {
		sendAttackPacket(viewer.Session, attacker.CharID, target.CharID, damage, attacker.Heading)
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

// handlePvPFarAttack processes ranged attack against another player.
func handlePvPFarAttack(attacker, target *world.PlayerInfo, deps *Deps) {
	if target.Dead {
		return
	}

	// Server-side PK mode check
	if !attacker.PKMode {
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

	// Java: if either attacker or target is in safety zone, play animation only (no damage)
	if inSafetyZone(attacker, deps) || inSafetyZone(target, deps) {
		sendArrowAttackPacket(attacker.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
			attacker.X, attacker.Y, target.X, target.Y)
		nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
		for _, viewer := range nearby {
			if viewer.SessionID == attacker.SessionID {
				continue
			}
			sendArrowAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
				attacker.X, attacker.Y, target.X, target.Y)
		}
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
