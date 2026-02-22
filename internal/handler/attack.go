package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

// HandleAttack processes C_ATTACK (opcode 229).
// Format: [D targetID][H x][H y]
func HandleAttack(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetID := r.ReadD()
	_ = r.ReadH() // target x (unused, we use server position)
	_ = r.ReadH() // target y (unused)

	ws := deps.World
	player := ws.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Look up target — could be NPC or player
	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		// Not an NPC — check if it's a player (PvP)
		targetPlayer := ws.GetByCharID(targetID)
		if targetPlayer != nil && !targetPlayer.Dead && targetPlayer.CharID != player.CharID {
			handlePvPAttack(player, targetPlayer, deps)
		}
		return
	}

	// Range check (Chebyshev <= 2 for melee + tolerance)
	dx := player.X - npc.X
	dy := player.Y - npc.Y
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
	if dist > 2 {
		return
	}

	// Face the target
	player.Heading = calcHeading(player.X, player.Y, npc.X, npc.Y)

	// Determine weapon damage from equipped weapon
	weaponDmg := 4 // fist damage default
	targetSize := npc.Size
	if targetSize == "" {
		targetSize = "small"
	}
	if wpn := player.Equip.Weapon(); wpn != nil {
		if info := deps.Items.Get(wpn.ItemID); info != nil {
			if targetSize == "large" && info.DmgLarge > 0 {
				weaponDmg = info.DmgLarge
			} else if info.DmgSmall > 0 {
				weaponDmg = info.DmgSmall
			}
		}
	}

	// Call Lua combat formula — equipment stats are already applied to player fields
	ctx := scripting.CombatContext{
		AttackerLevel:  int(player.Level),
		AttackerSTR:    int(player.Str),
		AttackerDEX:    int(player.Dex),
		AttackerWeapon: weaponDmg,
		AttackerHitMod: int(player.HitMod),
		AttackerDmgMod: int(player.DmgMod),
		TargetAC:       int(npc.AC),
		TargetLevel:    int(npc.Level),
		TargetMR:       int(npc.MR),
	}
	result := deps.Scripting.CalcMeleeAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	// Get nearby players for broadcasting
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// Send attack animation to all nearby
	for _, viewer := range nearby {
		sendAttackPacket(viewer.Session, player.CharID, npc.ID, damage, player.Heading)
	}

	if damage > 0 {
		// Apply damage
		npc.HP -= damage
		if npc.HP < 0 {
			npc.HP = 0
		}

		// Set aggro on hit (so even non-agro mobs fight back)
		if npc.AggroTarget == 0 {
			npc.AggroTarget = sess.ID
		}

		// Send HP meter update
		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		for _, viewer := range nearby {
			sendHpMeter(viewer.Session, npc.ID, hpRatio)
		}

		// Check death
		if npc.HP <= 0 {
			handleNpcDeath(npc, player, nearby, deps)
		}
	}
}

// HandleFarAttack processes C_FAR_ATTACK (opcode 123) — bow/ranged attacks.
// Format: [D targetID][H x][H y]
func HandleFarAttack(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetID := r.ReadD()
	_ = r.ReadH()
	_ = r.ReadH()

	ws := deps.World
	player := ws.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		// Not an NPC — check if it's a player (PvP ranged)
		targetPlayer := ws.GetByCharID(targetID)
		if targetPlayer != nil && !targetPlayer.Dead && targetPlayer.CharID != player.CharID {
			handlePvPFarAttack(player, targetPlayer, deps)
		}
		return
	}

	// Range check (Chebyshev <= 10 for ranged)
	dx := player.X - npc.X
	dy := player.Y - npc.Y
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

	player.Heading = calcHeading(player.X, player.Y, npc.X, npc.Y)

	// Find and consume an arrow from inventory
	arrow := findArrow(player, deps)
	if arrow == nil {
		// No arrows — notify player
		sendGlobalChat(sess, 9, "\\f3沒有箭矢。")
		return
	}

	// Consume 1 arrow
	arrowRemoved := player.Inv.RemoveItem(arrow.ObjectID, 1)
	if arrowRemoved {
		sendRemoveInventoryItem(sess, arrow.ObjectID)
	} else {
		sendItemCountUpdate(sess, arrow)
	}

	// Arrow damage bonus
	arrowDmg := 0
	if arrowInfo := deps.Items.Get(arrow.ItemID); arrowInfo != nil {
		arrowDmg = arrowInfo.DmgSmall
	}

	// Determine weapon damage from equipped bow
	bowDmg := 1
	targetSize := npc.Size
	if targetSize == "" {
		targetSize = "small"
	}
	if wpn := player.Equip.Weapon(); wpn != nil {
		if info := deps.Items.Get(wpn.ItemID); info != nil {
			if targetSize == "large" && info.DmgLarge > 0 {
				bowDmg = info.DmgLarge
			} else if info.DmgSmall > 0 {
				bowDmg = info.DmgSmall
			}
		}
	}

	// Equipment stats are already applied to player fields
	ctx := scripting.RangedCombatContext{
		AttackerLevel:     int(player.Level),
		AttackerSTR:       int(player.Str),
		AttackerDEX:       int(player.Dex),
		AttackerBowDmg:    bowDmg,
		AttackerArrowDmg:  arrowDmg,
		AttackerBowHitMod: int(player.BowHitMod),
		AttackerBowDmgMod: int(player.BowDmgMod),
		TargetAC:          int(npc.AC),
		TargetLevel:       int(npc.Level),
		TargetMR:          int(npc.MR),
	}
	result := deps.Scripting.CalcRangedAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// Send ranged attack animation with arrow projectile visual
	// Java: sendPackets(self) + broadcastPacket(others)
	sendArrowAttackPacket(sess, player.CharID, npc.ID, damage, player.Heading,
		player.X, player.Y, npc.X, npc.Y)
	for _, viewer := range nearby {
		if viewer.SessionID == sess.ID {
			continue // already sent to self
		}
		sendArrowAttackPacket(viewer.Session, player.CharID, npc.ID, damage, player.Heading,
			player.X, player.Y, npc.X, npc.Y)
	}

	if damage > 0 {
		npc.HP -= damage
		if npc.HP < 0 {
			npc.HP = 0
		}

		if npc.AggroTarget == 0 {
			npc.AggroTarget = sess.ID
		}

		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		for _, viewer := range nearby {
			sendHpMeter(viewer.Session, npc.ID, hpRatio)
		}

		if npc.HP <= 0 {
			handleNpcDeath(npc, player, nearby, deps)
		}
	}
}

// handleNpcDeath processes NPC death: animation, exp, respawn timer.
func handleNpcDeath(npc *world.NpcInfo, killer *world.PlayerInfo, nearby []*world.PlayerInfo, deps *Deps) {
	npc.Dead = true

	// Clear tile collision (dead NPC doesn't block movement)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(npc.MapID, npc.X, npc.Y, false)
	}

	// Broadcast death animation
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, npc.ID, 8) // ACTION_Die = 8
	}

	// Remove NPC from view
	for _, viewer := range nearby {
		sendRemoveObject(viewer.Session, npc.ID)
	}

	// Give exp to killer (apply server exp rate)
	expGain := npc.Exp
	if deps.Config.Rates.ExpRate > 0 {
		expGain = int32(float64(expGain) * deps.Config.Rates.ExpRate)
	}
	if expGain > 0 {
		addExp(killer, expGain, deps)
	}

	// Give lawful from kill
	addLawfulFromNpc(killer, npc.Lawful, deps)

	// Give drops to killer
	GiveDrops(killer, npc.NpcID, deps)

	// Set respawn timer (in ticks: delay_seconds * 5 at 200ms tick)
	if npc.RespawnDelay > 0 {
		npc.RespawnTimer = npc.RespawnDelay * 5
	}

	deps.Log.Info(fmt.Sprintf("NPC 被擊殺  擊殺者=%s  NPC=%s  經驗=%d", killer.Name, npc.Name, expGain))
}

// addExp adds experience to a player and checks for level up.
// Level up HP/MP formulas are in Lua (scripts/core/levelup.lua).
// Exp table is in Lua (scripts/core/tables.lua).
func addExp(player *world.PlayerInfo, expGain int32, deps *Deps) {
	player.Exp += expGain

	newLevel := deps.Scripting.LevelFromExp(int(player.Exp))
	leveledUp := false
	for int16(newLevel) > player.Level && player.Level < 99 {
		player.Level++
		leveledUp = true

		// Roll HP/MP gains per level via Lua
		result := deps.Scripting.CalcLevelUp(int(player.ClassType), int(player.Con), int(player.Wis))
		player.MaxHP += int16(result.HP)
		player.MaxMP += int16(result.MP)
		player.HP = player.MaxHP // full heal on level up
		player.MP = player.MaxMP
	}

	// Send exp update to player
	sendExpUpdate(player.Session, player.Level, player.Exp)

	if leveledUp {
		// Send full status update (client detects level change and shows effect)
		sendPlayerStatus(player.Session, player)

		// Show RaiseAttr dialog if bonus stat points are now available (level 51+)
		if player.Level >= bonusStatMinLevel {
			available := player.Level - 50 - player.BonusStats
			totalStats := player.Str + player.Dex + player.Con + player.Wis + player.Intel + player.Cha
			if available > 0 && totalStats < maxTotalStats {
				sendRaiseAttrDialog(player.Session, player.CharID)
			}
		}

		deps.Log.Info(fmt.Sprintf("玩家升級  角色=%s  等級=%d  經驗=%d  最大HP=%d  最大MP=%d", player.Name, player.Level, player.Exp, player.MaxHP, player.MaxMP))
	}
}

// findArrow finds the first arrow item in the player's inventory.
func findArrow(player *world.PlayerInfo, deps *Deps) *world.InvItem {
	for _, item := range player.Inv.Items {
		info := deps.Items.Get(item.ItemID)
		if info != nil && info.ItemType == "arrow" && item.Count > 0 {
			return item
		}
	}
	return nil
}

// calcHeading returns the heading direction from (sx,sy) to (tx,ty).
func calcHeading(sx, sy, tx, ty int32) int16 {
	ddx := tx - sx
	ddy := ty - sy
	if ddx > 0 {
		ddx = 1
	} else if ddx < 0 {
		ddx = -1
	}
	if ddy > 0 {
		ddy = 1
	} else if ddy < 0 {
		ddy = -1
	}
	for i := int16(0); i < 8; i++ {
		if headingDX[i] == ddx && headingDY[i] == ddy {
			return i
		}
	}
	return 0
}
