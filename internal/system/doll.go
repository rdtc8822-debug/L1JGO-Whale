package system

// doll.go — 魔法娃娃系統（召喚/解散/屬性加成）。
// 業務邏輯由 handler/doll.go 抽出，handler 只負責解封包 + 委派。

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

// DollSystem 實作 handler.DollManager。
type DollSystem struct {
	deps *handler.Deps
}

// NewDollSystem 建立 DollSystem。
func NewDollSystem(deps *handler.Deps) *DollSystem {
	return &DollSystem{deps: deps}
}

// UseDoll 處理使用魔法娃娃物品（切換行為：已召喚則解散，否則召喚）。
func (s *DollSystem) UseDoll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, dollDef *data.DollDef) {
	ws := s.deps.World

	// 切換：若此物品的娃娃已召喚，則解散
	for _, d := range ws.GetDollsByOwner(player.CharID) {
		if d.ItemObjID == invItem.ObjectID {
			s.DismissDoll(d, player)
			return
		}
	}

	existing := ws.GetDollsByOwner(player.CharID)

	// 最大數量檢查
	if len(existing) >= handler.MaxDollCount {
		handler.SendServerMessage(sess, 319) // "你不能擁有太多的怪物。"
		return
	}

	// Java：同類型娃娃不可重複召喚
	for _, d := range existing {
		if d.DollTypeID == invItem.ItemID {
			handler.SendServerMessage(sess, 319)
			return
		}
	}

	// 隱身中不可召喚
	if player.Invisible {
		return
	}

	// 建立 DollInfo
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
		TimerTicks:  dollDef.Duration * 5, // 秒 → ticks（5 ticks/sec）
	}

	// 計算加成
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

	// 套用屬性加成
	s.applyDollBonuses(player, doll)
	handler.SendPlayerStatus(sess, player)

	// 註冊到世界
	ws.AddDoll(doll)

	// 廣播外觀
	masterName := player.Name
	nearby := ws.GetNearbyPlayersAt(doll.X, doll.Y, doll.MapID)
	for _, viewer := range nearby {
		handler.SendDollPack(viewer.Session, doll, masterName)
	}
	handler.SendDollPack(sess, doll, masterName)

	// 召喚音效 + 計時器 UI
	handler.SendCompanionEffect(sess, doll.ID, 5935) // 召喚音效
	handler.SendDollTimer(sess, int32(dollDef.Duration))
}

// DismissDoll 解散魔法娃娃（還原加成、從世界移除、廣播）。
func (s *DollSystem) DismissDoll(doll *world.DollInfo, player *world.PlayerInfo) {
	ws := s.deps.World

	// 還原屬性加成
	s.removeDollBonuses(player, doll)

	// 從世界移除
	ws.RemoveDoll(doll.ID)

	// 廣播移除
	nearby := ws.GetNearbyPlayersAt(doll.X, doll.Y, doll.MapID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, doll.ID)
	}

	// 解散音效 + 清除計時器 + 更新狀態
	handler.SendCompanionEffect(player.Session, doll.ID, 5936) // 解散音效
	handler.SendDollTimer(player.Session, 0)                    // 清除計時器
	handler.SendPlayerStatus(player.Session, player)
}

// RemoveDollBonuses 僅還原娃娃屬性加成（不移除世界實體）。
// 供 companion_ai 到期處理使用。
func (s *DollSystem) RemoveDollBonuses(player *world.PlayerInfo, doll *world.DollInfo) {
	s.removeDollBonuses(player, doll)
}

// applyDollBonuses 套用娃娃屬性加成到玩家。
func (s *DollSystem) applyDollBonuses(player *world.PlayerInfo, doll *world.DollInfo) {
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

// removeDollBonuses 還原娃娃屬性加成。
func (s *DollSystem) removeDollBonuses(player *world.PlayerInfo, doll *world.DollInfo) {
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
	// 限制 HP/MP 不超過最大值
	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	if player.MP > player.MaxMP {
		player.MP = player.MaxMP
	}
}
