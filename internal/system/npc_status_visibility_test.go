package system

import (
	"testing"

	"github.com/l1jgo/server/internal/world"
)

func TestNpcDebuffPoisonRefreshBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws, npc, sameShow, otherShow := newNpcStatusVisibilityFixture(t)
	npc.Paralyzed = true
	npc.ActiveDebuffs = map[int32]int{157: 26}

	tickNpcDebuffs(npc, ws, nil)

	if !hasPoisonPacket(drainSkillTestPackets(sameShow.Session), npc.ID, 2) {
		t.Fatal("同 ShowID 玩家應收到 NPC 凍結灰色色調刷新封包")
	}
	if hasPoisonPacket(drainSkillTestPackets(otherShow.Session), npc.ID, 2) {
		t.Fatal("yiwei NPC 狀態色調走 broadcastPacketAll，不同 ShowID 不應收到凍結刷新封包")
	}
}

func TestNpcDebuffRemoveBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws, npc, sameShow, otherShow := newNpcStatusVisibilityFixture(t)
	npc.Paralyzed = true
	npc.ActiveDebuffs = map[int32]int{157: 1}

	tickNpcDebuffs(npc, ws, nil)

	if !hasPoisonPacket(drainSkillTestPackets(sameShow.Session), npc.ID, 0) {
		t.Fatal("同 ShowID 玩家應收到 NPC 凍結解除色調封包")
	}
	if hasPoisonPacket(drainSkillTestPackets(otherShow.Session), npc.ID, 0) {
		t.Fatal("yiwei NPC 狀態解除走 broadcastPacketAll，不同 ShowID 不應收到解除色調封包")
	}
}

func TestNpcPoisonExpireBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws, npc, sameShow, otherShow := newNpcStatusVisibilityFixture(t)
	npc.PoisonDmgAmt = 5

	tickNpcPoison(npc, ws, nil)

	if !hasPoisonPacket(drainSkillTestPackets(sameShow.Session), npc.ID, 0) {
		t.Fatal("同 ShowID 玩家應收到 NPC 毒狀態清除封包")
	}
	if hasPoisonPacket(drainSkillTestPackets(otherShow.Session), npc.ID, 0) {
		t.Fatal("yiwei NPC 毒狀態清除走 broadcastPacketAll，不同 ShowID 不應收到毒狀態清除封包")
	}
}

func TestNpcPoisonDamageHpMeterBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws, npc, sameShow, otherShow := newNpcStatusVisibilityFixture(t)
	npc.ActiveDebuffs = map[int32]int{11: 5}
	npc.PoisonDmgAmt = 5
	npc.PoisonDmgTimer = 14

	tickNpcPoison(npc, ws, nil)

	if !hasHpMeterPacket(drainSkillTestPackets(sameShow.Session), npc.ID) {
		t.Fatal("同 ShowID 玩家應收到 NPC 毒傷 HP meter")
	}
	if hasHpMeterPacket(drainSkillTestPackets(otherShow.Session), npc.ID) {
		t.Fatal("yiwei NPC 毒傷 HP meter 應只給同 ShowID 玩家")
	}
}

func TestNpcPoisonDamageNpcAbsoluteBarrierBlocksDamageLikeJava(t *testing.T) {
	ws, npc, _, _ := newNpcStatusVisibilityFixture(t)
	npc.ActiveDebuffs = map[int32]int{11: 5, 78: 5}
	npc.PoisonDmgAmt = 5
	npc.PoisonDmgTimer = 14

	tickNpcPoison(npc, ws, nil)

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei L1DamagePoison 對 NPC 會走 mob.receiveDamage，ABSOLUTE_BARRIER NPC 不應扣毒傷，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
	if npc.PoisonDmgTimer != 0 {
		t.Fatalf("毒傷 tick 仍應消耗本輪計時，PoisonDmgTimer=%d want=0", npc.PoisonDmgTimer)
	}
}

func newNpcStatusVisibilityFixture(t *testing.T) (*world.State, *world.NpcInfo, *world.PlayerInfo, *world.PlayerInfo) {
	t.Helper()
	ws := world.NewState()
	sameShow := addNpcTeleportHomeTestPlayer(t, ws, 1, 1001, "same_show", 100, 3)
	otherShow := addNpcTeleportHomeTestPlayer(t, ws, 2, 1002, "other_show", 100, 8)
	npc := &world.NpcInfo{
		ID:     2001,
		Impl:   "L1Monster",
		Name:   "status_npc",
		X:      100,
		Y:      100,
		MapID:  900,
		ShowID: 3,
		HP:     100,
		MaxHP:  100,
		MP:     10,
		MaxMP:  10,
	}
	ws.AddNpc(npc)
	return ws, npc, sameShow, otherShow
}
