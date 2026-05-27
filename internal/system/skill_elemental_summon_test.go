package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func newElementalSummonTestSystem(t *testing.T, ws *world.State) *SkillSystem {
	t.Helper()
	skills, err := data.LoadSkillTable("../../data/yaml/skill_list.yaml")
	if err != nil {
		t.Fatalf("讀取技能資料失敗: %v", err)
	}
	npcs, err := data.LoadNpcTable("../../data/yaml/npc_list.yaml")
	if err != nil {
		t.Fatalf("讀取 NPC 資料失敗: %v", err)
	}
	engine, err := scripting.NewEngine("../../scripts", zap.NewNop())
	if err != nil {
		t.Fatalf("建立 Lua engine 失敗: %v", err)
	}
	deps := &handler.Deps{
		World:     ws,
		Skills:    skills,
		Npcs:      npcs,
		Scripting: engine,
		Log:       zap.NewNop(),
	}
	skillSys := &SkillSystem{deps: deps}
	summonSys := NewSummonSystem(deps)
	deps.Skill = skillSys
	deps.Summon = summonSys
	return skillSys
}

func TestSkillElementalSummonElementalSummonUsesElfAttr(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "elf",
		X:         100,
		Y:         100,
		MapID:     4,
		MP:        100,
		MaxMP:     100,
		Cha:       12,
		ElfAttr:   2,
		KnownSpells: []int32{
			154,
		},
		Inv: world.NewInventory(),
	})
	caster.Inv.AddItem(40319, 10, "spirit gem", 0, 0, true, 1)
	s := newElementalSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{SessionID: caster.SessionID, SkillID: 154})

	summons := ws.GetSummonsByOwner(caster.CharID)
	if len(summons) != 1 {
		t.Fatalf("召喚屬性精靈應建立一隻召喚獸，count=%d", len(summons))
	}
	if summons[0].NpcID != 45303 || summons[0].PetCost != int(caster.Cha)+7 {
		t.Fatalf("火屬性小精靈應使用 NPC 45303 且 petcost=CHA+7，npc=%d petcost=%d", summons[0].NpcID, summons[0].PetCost)
	}
}

func TestSkillElementalSummonBroadcastsCastVisualLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "elf",
		X:         100,
		Y:         100,
		MapID:     4,
		ShowID:    10,
		MP:        100,
		MaxMP:     100,
		Cha:       12,
		ElfAttr:   2,
		KnownSpells: []int32{
			154,
		},
		Inv: world.NewInventory(),
	})
	sameShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "same_show",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    10,
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 3,
		Session:   newSkillTestSession(t, 3),
		CharID:    1003,
		Name:      "other_show",
		X:         102,
		Y:         100,
		MapID:     4,
		ShowID:    99,
	})
	caster.Inv.AddItem(40319, 10, "spirit gem", 0, 0, true, 1)
	s := newElementalSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{SessionID: caster.SessionID, SkillID: 154})

	casterPackets := drainSkillTestPackets(caster.Session)
	if !hasActionGfxPacket(casterPackets, caster.CharID, 19) {
		t.Fatal("義維 sendGrfx 成功路徑會讓施法者看到元素召喚施法動作")
	}
	if !hasSkillEffectPacket(casterPackets, caster.CharID, 2510) {
		t.Fatal("義維 sendGrfx 成功路徑會讓施法者看到元素召喚 cast_gfx")
	}
	samePackets := drainSkillTestPackets(sameShow.Session)
	if !hasActionGfxPacket(samePackets, caster.CharID, 19) || !hasSkillEffectPacket(samePackets, caster.CharID, 2510) {
		t.Fatal("同 ShowID 觀眾應看到元素召喚施法動作與 cast_gfx")
	}
	otherPackets := drainSkillTestPackets(otherShow.Session)
	if hasActionGfxPacket(otherPackets, caster.CharID, 19) || hasSkillEffectPacket(otherPackets, caster.CharID, 2510) {
		t.Fatal("不同 ShowID 觀眾不應看到元素召喚施法動作或 cast_gfx")
	}
}

func TestSkillElementalSummonGreaterElementalUsesElfAttr(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "elf",
		X:         100,
		Y:         100,
		MapID:     4,
		MP:        100,
		MaxMP:     100,
		Cha:       10,
		ElfAttr:   4,
		KnownSpells: []int32{
			162,
		},
		Inv: world.NewInventory(),
	})
	caster.Inv.AddItem(40319, 10, "spirit gem", 0, 0, true, 1)
	s := newElementalSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{SessionID: caster.SessionID, SkillID: 162})

	summons := ws.GetSummonsByOwner(caster.CharID)
	if len(summons) != 1 {
		t.Fatalf("召喚強力屬性精靈應建立一隻召喚獸，count=%d", len(summons))
	}
	if summons[0].NpcID != 81051 || summons[0].PetCost != int(caster.Cha)+7 {
		t.Fatalf("水屬性強力精靈應使用 NPC 81051 且 petcost=CHA+7，npc=%d petcost=%d", summons[0].NpcID, summons[0].PetCost)
	}
}

func TestSkillElementalSummonAreaOfSilenceAppliesSilenceToNearbyPlayers(t *testing.T) {
	disablePlayerDebuffMRForStatusTest(t, 161)
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
	})
	target := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "target",
		X:         101,
		Y:         100,
		MapID:     4,
	})
	far := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 3,
		Session:   newSkillTestSession(t, 3),
		CharID:    1003,
		Name:      "far",
		X:         200,
		Y:         200,
		MapID:     4,
	})
	s := newElementalSummonTestSystem(t, ws)
	skill := &data.SkillInfo{SkillID: 161, BuffDuration: 16, Area: -1, ActionID: 19, CastGfx: 2241}

	s.executeSelfSkill(caster.Session, caster, skill)

	if caster.Silenced || caster.HasBuff(161) {
		t.Fatalf("封印禁地不應沉默施法者，silenced=%v buff=%v", caster.Silenced, caster.GetBuff(161))
	}
	if !target.Silenced || !target.HasBuff(161) {
		t.Fatalf("封印禁地應沉默附近玩家，silenced=%v buff=%v", target.Silenced, target.GetBuff(161))
	}
	if far.Silenced || far.HasBuff(161) {
		t.Fatalf("封印禁地不應影響視野外玩家，silenced=%v buff=%v", far.Silenced, far.GetBuff(161))
	}
	s.removeBuffAndRevert(target, 161)
	if target.Silenced {
		t.Fatalf("封印禁地移除後應解除沉默")
	}
}

func TestSkillElementalSummonCounterMirrorReflectsMagicDamage(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
	})
	target := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "mirror",
		X:         101,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		Wis:       18,
	})
	target.AddBuff(&world.ActiveBuff{SkillID: 134, TicksLeft: 16 * 5})
	s := newElementalSummonTestSystem(t, ws)

	damage := s.applyCounterMirrorMagicDamage(caster, target, 30, 0, []*world.PlayerInfo{caster, target})

	if damage != 0 {
		t.Fatalf("鏡反射觸發後原傷害應歸零，damage=%d", damage)
	}
	if caster.HP != 70 || target.HP != 100 {
		t.Fatalf("鏡反射應把傷害反彈給施法者，casterHP=%d targetHP=%d", caster.HP, target.HP)
	}
	if target.HasBuff(134) {
		t.Fatalf("鏡反射觸發後應移除 buff")
	}
}
