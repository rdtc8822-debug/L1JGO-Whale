package system

import (
	"testing"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestItemUseLightningWandSkipsNpcBehindWallLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    91010,
		Name:      "wand_user",
		X:         100,
		Y:         100,
		MapID:     900,
		HP:        100,
		MaxHP:     100,
		Intel:     20,
	})
	npc := &world.NpcInfo{
		ID:    92010,
		NpcID: 45161,
		Impl:  "L1Monster",
		Name:  "wall_target",
		X:     103,
		Y:     100,
		MapID: 900,
		HP:    100,
		MaxHP: 100,
	}
	ws.AddNpc(npc)
	wand := &world.InvItem{ObjectID: 93010, ItemID: 40007, ChargeCount: 3}
	sys := NewItemUseSystem(&handler.Deps{
		World:   ws,
		MapData: newSkillLOSTestMap(t),
		Log:     zap.NewNop(),
	})

	sys.UseWand(player.Session, player, wand, npc.ID, int16(npc.X), int16(npc.Y))

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei Lightning_Magic_Wand.doWandAction 會以 glanceCheck 擋下隔牆 NPC，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
	if wand.ChargeCount != 2 {
		t.Fatalf("yiwei execute 即使 doWandAction 早退仍會扣魔杖次數，ChargeCount=%d want=2", wand.ChargeCount)
	}
}

func TestItemUseLightningWandNpcAbsoluteBarrierBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    91020,
		Name:      "wand_user_barrier",
		X:         100,
		Y:         100,
		MapID:     900,
		HP:        100,
		MaxHP:     100,
		Intel:     20,
	})
	npc := &world.NpcInfo{
		ID:    92020,
		NpcID: 45161,
		Impl:  "L1Monster",
		Name:  "barrier_target",
		X:     101,
		Y:     100,
		MapID: 900,
		HP:    100,
		MaxHP: 100,
	}
	npc.AddDebuff(78, 100)
	ws.AddNpc(npc)
	wand := &world.InvItem{ObjectID: 93020, ItemID: 40007, ChargeCount: 3}
	sys := NewItemUseSystem(&handler.Deps{
		World:   ws,
		MapData: newSkillLOSTestMap(t),
		Log:     zap.NewNop(),
	})

	sys.UseWand(player.Session, player, wand, npc.ID, int16(npc.X), int16(npc.Y))

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei L1MonsterInstance.receiveDamage 會以 SKM0 擋下 ABSOLUTE_BARRIER NPC 魔杖傷害，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
	if wand.ChargeCount != 2 {
		t.Fatalf("魔杖命中 ABSOLUTE_BARRIER NPC 仍應扣次數，ChargeCount=%d want=2", wand.ChargeCount)
	}
}
