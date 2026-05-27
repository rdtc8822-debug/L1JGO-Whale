package system

import (
	"math"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

// --- 屬性常數（Java: L1Skills.ATTR_*）---
const (
	attrNone  = 0
	attrEarth = 1
	attrFire  = 2
	attrWater = 4
	attrWind  = 8
)

// processWeaponSkillProc 處理武器技能觸發。
// 在攻擊命中後呼叫，回傳額外傷害（加到物理攻擊傷害上）。
// Java: L1WeaponSkill.getWeaponSkillDamage()
func processWeaponSkillProc(player *world.PlayerInfo, npc *world.NpcInfo, weaponItemID int32, nearby []*world.PlayerInfo, deps *handler.Deps) int32 {
	if deps.WeaponSkills == nil {
		return 0
	}

	// 特殊武器優先（硬編碼，與 Java 一致）
	switch weaponItemID {
	case 124: // 巴風特魔杖 Baphomet Staff
		return processBaphometStaff(player, npc, nearby, deps)
	case 2: // 骰子匕首 Dice Dagger
		return processDiceDagger(player, npc, nearby, deps)
	case 270: // 奇古獸 Kiringku
		return processKiringku(player, npc, weaponItemID, nearby, deps)
	case 263: // 酷寒之矛 Freezing Lancer
		return processAreaSkillWeapon(player, npc, weaponItemID, nearby, deps)
	case 260: // 狂風之劍 Raging Wind
		return processAreaSkillWeapon(player, npc, weaponItemID, nearby, deps)
	case 264: // 雷雨之劍 Lightning Edge
		return processLightningEdge(player, npc, nearby, deps)
	}

	ws := deps.WeaponSkills.Get(weaponItemID)
	if ws == nil {
		return 0
	}

	// 機率檢查（Java: Random.nextInt(100) + 1 <= probability）
	chance := world.RandInt(100) + 1
	if ws.Probability < chance {
		return 0
	}

	// 凍結/絕對屏障免疫檢查
	if isNpcFrozen(npc) {
		return 0
	}

	// buff 施加（Java: skill_id != 0 → setSkillEffect）
	if ws.SkillID != 0 && ws.SkillTime > 0 {
		npc.AddDebuff(ws.SkillID, ws.SkillTime*5) // 秒→ticks
	}

	// GFX 特效廣播
	if ws.EffectID != 0 {
		effectTargetID := npc.ID
		if ws.EffectTarget == 1 {
			effectTargetID = player.CharID
		}
		if ws.ArrowType {
			// 投射物類型 → S_UseAttackSkill
			for _, viewer := range nearby {
				handler.SendUseAttackSkill(viewer.Session, player.CharID, npc.ID, 0,
					player.Heading, ws.EffectID, 6,
					player.X, player.Y, npc.X, npc.Y)
			}
		} else {
			// 普通特效 → S_SkillSound
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, effectTargetID, ws.EffectID)
			}
		}
	}

	// 計算傷害
	var damage float64
	if ws.RandomDamage > 0 {
		damage = float64(world.RandInt(ws.RandomDamage))
	}
	damage += float64(ws.FixDamage)

	// AoE 傷害（Java: area > 0 || area == -1）
	if ws.Area > 0 || ws.Area == -1 {
		processWeaponSkillAoE(player, npc, damage, ws.Area, ws.Attr, nearby, deps)
	}

	// 對主目標的 MR + 屬性減傷
	damage = calcWeaponSkillDmgReduction(player, npc, damage, ws.Attr)
	if damage < 0 {
		damage = 0
	}

	return int32(damage)
}

// processWeaponSkillAoE 處理武器技能的範圍傷害。
// 以主目標為中心，對範圍內其他 NPC 造成傷害。
// Java: L1WeaponSkill 的 area 迴圈邏輯。
func processWeaponSkillAoE(player *world.PlayerInfo, primaryTarget *world.NpcInfo, baseDmg float64, area int, attr int, nearby []*world.PlayerInfo, deps *handler.Deps) {
	processWeaponSkillAoEFrom(player, primaryTarget.X, primaryTarget.Y, primaryTarget.MapID, primaryTarget, baseDmg, area, attr, nearby, deps)
}

func processWeaponSkillAoEFrom(player *world.PlayerInfo, centerX, centerY int32, mapID int16, primaryTarget *world.NpcInfo, baseDmg float64, area int, attr int, nearby []*world.PlayerInfo, deps *handler.Deps) {
	ws := deps.World
	npcs := ws.GetNearbyNpcs(centerX, centerY, mapID)

	for _, target := range npcs {
		// 排除主目標（由主攻擊處理）和已死亡的
		if target.ID == primaryTarget.ID || target.Dead {
			continue
		}
		// 距離檢查
		dist := chebyshevDist(centerX, centerY, target.X, target.Y)
		if area > 0 && dist > int32(area) {
			continue
		}

		// 凍結免疫
		if isNpcFrozen(target) {
			continue
		}

		// 獨立計算每個目標的 MR + 屬性減傷
		dmg := calcWeaponSkillDmgReduction(player, target, baseDmg, attr)
		if dmg <= 0 {
			continue
		}

		// 副本武器需求檢查（火龍窟「必須裝備真死亡騎士烈炎之劍」）：
		// 武器技能 AoE 命中副本怪時若主武器不符 → 該目標傷害歸 0、跳過後續扣血與廣播。
		if target.WeaponRequired != 0 {
			var equippedID int32
			if wpn := player.Equip.Weapon(); wpn != nil {
				equippedID = wpn.ItemID
			}
			if !target.CanReceiveDamageFrom(equippedID) {
				continue
			}
		}

		// 扣血
		target.HP -= int32(dmg)
		if target.HP < 0 {
			target.HP = 0
		}

		// 武器技能傷害累加仇恨
		AddPlayerHateLikeJava(ws, target, player, int32(dmg))

		// 廣播受傷動畫
		for _, viewer := range nearby {
			handler.SendActionGfx(viewer.Session, target.ID, 2) // ACTION_Damage = 2
		}

		// 血量更新
		hpRatio := int16(0)
		if target.MaxHP > 0 {
			hpRatio = int16((target.HP * 100) / target.MaxHP)
		}
		for _, viewer := range nearby {
			handler.SendHpMeter(viewer.Session, target.ID, hpRatio)
		}

		// 死亡檢查
		if target.HP <= 0 {
			deps.Combat.HandleNpcDeath(target, player, nearby)
		}
	}
}

// calcWeaponSkillDmgReduction 計算武器技能的 MR 減傷 + 屬性抗性減傷。
// Java: L1WeaponSkill.calcDamageReduction()
func calcWeaponSkillDmgReduction(caster *world.PlayerInfo, target *world.NpcInfo, dmg float64, attr int) float64 {
	if dmg <= 0 {
		return 0
	}

	// 凍結/絕對屏障 → 傷害為 0
	if isNpcFrozen(target) {
		return 0
	}

	// MR 減傷（Java: L1WeaponSkill.calcDamageReduction）
	mr := int(target.MR)
	magicHit := shockStunIntMagicHit(caster.Intel) + shockStunBaseIntMagicHit(caster) + int(caster.OriginalMagicHit)
	var mrFloor float64
	if mr < 100 {
		mrFloor = math.Floor(float64(mr-magicHit) / 2)
	} else {
		mrFloor = math.Floor(float64(mr-magicHit) / 10)
	}
	var mrCoeff float64
	if mr < 100 {
		mrCoeff = 1.0 - 0.01*mrFloor
	} else {
		mrCoeff = 0.6 - 0.01*mrFloor
	}
	if mrCoeff < 0 {
		mrCoeff = 0
	}
	dmg *= mrCoeff

	// 屬性抗性減傷（Java: resist → resistFloor → attrDeffence）
	var resist int
	switch attr {
	case attrEarth:
		resist = int(target.EarthRes)
	case attrFire:
		resist = int(target.FireRes)
	case attrWater:
		resist = int(target.WaterRes)
	case attrWind:
		resist = int(target.WindRes)
	}
	if resist != 0 {
		resistFloor := int(0.16 * math.Abs(float64(resist)))
		if resist < 0 {
			resistFloor = -resistFloor
		}
		attrDefence := float64(resistFloor) / 32.0
		dmg *= (1.0 - attrDefence)
	}

	return dmg
}

// isNpcFrozen 檢查 NPC 是否處於凍結/絕對屏障狀態（武器技能無效）。
// Java: L1WeaponSkill.isFreeze()
func isNpcFrozen(npc *world.NpcInfo) bool {
	// 凍結（ICE_LANCE=50, FREEZING_BLIZZARD=80, EARTH_BIND=157）
	if npc.HasDebuff(50) || npc.HasDebuff(80) || npc.HasDebuff(157) {
		return true
	}
	// 絕對屏障（ABSOLUTE_BARRIER=78）— NPC 不常用但保留檢查
	if npc.HasDebuff(78) {
		return true
	}
	return false
}

// --- 特殊武器處理 ---

// processBaphometStaff 巴風特魔杖（item 124）：14% 觸發，(INT+SP)*1.8 傷害，地屬性。
// Java: L1WeaponSkill.getBaphometStaffDamage()
func processBaphometStaff(player *world.PlayerInfo, npc *world.NpcInfo, nearby []*world.PlayerInfo, deps *handler.Deps) int32 {
	if world.RandInt(100)+1 > 14 {
		return 0
	}
	if isNpcFrozen(npc) {
		return 0
	}

	sp := calcPlayerSPLikeJava(player)
	intel := int(player.Intel)
	bsk := 0.0
	if player.HasBuff(55) { // Berserker
		bsk = 0.2
	}
	dmg := float64(intel+sp)*(1.8+bsk) + float64(world.RandInt(intel+sp))*1.8

	// GFX: 129 播在目標位置
	for _, viewer := range nearby {
		handler.SendSkillEffect(viewer.Session, npc.ID, 129)
	}

	dmg = calcWeaponSkillDmgReduction(player, npc, dmg, attrEarth)
	if dmg < 0 {
		dmg = 0
	}
	return int32(dmg)
}

// processDiceDagger 骰子匕首（item 2）：2% 觸發，目標 HP*2/3 傷害，消耗武器。
// Java: L1WeaponSkill.getDiceDaggerDamage()
func processDiceDagger(player *world.PlayerInfo, npc *world.NpcInfo, nearby []*world.PlayerInfo, deps *handler.Deps) int32 {
	if world.RandInt(100)+1 > 2 {
		return 0
	}

	// 消耗武器（Java: removeItem(weapon, 1)）
	wpn := player.Equip.Weapon()
	if wpn != nil {
		player.Inv.RemoveItem(wpn.ObjectID, 1)
		handler.SendRemoveInventoryItem(player.Session, wpn.ObjectID)
		player.Equip.Set(world.SlotWeapon, nil)
		handler.SendServerMessage(player.Session, 158) // "%0 消失了。"
	}

	// yiwei W_SK001 只對 L1PcInstance 套用毀滅性傷害；NPC 目標只會觸發武器消失。
	return 0
}

// processKiringku 奇鍛古（item 270/290）：固定觸發，2D5+value * INT 係數。
// Java: L1WeaponSkill.getKiringkuDamage()
func processKiringku(player *world.PlayerInfo, npc *world.NpcInfo, weaponItemID int32, nearby []*world.PlayerInfo, deps *handler.Deps) int32 {
	if isNpcFrozen(npc) {
		return 0
	}

	value := 14
	gfx := int32(7049)
	if weaponItemID == 270 {
		value = 16
		gfx = 6983
	}

	// 2D5 + value
	kiringkuDmg := 0
	for i := 0; i < 2; i++ {
		kiringkuDmg += world.RandInt(5) + 1
	}
	kiringkuDmg += value

	// INT 係數
	spByItem := int(player.SP)
	charaIntel := int(player.Intel) + spByItem - 12
	if charaIntel < 1 {
		charaIntel = 1
	}
	coeffA := 1.0 + float64(charaIntel)*3.0/32.0
	dmg := float64(kiringkuDmg) * coeffA

	// 強化等級加成
	wpn := player.Equip.Weapon()
	if wpn != nil {
		dmg += float64(wpn.EnchantLvl)
	}

	// Illusion Avatar 加成
	if player.HasBuff(219) {
		dmg += 10
	}

	// GFX
	for _, viewer := range nearby {
		handler.SendSkillEffect(viewer.Session, player.CharID, gfx)
	}

	dmg = calcWeaponSkillDmgReduction(player, npc, dmg, attrNone)
	if dmg < 0 {
		dmg = 0
	}
	return int32(dmg)
}

// processLightningEdge 雷雨之劍（item 264）：4% 觸發，INT/SP 係數傷害，風屬性。
func processLightningEdge(player *world.PlayerInfo, npc *world.NpcInfo, nearby []*world.PlayerInfo, deps *handler.Deps) int32 {
	if world.RandInt(100)+1 > 4 {
		return 0
	}
	if isNpcFrozen(npc) {
		return 0
	}

	sp := calcPlayerSPLikeJava(player)
	intel := int(player.Intel)
	if intel+sp <= 0 {
		return 0
	}
	dmg := float64(intel+sp)*2.0 + float64(world.RandInt(intel+sp))*2.0

	for _, viewer := range nearby {
		handler.SendSkillEffect(viewer.Session, npc.ID, 10)
	}

	dmg = calcWeaponSkillDmgReduction(player, npc, dmg, attrWind)
	if dmg < 0 {
		dmg = 0
	}
	return int32(dmg)
}

// processAreaSkillWeapon 冰矛/狂風之劍等特殊 AoE 武器。
// Java: L1WeaponSkill.getAreaSkillWeaponDamage()
func processAreaSkillWeapon(player *world.PlayerInfo, npc *world.NpcInfo, weaponItemID int32, nearby []*world.PlayerInfo, deps *handler.Deps) int32 {
	var probability int
	var attr int
	var area int
	var damageRate float64
	var effectID int32

	switch weaponItemID {
	case 263: // 酷寒之矛 Freezing Lancer
		probability = 5
		attr = attrWater
		area = 3
		damageRate = 1.4
		effectID = 1804
	case 260: // 狂風之劍 Raging Wind
		probability = 4
		attr = attrWind
		area = 4
		damageRate = 1.5
		effectID = 758
	default:
		return 0
	}

	if world.RandInt(100)+1 > probability {
		return 0
	}
	if isNpcFrozen(npc) {
		return 0
	}

	sp := calcPlayerSPLikeJava(player)
	intel := int(player.Intel)
	bsk := 0.0
	if player.HasBuff(55) { // Berserker
		bsk = 0.2
	}
	dmg := float64(intel+sp)*(damageRate+bsk) + float64(world.RandInt(intel+sp))*damageRate

	// GFX
	effectTargetID := npc.ID
	if weaponItemID == 260 {
		effectTargetID = player.CharID
	}
	for _, viewer := range nearby {
		handler.SendSkillEffect(viewer.Session, effectTargetID, effectID)
	}

	// AoE 傷害
	centerX, centerY, mapID := npc.X, npc.Y, npc.MapID
	if weaponItemID == 260 {
		centerX, centerY, mapID = player.X, player.Y, player.MapID
	}
	processWeaponSkillAoEFrom(player, centerX, centerY, mapID, npc, dmg, area, attr, nearby, deps)

	// 主目標減傷
	dmg = calcWeaponSkillDmgReduction(player, npc, dmg, attr)
	if dmg < 0 {
		dmg = 0
	}
	return int32(dmg)
}
