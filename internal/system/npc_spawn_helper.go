package system

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/world"
)

func newRuntimeNpcFromTemplate(tmpl *data.NpcTemplate, x, y int32, mapID, heading int16, showID int32, respawnDelay int, sprTable *data.SprTable) *world.NpcInfo {
	if tmpl == nil {
		return nil
	}
	atkSpeed := tmpl.AtkSpeed
	moveSpeed := tmpl.PassiveSpeed
	if sprTable != nil {
		gfx := int(tmpl.GfxID)
		if tmpl.AtkSpeed != 0 {
			if v := sprTable.GetAttackSpeed(gfx, data.ActAttack); v > 0 {
				atkSpeed = int16(v)
			}
		}
		if tmpl.PassiveSpeed != 0 {
			if v := sprTable.GetMoveSpeed(gfx, data.ActWalk); v > 0 {
				moveSpeed = int16(v)
			}
		}
	}
	return &world.NpcInfo{
		ID:                world.NextNpcID(),
		NpcID:             tmpl.NpcID,
		Impl:              tmpl.Impl,
		GfxID:             tmpl.GfxID,
		LightSize:         byte(tmpl.LightSize),
		Name:              tmpl.Name,
		NameID:            tmpl.NameID,
		Level:             tmpl.Level,
		X:                 x,
		Y:                 y,
		MapID:             mapID,
		ShowID:            showID,
		Heading:           heading,
		HP:                tmpl.HP,
		MaxHP:             tmpl.HP,
		MP:                tmpl.MP,
		MaxMP:             tmpl.MP,
		AC:                tmpl.AC,
		STR:               tmpl.STR,
		DEX:               tmpl.DEX,
		Intel:             tmpl.INT,
		Exp:               tmpl.Exp,
		Lawful:            tmpl.Lawful,
		Size:              tmpl.Size,
		MR:                tmpl.MR,
		Undead:            tmpl.Undead,
		UndeadType:        tmpl.UndeadType,
		TurnUndeadable:    tmpl.EffectiveTurnUndeadable(),
		TurnUndeadableSet: true,
		Hard:              tmpl.Hard,
		CantResurrect:     tmpl.CantResurrect,
		Agro:              tmpl.Agro,
		Family:            tmpl.Family,
		AgroFamily:        tmpl.AgroFamily,
		AtkDmg:            int32(tmpl.Level) + int32(tmpl.STR)/3,
		Ranged:            tmpl.Ranged,
		AtkSpeed:          atkSpeed,
		AtkMagicSpeed:     tmpl.AtkMagicSpeed,
		SubMagicSpeed:     tmpl.SubMagicSpeed,
		MoveSpeed:         moveSpeed,
		PoisonAtk:         tmpl.PoisonAtk,
		FireRes:           tmpl.FireRes,
		WaterRes:          tmpl.WaterRes,
		WindRes:           tmpl.WindRes,
		EarthRes:          tmpl.EarthRes,
		WeakAttr:          tmpl.WeakAttr,
		WeaponRequired:    tmpl.WeaponRequired,
		SpawnX:            x,
		SpawnY:            y,
		SpawnMapID:        mapID,
		RespawnDelay:      respawnDelay,
	}
}
