package system

import (
	"math/rand"
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

func TestWeaponSkillDamageReductionUsesJavaMagicHitNotSP(t *testing.T) {
	caster := &world.PlayerInfo{
		Intel:            23,
		OriginalMagicHit: 20,
		SP:               0,
	}
	target := &world.NpcInfo{MR: 80}

	got := calcWeaponSkillDmgReduction(caster, target, 100, attrNone)
	if int(got) != 71 {
		t.Fatalf("武器魔法 MR 減傷應使用 INT 魔法命中 + OriginalMagicHit，而不是額外 SP；got=%.2f want=71", got)
	}
}

func TestRagingWindWeaponSkillAreaUsesCasterCenterLikeJava(t *testing.T) {
	rand.Seed(9)
	ws := world.NewState()
	player := &world.PlayerInfo{
		SessionID: 1,
		CharID:    1001,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		Level:     52,
		ClassType: 3,
		Intel:     23,
		SP:        5,
	}
	primary := &world.NpcInfo{
		ID:    2001,
		NpcID: 45001,
		Name:  "primary",
		X:     104,
		Y:     100,
		MapID: 4,
		HP:    1000,
		MaxHP: 1000,
	}
	nearCasterOnly := &world.NpcInfo{
		ID:    2002,
		NpcID: 45001,
		Name:  "near_caster",
		X:     96,
		Y:     100,
		MapID: 4,
		HP:    1000,
		MaxHP: 1000,
	}
	ws.AddNpc(primary)
	ws.AddNpc(nearCasterOnly)

	processAreaSkillWeapon(player, primary, 260, nil, &handler.Deps{World: ws})

	if nearCasterOnly.HP >= 1000 {
		t.Fatalf("yiwei L1WeaponSkill weaponId=260 以施法者為 AoE 中心，玩家 4 格內的次要 NPC 應被狂風之劍波及，HP=%d", nearCasterOnly.HP)
	}
}

func TestDiceDaggerWeaponSkillDoesNotDamageNpcLikeYiweiWSK001(t *testing.T) {
	rand.Seed(2)
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "dice_dagger_user",
	})
	npc := &world.NpcInfo{
		ID:    2001,
		NpcID: 45001,
		Impl:  "L1Monster",
		Name:  "dice_dagger_npc",
		HP:    900,
		MaxHP: 900,
	}

	triggered := false
	for i := 0; i < 1000; i++ {
		weapon := player.Inv.AddItemWithID(int32(7000+i), 2, 1, "骰子匕首", 0, 0, false, 1)
		player.Equip.Set(world.SlotWeapon, weapon)

		dmg := processDiceDagger(player, npc, nil, nil)
		removed := true
		for _, item := range player.Inv.Items {
			if item.ObjectID == weapon.ObjectID {
				removed = false
				break
			}
		}
		if removed {
			triggered = true
			if dmg != 0 {
				t.Fatalf("yiwei W_SK001 對 NPC 不套毀滅性傷害，dmg=%d want=0", dmg)
			}
			break
		}
		if dmg != 0 {
			t.Fatalf("骰子匕首未觸發時不應回傳傷害，dmg=%d", dmg)
		}
		player.Inv.RemoveItem(weapon.ObjectID, 1)
	}

	if !triggered {
		t.Fatal("測試未觸發骰子匕首機率，無法驗證 yiwei W_SK001 NPC 目標傷害")
	}
}

func TestIceQueenStaffUsesWeaponSkillYamlNotBaphometHardcodeLikeJava(t *testing.T) {
	rand.Seed(4)
	weaponSkills, err := data.LoadWeaponSkillTable("../../data/yaml/weapon_skill.yaml")
	if err != nil {
		t.Fatalf("載入 weapon_skill.yaml 失敗: %v", err)
	}
	player := &world.PlayerInfo{
		CharID: 1001,
		Name:   "ice_queen_staff_user",
		Intel:  0,
		SP:     0,
	}
	npc := &world.NpcInfo{
		ID:    2001,
		NpcID: 45001,
		Impl:  "L1Monster",
		Name:  "ice_queen_staff_npc",
		HP:    1000,
		MaxHP: 1000,
		MR:    0,
	}
	deps := &handler.Deps{WeaponSkills: weaponSkills}

	for i := 0; i < 200; i++ {
		if dmg := processWeaponSkillProc(player, npc, 121, nil, deps); dmg > 0 {
			return
		}
	}

	t.Fatal("yiwei weaponId=121 冰之女王魔杖應走 weapon_skill YAML；若被巴風特 hardcode 攔截，低 INT/SP 會永遠沒有傷害")
}

func TestYaowuTwoHandSwordUsesWeaponSkillYamlNotKiringkuLikeJava(t *testing.T) {
	rand.Seed(4)
	weaponSkills, err := data.LoadWeaponSkillTable("../../data/yaml/weapon_skill.yaml")
	if err != nil {
		t.Fatalf("載入 weapon_skill.yaml 失敗: %v", err)
	}
	player := &world.PlayerInfo{
		CharID: 1001,
		Name:   "yaowu_user",
		Intel:  0,
		SP:     0,
	}
	npc := &world.NpcInfo{
		ID:    2001,
		NpcID: 45001,
		Impl:  "L1Monster",
		Name:  "yaowu_npc",
		HP:    1000,
		MaxHP: 1000,
		MR:    0,
	}
	deps := &handler.Deps{WeaponSkills: weaponSkills}

	for i := 0; i < 200; i++ {
		dmg := processWeaponSkillProc(player, npc, 290, nil, deps)
		if dmg > 0 && dmg < 65 {
			t.Fatalf("yiwei weaponId=290 耀武雙手劍應走 weapon_skill YAML 固定傷害，不應走奇古獸公式，dmg=%d", dmg)
		}
		if dmg >= 65 {
			return
		}
	}

	t.Fatal("yiwei weaponId=290 耀武雙手劍應走 weapon_skill YAML；測試未觸發 YAML 傷害")
}

func TestIceBowDoesNotUseFreezingLancerAreaSkillLikeJava(t *testing.T) {
	rand.Seed(4)
	weaponSkills, err := data.LoadWeaponSkillTable("../../data/yaml/weapon_skill.yaml")
	if err != nil {
		t.Fatalf("載入 weapon_skill.yaml 失敗: %v", err)
	}
	player := &world.PlayerInfo{
		CharID: 1001,
		Name:   "ice_bow_user",
		X:      100,
		Y:      100,
		MapID:  4,
		Intel:  23,
		SP:     5,
	}
	ws := world.NewState()
	npc := &world.NpcInfo{
		ID:    2001,
		NpcID: 45001,
		Impl:  "L1Monster",
		Name:  "ice_bow_npc",
		X:     101,
		Y:     100,
		MapID: 4,
		HP:    1000,
		MaxHP: 1000,
		MR:    0,
	}
	ws.AddNpc(npc)
	deps := &handler.Deps{World: ws, WeaponSkills: weaponSkills}

	for i := 0; i < 200; i++ {
		if dmg := processWeaponSkillProc(player, npc, 287, nil, deps); dmg != 0 {
			t.Fatalf("yiwei weaponId=287 玄冰弓不在酷寒之矛特殊分支，也沒有 YAML 武器技能，dmg=%d want=0", dmg)
		}
	}
}

func TestLightningEdgeUsesYiweiSpecialWeaponSkill(t *testing.T) {
	rand.Seed(4)
	weaponSkills, err := data.LoadWeaponSkillTable("../../data/yaml/weapon_skill.yaml")
	if err != nil {
		t.Fatalf("載入 weapon_skill.yaml 失敗: %v", err)
	}
	player := &world.PlayerInfo{
		CharID: 1001,
		Name:   "lightning_edge_user",
		Intel:  23,
		SP:     5,
	}
	npc := &world.NpcInfo{
		ID:    2001,
		NpcID: 45001,
		Impl:  "L1Monster",
		Name:  "lightning_edge_npc",
		HP:    1000,
		MaxHP: 1000,
		MR:    0,
	}
	deps := &handler.Deps{WeaponSkills: weaponSkills}

	for i := 0; i < 200; i++ {
		if dmg := processWeaponSkillProc(player, npc, 264, nil, deps); dmg > 0 {
			return
		}
	}

	t.Fatal("yiwei weaponId=264 雷雨之劍應走 getLightningEdgeDamage 特例，Go 不應因 YAML 無資料而永遠不觸發")
}
