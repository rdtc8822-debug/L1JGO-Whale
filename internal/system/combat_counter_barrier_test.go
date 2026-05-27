package system

import (
	"testing"

	"github.com/l1jgo/server/internal/world"
)

func TestMeleeAttackNpcCounterBarrierReflectsPlayerAndCancelsDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "attacker",
		X:         100,
		Y:         100,
		MapID:     900,
		Level:     50,
		Str:       35,
		HP:        1000,
		MaxHP:     1000,
	})
	npc := &world.NpcInfo{
		ID:     2001,
		NpcID:  990001,
		Impl:   "L1Monster",
		Name:   "counter_barrier_npc",
		X:      101,
		Y:      100,
		MapID:  900,
		Level:  100,
		STR:    18,
		HP:     100000,
		MaxHP:  100000,
		ShowID: player.ShowID,
		Size:   "small",
		AC:     10,
		MR:     0,
	}
	npc.AddDebuff(91, 120)
	ws.AddNpc(npc)
	s := newCombatLOSTestSystem(t, ws, &fakePvPManager{})

	initialPlayerHP := player.HP
	initialNpcHP := npc.HP
	var packets [][]byte
	for i := 0; i < 100 && player.HP == initialPlayerHP; i++ {
		s.processMeleeAttack(player.SessionID, npc.ID)
		packets = append(packets, drainSkillTestPackets(player.Session)...)
	}

	expectedReflect := int32(((int(npc.STR) + int(npc.Level)) << 1) * 3 / 2)
	if player.HP != initialPlayerHP-expectedReflect {
		t.Fatalf("yiwei PC->NPC 近戰觸發 NPC COUNTER_BARRIER 時會反傷玩家 %d，HP=%d want=%d", expectedReflect, player.HP, initialPlayerHP-expectedReflect)
	}
	if npc.HP != initialNpcHP {
		t.Fatalf("yiwei 反擊屏障觸發後原始攻擊傷害歸零，NPC HP=%d want=%d", npc.HP, initialNpcHP)
	}
	if !hasSkillEffectPacket(packets, npc.ID, 10710) {
		t.Fatalf("yiwei 反擊屏障觸發時會對 NPC 廣播 S_SkillSound 10710，packets=%v", packets)
	}
	if !hasActionGfxPacket(packets, player.CharID, 2) {
		t.Fatalf("yiwei 反擊屏障觸發時會對玩家廣播 S_DoActionGFX action=2，packets=%v", packets)
	}
}
func TestMeleeAttackNpcKirtasBarrierReflectsPlayerWithoutProbabilityLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "attacker",
		X:         100,
		Y:         100,
		MapID:     900,
		Level:     100,
		Str:       35,
		HP:        1000,
		MaxHP:     1000,
	})
	npc := &world.NpcInfo{
		ID:     2001,
		NpcID:  990001,
		Impl:   "L1Monster",
		Name:   "kirtas_barrier_npc",
		X:      101,
		Y:      100,
		MapID:  900,
		Level:  1,
		STR:    18,
		HP:     100000,
		MaxHP:  100000,
		ShowID: player.ShowID,
		Size:   "small",
		AC:     10,
		MR:     0,
	}
	npc.AddDebuff(11060, 120)
	ws.AddNpc(npc)
	s := newCombatLOSTestSystem(t, ws, &fakePvPManager{})

	initialPlayerHP := player.HP
	initialNpcHP := npc.HP
	var packets [][]byte
	for i := 0; i < 100 && player.HP == initialPlayerHP; i++ {
		s.processMeleeAttack(player.SessionID, npc.ID)
		packets = append(packets, drainSkillTestPackets(player.Session)...)
	}

	expectedReflect := int32(((int(npc.STR) + int(npc.Level)) << 1) * 3 / 2)
	if player.HP != initialPlayerHP-expectedReflect {
		t.Fatalf("yiwei KIRTAS_BARRIER1(11060) 需無機率反傷玩家 %d，HP=%d want=%d", expectedReflect, player.HP, initialPlayerHP-expectedReflect)
	}
	if npc.HP != initialNpcHP {
		t.Fatalf("yiwei KIRTAS_BARRIER1(11060) 需取消原本物理傷害，NPC HP=%d want=%d", npc.HP, initialNpcHP)
	}
	if !hasSkillEffectPacket(packets, npc.ID, 10710) {
		t.Fatalf("yiwei KIRTAS_BARRIER1(11060) 需對 NPC 廣播 S_SkillSound 10710，packets=%v", packets)
	}
	if !hasActionGfxPacket(packets, player.CharID, 2) {
		t.Fatalf("yiwei KIRTAS_BARRIER1(11060) 需對玩家廣播 S_DoActionGFX action=2，packets=%v", packets)
	}
}

func TestMeleeAttackNpcKirtasBarrierThreeAbsoluteBarrierBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "attacker",
		X:         100,
		Y:         100,
		MapID:     900,
		Level:     80,
		Str:       35,
		HP:        1000,
		MaxHP:     1000,
	})
	npc := &world.NpcInfo{
		ID:     2001,
		NpcID:  81163,
		Impl:   "L1Monster",
		Name:   "kirtas_absolute_barrier_npc",
		X:      101,
		Y:      100,
		MapID:  900,
		Level:  80,
		STR:    18,
		HP:     100000,
		MaxHP:  100000,
		ShowID: player.ShowID,
		Size:   "small",
		AC:     10,
		MR:     0,
	}
	npc.AddDebuff(11058, 120)
	npc.AddDebuff(78, 120)
	ws.AddNpc(npc)
	s := newCombatLOSTestSystem(t, ws, &fakePvPManager{})

	initialPlayerHP := player.HP
	initialNpcHP := npc.HP
	s.processMeleeAttack(player.SessionID, npc.ID)

	if npc.HP != initialNpcHP {
		t.Fatalf("yiwei KIRTAS_BARRIER3(11058) 會掛 ABSOLUTE_BARRIER(78)，物攻應被 dmg0 擋下，NPC HP=%d want=%d", npc.HP, initialNpcHP)
	}
	if player.HP != initialPlayerHP {
		t.Fatalf("yiwei KIRTAS_BARRIER3(11058) 不是反擊屏障，玩家不應被反傷，HP=%d want=%d", player.HP, initialPlayerHP)
	}
}

func TestMeleeAttackNpcIceLanceBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1003,
		Name:      "attacker_ice_lance",
		X:         100,
		Y:         100,
		MapID:     900,
		Level:     80,
		Str:       35,
		HP:        1000,
		MaxHP:     1000,
	})
	npc := &world.NpcInfo{
		ID:     2003,
		NpcID:  45001,
		Impl:   "L1Monster",
		Name:   "ice_lance_npc",
		X:      101,
		Y:      100,
		MapID:  900,
		Level:  1,
		STR:    18,
		HP:     1000,
		MaxHP:  1000,
		ShowID: player.ShowID,
		Size:   "small",
		AC:     10,
		MR:     0,
	}
	npc.AddDebuff(50, 120)
	ws.AddNpc(npc)
	s := newCombatLOSTestSystem(t, ws, &fakePvPManager{})

	s.processMeleeAttack(player.SessionID, npc.ID)

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei L1AttackPc.dmg0 會以 SKM0 擋下 ICE_LANCE NPC 物理傷害，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
}

func TestRangedAttackNpcIceLanceBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:     1,
		Session:       newSkillTestSession(t, 1),
		CharID:        1004,
		Name:          "archer_ice_lance",
		X:             100,
		Y:             100,
		MapID:         900,
		Level:         80,
		Dex:           35,
		BowHitMod:     100,
		HP:            1000,
		MaxHP:         1000,
		CurrentWeapon: 20,
	})
	player.Equip.Set(world.SlotWeapon, &world.InvItem{ItemID: 190, ObjectID: 5001, Equipped: true})
	npc := &world.NpcInfo{
		ID:     2004,
		NpcID:  45001,
		Impl:   "L1Monster",
		Name:   "ranged_ice_lance_npc",
		X:      101,
		Y:      100,
		MapID:  900,
		Level:  1,
		HP:     1000,
		MaxHP:  1000,
		ShowID: player.ShowID,
		Size:   "small",
		AC:     10,
		MR:     0,
	}
	npc.AddDebuff(50, 120)
	ws.AddNpc(npc)
	s := newCombatLOSTestSystem(t, ws, &fakePvPManager{})

	for i := 0; i < 10; i++ {
		s.processRangedAttack(player.SessionID, npc.ID)
	}

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei L1AttackPc.dmg0 會以 SKM0 擋下 ICE_LANCE NPC 遠攻傷害，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
}
