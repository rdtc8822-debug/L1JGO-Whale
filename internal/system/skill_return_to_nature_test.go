package system

import (
	"testing"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

func TestSkillReturnToNatureTargetsSingleSummonLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:        1,
		Session:          newSkillTestSession(t, 1),
		CharID:           1001,
		Name:             "elf",
		X:                100,
		Y:                100,
		MapID:            4,
		ShowID:           10,
		Level:            80,
		MP:               100,
		MaxMP:            100,
		Intel:            25,
		OriginalMagicHit: 100,
		KnownSpells: []int32{
			145,
		},
		Inv: world.NewInventory(),
	})
	owner := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "owner",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    10,
		Level:     1,
	})
	caster.Inv.AddItem(40319, 10, "spirit gem", 0, 0, true, 1)
	ownSummon := addReturnToNatureTestSummon(ws, 2001, caster.CharID, caster.X, caster.Y, caster.MapID, caster.ShowID, 45, 0)
	targetSummon := addReturnToNatureTestSummon(ws, 2002, owner.CharID, owner.X, owner.Y, owner.MapID, owner.ShowID, 1, 0)
	s := newElementalSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{SessionID: caster.SessionID, SkillID: 145, TargetID: targetSummon.ID})

	if ws.GetSummon(targetSummon.ID) != nil {
		t.Fatal("Java RETURN_TO_NATURE 應釋放指定召喚獸，即使該召喚獸屬於其他玩家")
	}
	if ws.GetSummon(ownSummon.ID) == nil {
		t.Fatal("Java RETURN_TO_NATURE 只處理指定目標，不應釋放施法者自己的其他召喚獸")
	}
	packets := drainSkillTestPackets(caster.Session)
	if !hasActionGfxPacket(packets, caster.CharID, 19) || !hasSkillEffectPacket(packets, targetSummon.ID, 2245) {
		t.Fatal("Java sendGrfx 會送施法者 action_id，並以指定召喚獸作為 cast_gfx 目標")
	}
}

func TestSkillReturnToNatureRejectsNonSummonTargetLikeJava(t *testing.T) {
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
		Level:     80,
		MP:        100,
		MaxMP:     100,
		KnownSpells: []int32{
			145,
		},
		Inv: world.NewInventory(),
	})
	caster.Inv.AddItem(40319, 10, "spirit gem", 0, 0, true, 1)
	ownSummon := addReturnToNatureTestSummon(ws, 2001, caster.CharID, caster.X, caster.Y, caster.MapID, caster.ShowID, 45, 0)
	npc := &world.NpcInfo{
		ID:     3001,
		NpcID:  45001,
		Name:   "not_summon",
		X:      101,
		Y:      100,
		MapID:  4,
		ShowID: 10,
	}
	ws.AddNpc(npc)
	s := newElementalSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{SessionID: caster.SessionID, SkillID: 145, TargetID: npc.ID})

	if ws.GetSummon(ownSummon.ID) == nil {
		t.Fatal("Java RETURN_TO_NATURE 非召喚目標只送 79，不應改動施法者既有召喚獸")
	}
	if !hasServerMessage(drainSkillTestPackets(caster.Session), 79) {
		t.Fatal("Java RETURN_TO_NATURE 非召喚目標會送 S_ServerMessage(79)")
	}
}

func TestSkillReturnToNatureUsesSummonMasterLevelForProbabilityLikeJava(t *testing.T) {
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
		Level:     1,
		MP:        100,
		MaxMP:     100,
		KnownSpells: []int32{
			145,
		},
		Inv: world.NewInventory(),
	})
	owner := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "owner",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    10,
		Level:     80,
	})
	caster.Inv.AddItem(40319, 10, "spirit gem", 0, 0, true, 1)
	targetSummon := addReturnToNatureTestSummon(ws, 2002, owner.CharID, owner.X, owner.Y, owner.MapID, owner.ShowID, 1, 200)
	s := newElementalSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{SessionID: caster.SessionID, SkillID: 145, TargetID: targetSummon.ID})

	if ws.GetSummon(targetSummon.ID) == nil {
		t.Fatal("Java RETURN_TO_NATURE 機率會以召喚獸主人等級作 defenseLevel；低等施法者不應穩定釋放高等主人召喚獸")
	}
}

func addReturnToNatureTestSummon(ws *world.State, id, ownerID int32, x, y int32, mapID int16, showID int32, level int16, mr int16) *world.SummonInfo {
	sum := &world.SummonInfo{
		ID:          id,
		OwnerCharID: ownerID,
		NpcID:       45065,
		GfxID:       3860,
		NameID:      "$45065",
		Name:        "summon",
		Level:       level,
		HP:          100,
		MaxHP:       100,
		MP:          10,
		MaxMP:       10,
		MR:          mr,
		X:           x,
		Y:           y,
		MapID:       mapID,
		ShowID:      showID,
	}
	ws.AddSummon(sum)
	return sum
}
