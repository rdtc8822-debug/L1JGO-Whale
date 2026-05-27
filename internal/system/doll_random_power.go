package system

import "github.com/l1jgo/server/internal/world"

func applyDollRandomDamageAdd(player *world.PlayerInfo, damage int32) int32 {
	return applyDollRandomDamageAddLikeJava(player, damage, world.RandInt(100))
}

func applyDollRandomDamageAddLikeJava(player *world.PlayerInfo, damage int32, roll int) int32 {
	if player == nil || damage <= 0 || player.DollDmgAdd <= 0 || player.DollDmgAddChance <= 0 {
		return damage
	}
	if roll < 0 {
		roll = 0
	}
	if roll+1 > player.DollDmgAddChance {
		return damage
	}
	return damage + int32(player.DollDmgAdd)
}

func applyDollRandomDamageReduction(player *world.PlayerInfo, damage int32) int32 {
	return applyDollRandomDamageReductionLikeJava(player, damage, world.RandInt(100))
}

func applyDollRandomDamageReductionLikeJava(player *world.PlayerInfo, damage int32, roll int) int32 {
	if player == nil || damage <= 0 || player.DollDmgReduceRandom <= 0 || player.DollDmgReduceRandomChance <= 0 {
		return damage
	}
	if roll < 0 {
		roll = 0
	}
	if roll+1 > player.DollDmgReduceRandomChance {
		return damage
	}
	damage -= int32(player.DollDmgReduceRandom)
	if damage < 0 {
		return 0
	}
	return damage
}
