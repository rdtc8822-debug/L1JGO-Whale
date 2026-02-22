package handler

import (
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// Skill-related message IDs
const (
	msgNotEnoughMP uint16 = 278 // "因魔力不足而無法使用魔法。"
	msgNotEnoughHP uint16 = 279 // "因體力不足而無法使用魔法。"
	msgCastFail    uint16 = 280 // "施展魔法失敗。"
)

// HandleUseSpell processes C_USE_SPELL (opcode 6).
// Packet format: [C row][C column] then variable data depending on skill:
//
//	Most spells: [D targetID][H targetX][H targetY]
func HandleUseSpell(sess *net.Session, r *packet.Reader, deps *Deps) {
	row := int32(r.ReadC())
	column := int32(r.ReadC())
	skillID := row*8 + column + 1

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	skill := deps.Skills.Get(skillID)
	if skill == nil {
		deps.Log.Debug("unknown skill", zap.Int32("skill_id", skillID))
		return
	}

	// Read target info (most spells have this)
	var targetID int32
	if r.Remaining() >= 8 {
		targetID = r.ReadD()
		_ = r.ReadH() // targetX
		_ = r.ReadH() // targetY
	}

	deps.Log.Debug("C_UseSpell",
		zap.String("player", player.Name),
		zap.Int32("skill_id", skillID),
		zap.String("skill", skill.Name),
		zap.String("target_type", skill.Target),
		zap.Int32("target", targetID),
	)

	// --- Validation ---

	// Check if player knows this spell
	if !playerKnowsSpell(player, skillID) {
		sendServerMessage(sess, msgCastFail)
		return
	}

	// Global cast cooldown (Java: isSkillDelay — blocks ALL spells until delay expires)
	now := time.Now()
	if now.Before(player.SkillDelayUntil) {
		return // silently ignore, still in cooldown
	}

	// HP cost check
	if skill.HpConsume > 0 && player.HP <= int16(skill.HpConsume) {
		sendServerMessage(sess, msgNotEnoughHP)
		return
	}

	// MP cost check
	if skill.MpConsume > 0 && player.MP < int16(skill.MpConsume) {
		sendServerMessage(sess, msgNotEnoughMP)
		return
	}

	// --- Consume resources ---
	if skill.MpConsume > 0 {
		player.MP -= int16(skill.MpConsume)
		sendMpUpdate(sess, player)
	}
	if skill.HpConsume > 0 {
		player.HP -= int16(skill.HpConsume)
		sendHpUpdate(sess, player)
	}

	// --- Set global cooldown (Java: L1SkillDelay) ---
	// ReuseDelay from YAML is in milliseconds (e.g. 1000 = 1 second)
	delay := skill.ReuseDelay
	if delay <= 0 {
		delay = 1000 // default 1 second
	}
	player.SkillDelayUntil = now.Add(time.Duration(delay) * time.Millisecond)

	// --- Resurrection spells: special routing (targets dead players) ---
	if isResurrectionSkill(skill, deps) {
		executeResurrection(sess, player, skill, targetID, deps)
		return
	}

	// --- Execute skill based on target type ---
	switch skill.Target {
	case "attack":
		executeAttackSkill(sess, player, skill, targetID, deps)
	case "buff":
		executeBuffSkill(sess, player, skill, targetID, deps)
	default:
		// "none" target = self-effect (e.g., light, shields)
		executeSelfSkill(sess, player, skill, deps)
	}
}

// isResurrectionSkill returns true for resurrection-type spells (defined in Lua).
func isResurrectionSkill(skill *data.SkillInfo, deps *Deps) bool {
	fn := deps.Scripting
	if fn == nil {
		return false
	}
	return fn.GetResurrectEffect(int(skill.SkillID)) != nil
}

// executeResurrection handles resurrection spells (18, 75, 131, 165).
func executeResurrection(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)

	// Broadcast cast animation
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	// Item consumption check (skills 75, 165 require specific items)
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot == nil || slot.Count < int32(skill.ItemConsumeCount) {
			sendServerMessage(sess, msgCastFail)
			return
		}
		removed := player.Inv.RemoveItem(slot.ObjectID, int32(skill.ItemConsumeCount))
		if removed {
			sendRemoveInventoryItem(sess, slot.ObjectID)
		} else {
			sendItemCountUpdate(sess, slot)
		}
		sendWeightUpdate(sess, player)
	}

	switch skill.SkillID {
	case 131: // 世界樹的呼喚 — AoE resurrection (nearby dead players)
		resurrected := false
		for _, p := range nearby {
			if p.Dead {
				resurrectPlayer(p, player, skill, deps)
				resurrected = true
			}
		}
		if !resurrected {
			sendServerMessage(sess, msgCastFail)
		}

	default: // 18, 75, 165 — single target resurrection
		if targetID == 0 {
			sendServerMessage(sess, msgCastFail)
			return
		}
		target := deps.World.GetByCharID(targetID)
		if target == nil || !target.Dead {
			sendServerMessage(sess, msgCastFail)
			return
		}
		if target.MapID != player.MapID {
			return
		}

		// Skill 18 has probability check (probability_dice: 7 out of 10)
		if skill.ProbabilityDice > 0 {
			if world.RandInt(10) >= skill.ProbabilityDice {
				sendServerMessage(sess, msgCastFail)
				return
			}
		}

		resurrectPlayer(target, player, skill, deps)
	}

	// Send cast GFX
	if skill.CastGfx > 0 {
		for _, viewer := range nearby {
			sendSkillEffect(viewer.Session, player.CharID, skill.CastGfx)
		}
	}
}

// resurrectPlayer revives a dead player with HP/MP based on the resurrection spell used.
// Resurrection effects are defined in Lua (scripts/skill/resurrection.lua).
func resurrectPlayer(target *world.PlayerInfo, caster *world.PlayerInfo, skill *data.SkillInfo, deps *Deps) {
	target.Dead = false

	eff := deps.Scripting.GetResurrectEffect(int(skill.SkillID))
	if eff != nil {
		if eff.FixedHP == -1 {
			// Special: HP = caster's level (e.g. skill 18)
			target.HP = int16(caster.Level)
		} else if eff.FixedHP > 0 {
			target.HP = int16(eff.FixedHP)
		} else {
			target.HP = int16(float64(target.MaxHP) * eff.HPRatio)
			target.MP = int16(float64(target.MaxMP) * eff.MPRatio)
		}
	} else {
		target.HP = int16(target.Level)
	}

	if target.HP < 1 {
		target.HP = 1
	}
	if target.HP > target.MaxHP {
		target.HP = target.MaxHP
	}
	if target.MP > target.MaxMP {
		target.MP = target.MaxMP
	}

	// Send updates to the resurrected player
	sendHpUpdate(target.Session, target)
	sendMpUpdate(target.Session, target)
	sendPlayerStatus(target.Session, target)
	sendPutObject(target.Session, target)

	// Notify nearby players to refresh the resurrected player's appearance
	nearbyTarget := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearbyTarget {
		if viewer.SessionID != target.SessionID {
			sendPutObject(viewer.Session, target)
		}
	}

	deps.Log.Info(fmt.Sprintf("玩家復活  目標=%s  施法者=%s  技能ID=%d", target.Name, caster.Name, skill.SkillID))
}

// executeAttackSkill handles damage-dealing spells targeted at NPCs.
// Damage is computed by Lua (scripts/combat/magic.lua).
func executeAttackSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	ws := deps.World

	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		return
	}

	if npc.MapID != player.MapID {
		return
	}

	// Range check — Triple Arrow uses bow range, others use skill.Ranged
	maxRange := int32(skill.Ranged)
	if skill.SkillID == 132 {
		maxRange = 10
	} else if maxRange <= 0 {
		maxRange = 2
	}
	dist := chebyshevDist(player.X, player.Y, npc.X, npc.Y)
	if dist > maxRange+2 {
		return
	}

	player.Heading = calcHeading(player.X, player.Y, npc.X, npc.Y)

	// Triple Arrow (132): consume 1 arrow
	if skill.SkillID == 132 {
		arrow := findArrow(player, deps)
		if arrow == nil {
			sendServerMessage(sess, msgCastFail)
			return
		}
		if player.Inv.RemoveItem(arrow.ObjectID, 1) {
			sendRemoveInventoryItem(sess, arrow.ObjectID)
		} else {
			sendItemCountUpdate(sess, arrow)
		}
	}

	// Equipment stats are already in player fields
	weaponDmg := 4 // fist
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

	// Lua damage calculation
	buildCtx := func(n *world.NpcInfo) scripting.SkillDamageContext {
		return scripting.SkillDamageContext{
			SkillID:         int(skill.SkillID),
			DamageValue:     skill.DamageValue,
			DamageDice:      skill.DamageDice,
			DamageDiceCount: skill.DamageDiceCount,
			SkillLevel:      skill.SkillLevel,
			Attr:            skill.Attr,
			AttackerLevel:   int(player.Level),
			AttackerSTR:     int(player.Str),
			AttackerDEX:     int(player.Dex),
			AttackerINT:     int(player.Intel),
			AttackerWIS:     int(player.Wis),
			AttackerSP:      int(player.SP),
			AttackerDmgMod:  int(player.DmgMod),
			AttackerHitMod:  int(player.HitMod),
			AttackerWeapon:  weaponDmg,
			AttackerHP:      int(player.HP),
			AttackerMaxHP:   int(player.MaxHP),
			TargetAC:        int(n.AC),
			TargetLevel:     int(n.Level),
			TargetMR:        int(n.MR),
			TargetMP:        int(n.MP),
		}
	}

	type hitTarget struct {
		npc      *world.NpcInfo
		dmg      int32
		hitCount int
		drainMP  int32
	}

	res := deps.Scripting.CalcSkillDamage(buildCtx(npc))
	hits := []hitTarget{{npc: npc, dmg: int32(res.Damage), hitCount: res.HitCount, drainMP: int32(res.DrainMP)}}

	if skill.Area > 0 {
		allNpcs := ws.GetNearbyNpcs(npc.X, npc.Y, npc.MapID)
		for _, other := range allNpcs {
			if other.ID == npc.ID || other.Dead {
				continue
			}
			if chebyshevDist(npc.X, npc.Y, other.X, other.Y) <= int32(skill.Area) {
				r := deps.Scripting.CalcSkillDamage(buildCtx(other))
				hits = append(hits, hitTarget{npc: other, dmg: int32(r.Damage), hitCount: r.HitCount, drainMP: int32(r.DrainMP)})
			}
		}
	}

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// Physical skills use melee attack animation; magic skills use spell projectile
	isPhysicalSkill := skill.DamageValue == 0 && skill.DamageDice == 0

	useType := byte(6)
	if skill.Area > 0 {
		useType = 8
	}

	for _, t := range hits {
		hitsToApply := t.hitCount
		if hitsToApply < 1 {
			hitsToApply = 1
		}

		for h := 0; h < hitsToApply; h++ {
			dmg := t.dmg

			// Visual: physical → melee animation, magic → spell projectile
			if isPhysicalSkill {
				for _, viewer := range nearby {
					sendAttackPacket(viewer.Session, player.CharID, t.npc.ID, dmg, player.Heading)
				}
				if skill.CastGfx > 0 {
					for _, viewer := range nearby {
						sendSkillEffect(viewer.Session, t.npc.ID, skill.CastGfx)
					}
				}
			} else {
				gfxID := int32(skill.CastGfx)
				if gfxID <= 0 {
					gfxID = int32(skill.ActionID)
				}
				for _, viewer := range nearby {
					sendUseAttackSkill(viewer.Session, player.CharID, t.npc.ID,
						int16(dmg), player.Heading, gfxID, useType,
						int32(player.X), int32(player.Y), int32(t.npc.X), int32(t.npc.Y))
				}
			}

			t.npc.HP -= dmg
			if t.npc.HP < 0 {
				t.npc.HP = 0
			}

			// Mind Break: drain MP from target
			if h == 0 && t.drainMP > 0 && t.npc.MP >= t.drainMP {
				t.npc.MP -= t.drainMP
			}

			if t.npc.AggroTarget == 0 {
				t.npc.AggroTarget = sess.ID
			}

			hpRatio := int16(0)
			if t.npc.MaxHP > 0 {
				hpRatio = int16((t.npc.HP * 100) / t.npc.MaxHP)
			}
			for _, viewer := range nearby {
				sendHpMeter(viewer.Session, t.npc.ID, hpRatio)
			}

			if t.npc.HP <= 0 {
				handleNpcDeath(t.npc, player, nearby, deps)
				break // NPC dead, stop multi-hit
			}
		}
	}
}

// executeBuffSkill handles healing and buff spells.
func executeBuffSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	target := player
	if targetID != 0 && targetID != player.CharID {
		if other := deps.World.GetByCharID(targetID); other != nil {
			// Validate: same map and within range
			if other.MapID != player.MapID || other.Dead {
				return
			}
			if chebyshevDist(player.X, player.Y, other.X, other.Y) > 20 {
				return
			}
			target = other
		}
	}

	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)

	// Broadcast cast animation
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	// Handle instant-effect spells on target
	switch skill.SkillID {
	case 9: // 解毒術 Cure Poison — remove poison
		// Clear poison status (currently no poison tracking; just send cure GFX)

	case 23: // 能量感測 Sense — show target info
		// TODO: send target stats to caster

	case 37: // 聖潔之光 Remove Curse — cures poison and paralysis
		target.Paralyzed = false
		target.Sleeped = false

	case 39: // 魔力奪取 Mana Drain — steal MP from target
		drain := int16(5 + world.RandInt(10))
		if target.MP >= drain {
			target.MP -= drain
			player.MP += drain
			if player.MP > player.MaxMP {
				player.MP = player.MaxMP
			}
			sendMpUpdate(target.Session, target)
			sendMpUpdate(sess, player)
		}

	case 44: // 魔法相消術 Cancellation — remove all buffs from target
		if targetID != 0 && targetID != player.CharID {
			cancelAllBuffs(target, deps)
		}

	case 145: // 釋放元素 Return to Nature — remove summons (placeholder)

	case 153: // 魔法消除 Dispel — remove buffs from target
		cancelAllBuffs(target, deps)
	}

	// Apply healing (for heal-type spells with damage_value/dice)
	if skill.Type == 16 || skill.DamageValue > 0 || skill.DamageDice > 0 {
		casterINT := int(player.Intel)
		casterSP := int(player.SP)

		if skill.Area == -1 {
			// AoE heal: heal all nearby players (screen-wide)
			for _, p := range nearby {
				heal := int16(deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
				if heal > 0 && p.HP < p.MaxHP {
					p.HP += heal
					if p.HP > p.MaxHP {
						p.HP = p.MaxHP
					}
					sendHpUpdate(p.Session, p)
				}
			}
		} else {
			// Single target heal
			heal := int16(deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
			if heal > 0 && target.HP < target.MaxHP {
				target.HP += heal
				if target.HP > target.MaxHP {
					target.HP = target.MaxHP
				}
				sendHpUpdate(target.Session, target)
			}
		}
	}

	// Apply buff effects (for spells with duration)
	applyBuffEffect(target, skill, deps)

	// Send effect GFX on target
	if skill.CastGfx > 0 {
		for _, viewer := range nearby {
			sendSkillEffect(viewer.Session, target.CharID, skill.CastGfx)
		}
	}

	if skill.SysMsgHappen > 0 {
		sendServerMessage(target.Session, uint16(skill.SysMsgHappen))
	}
}

// sendBuffIcon sends the appropriate buff icon packet to the client for a given skill.
// Each buff type has its own icon opcode/format. Duration in seconds; 0 = cancel.
func sendBuffIcon(target *world.PlayerInfo, skillID int32, durationSec uint16) {
	sess := target.Session
	switch skillID {
	// AC buff icons (S_SkillIconShield opcode 216)
	case 3:
		sendIconShield(sess, durationSec, 2) // Shield
	case 21:
		sendIconShield(sess, durationSec, 3) // Blessed Armor
	case 24, 99:
		sendIconShield(sess, durationSec, 3) // Shadow Armor / DE Shadow Armor
	case 151:
		sendIconShield(sess, durationSec, 6) // Earth Skin
	case 159:
		sendIconShield(sess, durationSec, 7) // Earth Bless
	case 168:
		sendIconShield(sess, durationSec, 10) // Iron Skin

	// STR buff icons (S_Strup opcode 166)
	case 109:
		sendIconStrup(sess, durationSec, byte(target.Str), 2) // Dress Mighty
	case 42:
		sendIconStrup(sess, durationSec, byte(target.Str), 5) // Physical Enchant STR

	// DEX buff icons (S_Dexup opcode 188)
	case 110:
		sendIconDexup(sess, durationSec, byte(target.Dex), 2) // Dress Dexterity
	case 26:
		sendIconDexup(sess, durationSec, byte(target.Dex), 5) // Physical Enchant DEX

	// Aura/weapon buff icons (S_SkillIconAura opcode 250, sub 0x16)
	// iconId = skillID - 1 for all aura-type skills
	case 114: // Glowing Aura → iconId 113
		sendIconAura(sess, byte(skillID-1), durationSec)
	case 115: // Shining Aura → iconId 114
		sendIconAura(sess, byte(skillID-1), durationSec)
	case 117: // Brave Aura → iconId 116
		sendIconAura(sess, byte(skillID-1), durationSec)
	case 148: // Fire Weapon → iconId 147
		sendIconAura(sess, byte(skillID-1), durationSec)
	case 149: // Wind Shot → iconId 148
		sendIconAura(sess, byte(skillID-1), durationSec)
	case 156: // Storm Eye → iconId 155
		sendIconAura(sess, byte(skillID-1), durationSec)
	case 163: // Burning Weapon → iconId 162
		sendIconAura(sess, byte(skillID-1), durationSec)
	case 166: // Storm Shot → iconId 165
		sendIconAura(sess, byte(skillID-1), durationSec)

	// Invisibility (S_Invis opcode 171)
	case 60, 97:
		sendInvisible(sess, target.CharID, durationSec > 0)
	}
}

// cancelBuffIcon cancels the buff icon for a given skill (sends icon with time=0).
func cancelBuffIcon(target *world.PlayerInfo, skillID int32) {
	sendBuffIcon(target, skillID, 0)
}

// applyBuffEffect applies stat changes and registers the buff timer.
// Buff definitions are in Lua (scripts/combat/buffs.lua).
// Go engine handles: remove exclusions → apply deltas → register timer → send packets.
func applyBuffEffect(target *world.PlayerInfo, skill *data.SkillInfo, deps *Deps) {
	if skill.BuffDuration <= 0 {
		return // instant effect, no buff to track
	}

	buff := &world.ActiveBuff{
		SkillID:   skill.SkillID,
		TicksLeft: skill.BuffDuration * 5, // seconds → ticks (200ms each)
	}

	// Query Lua for buff definition
	eff := deps.Scripting.GetBuffEffect(int(skill.SkillID), int(target.Level))

	if eff != nil {
		// Remove conflicting buffs
		for _, exID := range eff.Exclusions {
			removeBuffAndRevert(target, int32(exID), deps)
		}

		// Set stat deltas on the buff record (for reversal on expiry)
		buff.DeltaAC = int16(eff.AC)
		buff.DeltaStr = int16(eff.Str)
		buff.DeltaDex = int16(eff.Dex)
		buff.DeltaCon = int16(eff.Con)
		buff.DeltaWis = int16(eff.Wis)
		buff.DeltaIntel = int16(eff.Intel)
		buff.DeltaCha = int16(eff.Cha)
		buff.DeltaMaxHP = int16(eff.MaxHP)
		buff.DeltaMaxMP = int16(eff.MaxMP)
		buff.DeltaHitMod = int16(eff.HitMod)
		buff.DeltaDmgMod = int16(eff.DmgMod)
		buff.DeltaSP = int16(eff.SP)
		buff.DeltaMR = int16(eff.MR)
		buff.DeltaHPR = int16(eff.HPR)
		buff.DeltaMPR = int16(eff.MPR)
		buff.DeltaBowHit = int16(eff.BowHit)
		buff.DeltaBowDmg = int16(eff.BowDmg)
		buff.DeltaDodge = int16(eff.Dodge)
		buff.DeltaFireRes = int16(eff.FireRes)
		buff.DeltaWaterRes = int16(eff.WaterRes)
		buff.DeltaWindRes = int16(eff.WindRes)
		buff.DeltaEarthRes = int16(eff.EarthRes)

		// Apply stat deltas to target
		target.AC += buff.DeltaAC
		target.Str += buff.DeltaStr
		target.Dex += buff.DeltaDex
		target.Con += buff.DeltaCon
		target.Wis += buff.DeltaWis
		target.Intel += buff.DeltaIntel
		target.Cha += buff.DeltaCha
		target.MaxHP += buff.DeltaMaxHP
		target.MaxMP += buff.DeltaMaxMP
		target.HitMod += buff.DeltaHitMod
		target.DmgMod += buff.DeltaDmgMod
		target.SP += buff.DeltaSP
		target.MR += buff.DeltaMR
		target.HPR += buff.DeltaHPR
		target.MPR += buff.DeltaMPR
		target.BowHitMod += buff.DeltaBowHit
		target.BowDmgMod += buff.DeltaBowDmg
		target.Dodge += buff.DeltaDodge
		target.FireRes += buff.DeltaFireRes
		target.WaterRes += buff.DeltaWaterRes
		target.WindRes += buff.DeltaWindRes
		target.EarthRes += buff.DeltaEarthRes

		// Special flags
		if eff.MoveSpeed > 0 {
			buff.SetMoveSpeed = byte(eff.MoveSpeed)
			target.MoveSpeed = byte(eff.MoveSpeed)
			target.HasteTicks = buff.TicksLeft
			sendSpeedToAll(target, deps, byte(eff.MoveSpeed), uint16(skill.BuffDuration))
		}
		if eff.BraveSpeed > 0 {
			buff.SetBraveSpeed = byte(eff.BraveSpeed)
			target.BraveSpeed = byte(eff.BraveSpeed)
			sendSpeedToAll(target, deps, byte(eff.BraveSpeed), uint16(skill.BuffDuration))
		}
		if eff.Invisible {
			buff.SetInvisible = true
			target.Invisible = true
		}
		if eff.Paralyzed {
			buff.SetParalyzed = true
			target.Paralyzed = true
		}
		if eff.Sleeped {
			buff.SetSleeped = true
			target.Sleeped = true
		}
	}
	// else: no Lua definition → generic timer-only buff (no stat changes)

	// Register the buff (replace old if exists)
	old := target.AddBuff(buff)
	if old != nil {
		revertBuffStats(target, old)
	}

	// Send status update for any stat-changing buff
	if buff.DeltaStr != 0 || buff.DeltaDex != 0 || buff.DeltaCon != 0 ||
		buff.DeltaWis != 0 || buff.DeltaIntel != 0 || buff.DeltaCha != 0 ||
		buff.DeltaMaxHP != 0 || buff.DeltaMaxMP != 0 || buff.DeltaAC != 0 {
		sendPlayerStatus(target.Session, target)
	}

	// Send buff icon to client
	sendBuffIcon(target, skill.SkillID, uint16(skill.BuffDuration))
}

// removeBuffAndRevert removes a conflicting buff and reverts its stats.
func removeBuffAndRevert(target *world.PlayerInfo, skillID int32, deps *Deps) {
	old := target.RemoveBuff(skillID)
	if old != nil {
		revertBuffStats(target, old)
		cancelBuffIcon(target, skillID)
		if deps.Skills != nil {
			if sk := deps.Skills.Get(skillID); sk != nil && sk.SysMsgStop > 0 {
				sendServerMessage(target.Session, uint16(sk.SysMsgStop))
			}
		}
	}
}

// revertBuffStats undoes all stat deltas from a buff (Java: L1SkillStop.stopSkill).
func revertBuffStats(target *world.PlayerInfo, buff *world.ActiveBuff) {
	target.AC -= buff.DeltaAC
	target.Str -= buff.DeltaStr
	target.Dex -= buff.DeltaDex
	target.Con -= buff.DeltaCon
	target.Wis -= buff.DeltaWis
	target.Intel -= buff.DeltaIntel
	target.Cha -= buff.DeltaCha
	target.MaxHP -= buff.DeltaMaxHP
	target.MaxMP -= buff.DeltaMaxMP
	target.HitMod -= buff.DeltaHitMod
	target.DmgMod -= buff.DeltaDmgMod
	target.SP -= buff.DeltaSP
	target.MR -= buff.DeltaMR
	target.HPR -= buff.DeltaHPR
	target.MPR -= buff.DeltaMPR
	target.BowHitMod -= buff.DeltaBowHit
	target.BowDmgMod -= buff.DeltaBowDmg
	target.FireRes -= buff.DeltaFireRes
	target.WaterRes -= buff.DeltaWaterRes
	target.WindRes -= buff.DeltaWindRes
	target.EarthRes -= buff.DeltaEarthRes
	target.Dodge -= buff.DeltaDodge
	if target.HP > target.MaxHP && target.MaxHP > 0 {
		target.HP = target.MaxHP
	}
	if target.MP > target.MaxMP && target.MaxMP > 0 {
		target.MP = target.MaxMP
	}
	// Clear special flags
	if buff.SetInvisible {
		target.Invisible = false
	}
	if buff.SetParalyzed {
		target.Paralyzed = false
	}
	if buff.SetSleeped {
		target.Sleeped = false
	}
}

// sendSpeedToAll sends S_SPEED to self and nearby players.
func sendSpeedToAll(target *world.PlayerInfo, deps *Deps, speedType byte, duration uint16) {
	sendSpeedPacket(target.Session, target.CharID, speedType, duration)
	nearby := deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
	for _, other := range nearby {
		sendSpeedPacket(other.Session, target.CharID, speedType, duration)
	}
}

// executeSelfSkill handles self-target spells (light, shields, etc.).
func executeSelfSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, deps *Deps) {
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)

	// Handle instant-effect utility spells
	switch skill.SkillID {
	case 2: // 日光術 Light — toggle light (just visual, send GFX)
		// Light has duration in YAML but no stat changes; applyBuffEffect handles it

	case 13: // 無所遁形術 Detect — reveal invisible (screen-wide)
		// TODO: clear invisible on all nearby hidden players

	case 44: // 魔法相消術 Cancellation — remove all buffs from target/self
		cancelAllBuffs(player, deps)

	case 72: // 強力無所遁形術 Greater Detect — reveal invisible + damage

	case 130: // 心靈轉換 Mind Transfer — convert HP to MP
		transfer := player.HP / 4
		if transfer > 0 && player.HP > transfer {
			player.HP -= transfer
			player.MP += transfer
			if player.MP > player.MaxMP {
				player.MP = player.MaxMP
			}
			sendHpUpdate(sess, player)
			sendMpUpdate(sess, player)
		}

	case 146: // 魂體轉換 Soul Transfer — convert MP to HP
		transfer := player.MP / 4
		if transfer > 0 && player.MP > transfer {
			player.MP -= transfer
			player.HP += transfer
			if player.HP > player.MaxHP {
				player.HP = player.MaxHP
			}
			sendHpUpdate(sess, player)
			sendMpUpdate(sess, player)
		}

	case 172: // 暴風疾走 Storm Walk — instant speed buff (like Wind Walk)
		player.BraveSpeed = 4
		player.BraveTicks = 300 * 5 // ~5 minutes
		sendSpeedToAll(player, deps, 4, 300)
	}

	// Broadcast cast animation
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	// Self-centered AoE heal (e.g. 164 生命的祝福: target=none, type=16, area=-1)
	if skill.Type == 16 && (skill.DamageValue > 0 || skill.DamageDice > 0) {
		casterINT := int(player.Intel)
		casterSP := int(player.SP)

		if skill.Area == -1 {
			// Screen-wide AoE heal (self + nearby)
			heal := int16(deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
			if heal > 0 && player.HP < player.MaxHP {
				player.HP += heal
				if player.HP > player.MaxHP {
					player.HP = player.MaxHP
				}
				sendHpUpdate(sess, player)
			}
			for _, p := range nearby {
				if p.SessionID == sess.ID {
					continue
				}
				h := int16(deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
				if h > 0 && p.HP < p.MaxHP {
					p.HP += h
					if p.HP > p.MaxHP {
						p.HP = p.MaxHP
					}
					sendHpUpdate(p.Session, p)
				}
			}
		} else {
			// Self heal
			heal := int16(deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
			if heal > 0 && player.HP < player.MaxHP {
				player.HP += heal
				if player.HP > player.MaxHP {
					player.HP = player.MaxHP
				}
				sendHpUpdate(sess, player)
			}
		}
	}

	// Self-centered AoE damage to nearby NPCs (e.g. 189 衝擊之膚: target=none, type=64, area>0)
	if skill.Type == 64 && skill.Area > 0 && skill.DamageValue > 0 {
		nearbyNpcs := deps.World.GetNearbyNpcs(player.X, player.Y, player.MapID)
		for _, npc := range nearbyNpcs {
			if npc.Dead {
				continue
			}
			if chebyshevDist(player.X, player.Y, npc.X, npc.Y) > int32(skill.Area) {
				continue
			}
			ctx := scripting.SkillDamageContext{
				SkillID:         int(skill.SkillID),
				DamageValue:     skill.DamageValue,
				DamageDice:      skill.DamageDice,
				DamageDiceCount: skill.DamageDiceCount,
				SkillLevel:      skill.SkillLevel,
				Attr:            skill.Attr,
				AttackerLevel:   int(player.Level),
				AttackerSTR:     int(player.Str),
				AttackerDEX:     int(player.Dex),
				AttackerINT:     int(player.Intel),
				AttackerWIS:     int(player.Wis),
				AttackerSP:      int(player.SP),
				AttackerDmgMod:  int(player.DmgMod),
				AttackerHitMod:  int(player.HitMod),
				TargetAC:        int(npc.AC),
				TargetLevel:     int(npc.Level),
				TargetMR:        int(npc.MR),
			}
			res := deps.Scripting.CalcSkillDamage(ctx)
			dmg := int32(res.Damage)
			for _, viewer := range nearby {
				sendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
			npc.HP -= dmg
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

	// Apply buff effects (Shield, Light, Meditation, etc.)
	applyBuffEffect(player, skill, deps)

	// Send effect GFX
	if skill.CastGfx > 0 {
		for _, viewer := range nearby {
			sendSkillEffect(viewer.Session, player.CharID, skill.CastGfx)
		}
	}

	if skill.SysMsgHappen > 0 {
		sendServerMessage(sess, uint16(skill.SysMsgHappen))
	}
}

// cancelAllBuffs removes all cancellable buffs from a player (Java: Cancellation).
// Non-cancellable list is defined in Lua (scripts/combat/buffs.lua).
func cancelAllBuffs(target *world.PlayerInfo, deps *Deps) {
	if target.ActiveBuffs == nil {
		return
	}
	for skillID, buff := range target.ActiveBuffs {
		if deps.Scripting.IsNonCancellable(int(skillID)) {
			continue
		}
		revertBuffStats(target, buff)
		delete(target.ActiveBuffs, skillID)
		cancelBuffIcon(target, skillID)

		if buff.SetMoveSpeed > 0 {
			target.MoveSpeed = 0
			target.HasteTicks = 0
			sendSpeedToAll(target, deps, 0, 0)
		}
		if buff.SetBraveSpeed > 0 {
			target.BraveSpeed = 0
			sendSpeedToAll(target, deps, 0, 0)
		}
	}
	sendPlayerStatus(target.Session, target)
}

// TickPlayerBuffs decrements buff timers and expires them. Called from game loop each tick.
func TickPlayerBuffs(p *world.PlayerInfo, deps *Deps) {
	if p.ActiveBuffs == nil {
		return
	}
	for skillID, buff := range p.ActiveBuffs {
		if buff.TicksLeft <= 0 {
			continue // permanent or already handled
		}
		buff.TicksLeft--
		if buff.TicksLeft <= 0 {
			// Buff expired — revert stats
			revertBuffStats(p, buff)
			delete(p.ActiveBuffs, skillID)

			// Cancel buff icon
			cancelBuffIcon(p, skillID)

			// Handle speed-related buff expiry
			if buff.SetMoveSpeed > 0 {
				p.MoveSpeed = 0
				p.HasteTicks = 0
				sendSpeedToAll(p, deps, 0, 0) // cancel haste/slow
			}
			if buff.SetBraveSpeed > 0 {
				p.BraveSpeed = 0
				sendSpeedToAll(p, deps, 0, 0) // cancel brave/holy walk
			}

			if deps.Skills != nil {
				if sk := deps.Skills.Get(skillID); sk != nil && sk.SysMsgStop > 0 {
					sendServerMessage(p.Session, uint16(sk.SysMsgStop))
				}
			}

			sendPlayerStatus(p.Session, p)
		}
	}

	// Brave expiration (from potions, not spell buffs — tracked separately)
	if p.BraveTicks > 0 {
		p.BraveTicks--
		if p.BraveTicks <= 0 {
			oldBrave := p.BraveSpeed
			p.BraveSpeed = 0
			sendSpeedToAll(p, deps, oldBrave, 0)
		}
	}

	// Haste expiration (from potions — tracked separately)
	if p.HasteTicks > 0 {
		p.HasteTicks--
		if p.HasteTicks <= 0 {
			p.MoveSpeed = 0
			sendSpeedToAll(p, deps, 0, 0)
		}
	}

	// Wisdom potion expiration
	if p.WisdomTicks > 0 {
		p.WisdomTicks--
		if p.WisdomTicks <= 0 {
			p.SP -= p.WisdomSP
			p.WisdomSP = 0
			sendPlayerStatus(p.Session, p)
		}
	}

	// Pink name expiration (PK system)
	if p.PinkNameTicks > 0 {
		p.PinkNameTicks--
		if p.PinkNameTicks <= 0 {
			p.PinkName = false
		}
	}
}

// chebyshevDist returns the Chebyshev distance between two points.
func chebyshevDist(x1, y1, x2, y2 int32) int32 {
	dx := x1 - x2
	dy := y1 - y2
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return dy
	}
	return dx
}

// --- Packet helpers for spell list ---

// sendSkillList sends S_SkillList (opcode 164) — tells the client which spells the player knows.
// Uses S_SkillList format: [C length=32][32 bytes bitmask][C 0x00 terminator].
func sendSkillList(sess *net.Session, skills []*data.SkillInfo) {
	var skillSlots [32]byte
	for _, sk := range skills {
		idx := sk.SkillLevel - 1
		if idx < 0 || idx >= 32 {
			continue
		}
		skillSlots[idx] |= byte(sk.IDBitmask)
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ADD_SPELL)
	w.WriteC(byte(len(skillSlots)))
	for _, b := range skillSlots {
		w.WriteC(b)
	}
	w.WriteC(0x00)
	sess.Send(w.Bytes())
}

// sendAddSingleSkill sends S_AddSkill (opcode 164) — notifies the client a new spell was learned.
// Uses S_AddSkill format: [C pageSize][28 bytes bitmask][D 0][D 0].
func sendAddSingleSkill(sess *net.Session, skill *data.SkillInfo) {
	var skillSlots [28]byte
	idx := skill.SkillLevel - 1
	if idx < 0 || idx >= 28 {
		return
	}
	skillSlots[idx] = byte(skill.IDBitmask)

	hasLevel5to8 := idx >= 4 && idx <= 7
	hasLevel9to10 := idx >= 8 && idx <= 9

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ADD_SPELL)
	if hasLevel9to10 {
		w.WriteC(100)
	} else if hasLevel5to8 {
		w.WriteC(50)
	} else {
		w.WriteC(32)
	}
	for _, b := range skillSlots {
		w.WriteC(b)
	}
	w.WriteD(0)
	w.WriteD(0)
	sess.Send(w.Bytes())
}

// playerKnowsSpell checks if the player has learned a specific spell.
func playerKnowsSpell(player *world.PlayerInfo, skillID int32) bool {
	for _, sid := range player.KnownSpells {
		if sid == skillID {
			return true
		}
	}
	return false
}
