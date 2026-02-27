package handler

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

// MaxDollCount is the maximum number of dolls a player can have active at once.
// Java default: ConfigAlt.MAX_DOLL_COUNT = 1.
const MaxDollCount = 1

// handleUseDoll processes using a magic doll item.
// Toggle behavior: if the doll item is already active, dismiss it; otherwise summon.
func handleUseDoll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, dollDef *data.DollDef, deps *Deps) {
	ws := deps.World

	// Toggle: dismiss if this item's doll is already active
	for _, d := range ws.GetDollsByOwner(player.CharID) {
		if d.ItemObjID == invItem.ObjectID {
			dismissDoll(d, player, deps)
			return
		}
	}

	existing := ws.GetDollsByOwner(player.CharID)

	// Max doll count check
	if len(existing) >= MaxDollCount {
		sendServerMessage(sess, 319) // "你不能擁有太多的怪物。"
		return
	}

	// Java: cannot summon the same doll type twice (same itemId)
	for _, d := range existing {
		if d.DollTypeID == invItem.ItemID {
			sendServerMessage(sess, 319)
			return
		}
	}

	// Cannot summon while invisible
	if player.Invisible {
		return
	}

	// Create DollInfo
	doll := &world.DollInfo{
		ID:          world.NextNpcID(),
		OwnerCharID: player.CharID,
		ItemObjID:   invItem.ObjectID,
		DollTypeID:  dollDef.ItemID,
		GfxID:       dollDef.GfxID,
		NameID:      dollDef.NameID,
		Name:        dollDef.Name,
		X:           player.X + int32(world.RandInt(5)) - 2,
		Y:           player.Y + int32(world.RandInt(5)) - 2,
		MapID:       player.MapID,
		Heading:     player.Heading,
		TimerTicks:  dollDef.Duration * 5, // seconds → ticks (5 ticks/sec)
	}

	// Calculate bonuses from power definitions
	for _, p := range dollDef.Powers {
		switch p.Type {
		case "hp":
			doll.BonusHP += int16(p.Value)
		case "mp":
			doll.BonusMP += int16(p.Value)
		case "ac":
			doll.BonusAC += int16(p.Value)
		case "hit":
			doll.BonusHit += int16(p.Value)
		case "dmg":
			doll.BonusDmg += int16(p.Value)
		case "bow_hit":
			doll.BonusBowHit += int16(p.Value)
		case "bow_dmg":
			doll.BonusBowDmg += int16(p.Value)
		case "sp":
			doll.BonusSP += int16(p.Value)
		case "mr":
			doll.BonusMR += int16(p.Value)
		case "hpr":
			doll.BonusHPR += int16(p.Value)
		case "mpr":
			doll.BonusMPR += int16(p.Value)
		case "fire_res":
			doll.BonusFireRes += int16(p.Value)
		case "water_res":
			doll.BonusWaterRes += int16(p.Value)
		case "wind_res":
			doll.BonusWindRes += int16(p.Value)
		case "earth_res":
			doll.BonusEarthRes += int16(p.Value)
		case "dodge":
			doll.BonusDodge += int16(p.Value)
		case "str":
			doll.BonusSTR += int16(p.Value)
		case "dex":
			doll.BonusDEX += int16(p.Value)
		case "con":
			doll.BonusCON += int16(p.Value)
		case "wis":
			doll.BonusWIS += int16(p.Value)
		case "int":
			doll.BonusINT += int16(p.Value)
		case "cha":
			doll.BonusCHA += int16(p.Value)
		case "stun_resist":
			doll.BonusStunRes += int16(p.Value)
		case "freeze_resist":
			doll.BonusFreezeRes += int16(p.Value)
		case "skill":
			doll.SkillID = int32(p.Value)
			doll.SkillChance = p.Chance
		}
	}

	// Apply stat bonuses to player
	applyDollBonuses(player, doll)
	sendPlayerStatus(sess, player)

	// Register doll in world
	ws.AddDoll(doll)

	// Broadcast appearance
	masterName := player.Name
	nearby := ws.GetNearbyPlayersAt(doll.X, doll.Y, doll.MapID)
	for _, viewer := range nearby {
		SendDollPack(viewer.Session, doll, masterName)
	}
	SendDollPack(sess, doll, masterName)

	// Summon sound effect + timer UI
	sendCompanionEffect(sess, doll.ID, 5935) // summon sound
	sendDollTimer(sess, int32(dollDef.Duration))
}

// dismissDoll removes an active doll and reverses its bonuses.
func dismissDoll(doll *world.DollInfo, player *world.PlayerInfo, deps *Deps) {
	ws := deps.World

	// Reverse stat bonuses
	removeDollBonusesHandler(player, doll)

	// Remove from world
	ws.RemoveDoll(doll.ID)

	// Broadcast removal
	nearby := ws.GetNearbyPlayersAt(doll.X, doll.Y, doll.MapID)
	for _, viewer := range nearby {
		SendRemoveObject(viewer.Session, doll.ID)
	}

	// Dismiss sound + clear timer
	sendCompanionEffect(player.Session, doll.ID, 5936) // dismiss sound
	sendDollTimer(player.Session, 0)                     // clear timer
	sendPlayerStatus(player.Session, player)
}

// applyDollBonuses adds doll stat bonuses to the player.
func applyDollBonuses(player *world.PlayerInfo, doll *world.DollInfo) {
	player.AC += int16(doll.BonusAC)
	player.DmgMod += int16(doll.BonusDmg)
	player.HitMod += int16(doll.BonusHit)
	player.BowDmgMod += int16(doll.BonusBowDmg)
	player.BowHitMod += int16(doll.BonusBowHit)
	player.SP += int16(doll.BonusSP)
	player.MR += int16(doll.BonusMR)
	player.MaxHP += int16(doll.BonusHP)
	player.MaxMP += int16(doll.BonusMP)
	player.HPR += int16(doll.BonusHPR)
	player.MPR += int16(doll.BonusMPR)
	player.FireRes += int16(doll.BonusFireRes)
	player.WaterRes += int16(doll.BonusWaterRes)
	player.WindRes += int16(doll.BonusWindRes)
	player.EarthRes += int16(doll.BonusEarthRes)
	player.Dodge += int16(doll.BonusDodge)
	player.Str += int16(doll.BonusSTR)
	player.Dex += int16(doll.BonusDEX)
	player.Con += int16(doll.BonusCON)
	player.Wis += int16(doll.BonusWIS)
	player.Intel += int16(doll.BonusINT)
	player.Cha += int16(doll.BonusCHA)
}

// removeDollBonusesHandler reverses doll stat bonuses from the player (handler package version).
func removeDollBonusesHandler(player *world.PlayerInfo, doll *world.DollInfo) {
	player.AC -= int16(doll.BonusAC)
	player.DmgMod -= int16(doll.BonusDmg)
	player.HitMod -= int16(doll.BonusHit)
	player.BowDmgMod -= int16(doll.BonusBowDmg)
	player.BowHitMod -= int16(doll.BonusBowHit)
	player.SP -= int16(doll.BonusSP)
	player.MR -= int16(doll.BonusMR)
	player.MaxHP -= int16(doll.BonusHP)
	player.MaxMP -= int16(doll.BonusMP)
	player.HPR -= int16(doll.BonusHPR)
	player.MPR -= int16(doll.BonusMPR)
	player.FireRes -= int16(doll.BonusFireRes)
	player.WaterRes -= int16(doll.BonusWaterRes)
	player.WindRes -= int16(doll.BonusWindRes)
	player.EarthRes -= int16(doll.BonusEarthRes)
	player.Dodge -= int16(doll.BonusDodge)
	player.Str -= int16(doll.BonusSTR)
	player.Dex -= int16(doll.BonusDEX)
	player.Con -= int16(doll.BonusCON)
	player.Wis -= int16(doll.BonusWIS)
	player.Intel -= int16(doll.BonusINT)
	player.Cha -= int16(doll.BonusCHA)
	// Clamp HP/MP
	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	if player.MP > player.MaxMP {
		player.MP = player.MaxMP
	}
}
