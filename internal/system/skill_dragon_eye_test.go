package system

import (
	"testing"
	"time"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestSkillBuffDragonEyesCancelExistingEyeLikeYiwei(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "dragon-eye-buff",
		Level:     80,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
	})
	s := newSkillBuffTestSystem(t, ws)

	s.applyBuffEffect(player, &data.SkillInfo{SkillID: 6683, BuffDuration: 600})
	s.applyBuffEffect(player, &data.SkillInfo{SkillID: 6684, BuffDuration: 600})

	if player.HasBuff(6683) {
		t.Fatal("yiwei L1BuffUtil.cancelDragon 會先移除既有魔眼，6683 不應殘留")
	}
	if !player.HasBuff(6684) {
		t.Fatal("地龍魔眼 6684 應成為唯一作用中的魔眼")
	}
	if player.DmgMod != 0 || player.RegistStun != 0 {
		t.Fatalf("火龍魔眼加成應被回復，DmgMod=%d RegistStun=%d", player.DmgMod, player.RegistStun)
	}
	if player.RegistStone != 3 || player.Dodge != 1 {
		t.Fatalf("地龍魔眼加成錯誤，RegistStone=%d Dodge=%d", player.RegistStone, player.Dodge)
	}
}

func TestItemUseDragonEyeAppliesBuffWithoutConsumingAndRecordsLastUsedLikeYiwei(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "dragon-eye-item",
		X:         100,
		Y:         100,
		MapID:     4,
		Level:     80,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
	})
	sys := newDragonEyeItemUseSystem(t, ws)
	eye := player.Inv.AddItemWithID(7701, 47020, 1, "火龍之魔眼", 3644, 10000, false, 1)
	info := &data.ItemInfo{ItemID: 47020, Name: "火龍之魔眼", DelayEffect: 3600}

	used := sys.UseConsumable(player.Session, player, eye, info)

	if !used {
		t.Fatal("火龍魔眼應可透過 Go ItemUseSystem 套用")
	}
	if item := player.Inv.FindByObjectID(eye.ObjectID); item == nil || item.Count != 1 {
		t.Fatalf("魔眼是可重複使用道具，不應被消耗，item=%+v", item)
	}
	if !player.HasBuff(6683) || player.DmgMod != 2 || player.RegistStun != 3 {
		t.Fatalf("火龍魔眼 buff 未正確套用，has=%v DmgMod=%d RegistStun=%d", player.HasBuff(6683), player.DmgMod, player.RegistStun)
	}
	if eye.LastUsedAt == 0 {
		t.Fatal("yiwei 會保存 last_used 供 3600 秒循環時間判斷，Go 應記錄 LastUsedAt")
	}
	if !hasSkillEffectPacket(drainSkillTestPackets(player.Session), player.CharID, 7674) {
		t.Fatal("火龍魔眼使用時應播放 yiwei S_SkillSound 7674")
	}
}

func TestItemUseDragonEyeDelayEffectBlocksReuseLikeYiwei(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "dragon-eye-delay",
		Level:     80,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
	})
	sys := newDragonEyeItemUseSystem(t, ws)
	eye := player.Inv.AddItemWithID(7701, 47020, 1, "火龍之魔眼", 3644, 10000, false, 1)
	eye.LastUsedAt = time.Now().Unix()
	info := &data.ItemInfo{ItemID: 47020, Name: "火龍之魔眼", DelayEffect: 3600}

	used := sys.UseConsumable(player.Session, player, eye, info)

	if used {
		t.Fatal("魔眼仍在 delay_effect 期間時不應再次套用")
	}
	if player.HasBuff(6683) {
		t.Fatal("冷卻中使用魔眼不應新增 buff")
	}
	if eye.LastUsedAt == 0 {
		t.Fatal("冷卻中的使用嘗試不應清掉既有 LastUsedAt")
	}
}

func TestDragonEyeMagicDamageReductionHalvesDamageAndBroadcastsLikeYiwei(t *testing.T) {
	ws := world.NewState()
	target := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "dragon-eye-magic-reduce",
		X:         100,
		Y:         100,
		MapID:     4,
	})
	target.AddBuff(&world.ActiveBuff{SkillID: 6685, TicksLeft: 600})

	damage := applyDragonEyeMagicDamageReductionLikeYiwei(target, 41, 9, []*world.PlayerInfo{target})

	if damage != 20 {
		t.Fatalf("yiwei 魔眼魔法減傷觸發時應做整數除 2，got=%d", damage)
	}
	if !hasSkillEffectPacket(drainSkillTestPackets(target.Session), target.CharID, 6320) {
		t.Fatal("yiwei 魔眼魔法減傷觸發時應廣播 S_SkillSound 6320")
	}
}

func TestDragonEyeMagicDamageReductionOnlyCertainEyesAndRollBelowTenLikeYiwei(t *testing.T) {
	ws := world.NewState()
	target := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "dragon-eye-magic-gate",
		X:         100,
		Y:         100,
		MapID:     4,
	})

	target.AddBuff(&world.ActiveBuff{SkillID: 6686, TicksLeft: 600})
	if damage := applyDragonEyeMagicDamageReductionLikeYiwei(target, 40, 0, []*world.PlayerInfo{target}); damage != 40 {
		t.Fatalf("yiwei 風龍之魔眼 6686 不在魔法減傷清單，got=%d", damage)
	}
	target.RemoveBuff(6686)

	target.AddBuff(&world.ActiveBuff{SkillID: 6689, TicksLeft: 600})
	if damage := applyDragonEyeMagicDamageReductionLikeYiwei(target, 40, 10, []*world.PlayerInfo{target}); damage != 40 {
		t.Fatalf("yiwei 魔眼魔法減傷機率為 random(100) < 10，roll=10 不應觸發，got=%d", damage)
	}
}

func newDragonEyeItemUseSystem(t *testing.T, ws *world.State) *ItemUseSystem {
	t.Helper()
	engine, err := scripting.NewEngine("../../scripts", zap.NewNop())
	if err != nil {
		t.Fatalf("建立 Lua engine 失敗: %v", err)
	}
	t.Cleanup(engine.Close)
	return NewItemUseSystem(&handler.Deps{
		World:     ws,
		Scripting: engine,
		Log:       zap.NewNop(),
	})
}
