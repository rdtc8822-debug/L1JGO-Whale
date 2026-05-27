package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestSkillStormWalkUsesJavaBraveConflictsWithoutRemovingStrengthBuff(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:  1,
		Session:    newSkillTestSession(t, 1),
		CharID:     1001,
		Name:       "storm-walk",
		X:          100,
		Y:          100,
		MapID:      4,
		Str:        23,
		BraveSpeed: 1,
	})
	for _, skillID := range []int32{
		52,  // HOLY_WALK
		101, // MOVING_ACCELERATION
		150, // WIND_WALK
		155, // FIRE_BLESS
		186, // BLOODLUST
		handler.SkillStatusBrave,
		handler.SkillStatusElfBrave,
	} {
		player.AddBuff(&world.ActiveBuff{
			SkillID:       skillID,
			TicksLeft:     100,
			SetBraveSpeed: 1,
		})
	}
	player.AddBuff(&world.ActiveBuff{
		SkillID:   42, // PHYSICAL_ENCHANT_STR, not HOLY_WALK.
		TicksLeft: 100,
		DeltaStr:  5,
	})

	s := newSkillTestSystem(t, ws)
	s.executeSelfSkill(player.Session, player, &data.SkillInfo{
		SkillID:  172,
		ActionID: 19,
	})

	for _, skillID := range []int32{
		52,
		101,
		150,
		155,
		186,
		handler.SkillStatusBrave,
		handler.SkillStatusElfBrave,
	} {
		if player.HasBuff(skillID) {
			t.Fatalf("STORM_WALK 應清除 Java 速度互斥 buff %d", skillID)
		}
	}
	if !player.HasBuff(42) {
		t.Fatal("STORM_WALK 不應移除 42 PHYSICAL_ENCHANT_STR")
	}
	if player.Str != 23 {
		t.Fatalf("STORM_WALK 不應回退 STR buff 數值，Str=%d", player.Str)
	}
	if !player.HasBuff(172) || player.BraveSpeed != 4 || player.BraveTicks != 300*5 {
		t.Fatalf("STORM_WALK 應套用 brave speed 4 與 300 秒 buff，buff172=%v BraveSpeed=%d BraveTicks=%d",
			player.GetBuff(172), player.BraveSpeed, player.BraveTicks)
	}
}

func TestSkillSelfBuffSendsYiweiPostCastStatusRefresh(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "body-to-mind",
		X:         100,
		Y:         100,
		MapID:     4,
		MP:        10,
		MaxMP:     100,
		SP:        7,
		MR:        22,
	})

	s := newSkillTestSystem(t, ws)
	s.executeSelfSkill(player.Session, player, &data.SkillInfo{
		SkillID:  130,
		ActionID: 19,
		CastGfx:  2179,
	})

	packets := drainSkillTestPackets(player.Session)
	if !hasOpcodePacket(packets, packet.S_OPCODE_MAGIC_STATUS) {
		t.Fatalf("yiwei sendGrfx 補助魔法後會送 S_SPMR，packets=%v", packets)
	}
	if !hasOpcodePacket(packets, packet.S_OPCODE_STATUS) {
		t.Fatalf("yiwei sendGrfx 補助魔法後會送 S_OwnCharStatus，packets=%v", packets)
	}
	if !hasYiweiUpdateERPacket(packets, calcPlayerErLikeYiwei(player)) {
		t.Fatalf("yiwei sendGrfx 補助魔法後會送 S_PacketBox.UPDATE_ER，packets=%v", packets)
	}
}

func TestSkillSelfInvisibilityBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		ShowID:    7,
	})
	sameShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "same-show",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    7,
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 3,
		Session:   newSkillTestSession(t, 3),
		CharID:    1003,
		Name:      "other-show",
		X:         101,
		Y:         101,
		MapID:     4,
		ShowID:    8,
	})

	newSkillTestSystem(t, ws).executeSelfSkill(player.Session, player, &data.SkillInfo{
		SkillID:  60,
		ActionID: 19,
	})

	samePackets := drainSkillTestPackets(sameShow.Session)
	otherPackets := drainSkillTestPackets(otherShow.Session)
	if !hasRemoveObjectPacket(samePackets, player.CharID) {
		t.Fatal("同 ShowID 玩家應收到隱身 remove object")
	}
	if hasRemoveObjectPacket(otherPackets, player.CharID) {
		t.Fatal("不同 ShowID 玩家不應收到隱身 remove object")
	}
	if !hasActionGfxPacket(samePackets, player.CharID, 19) {
		t.Fatal("同 ShowID 玩家應收到隱身施法動作")
	}
	if hasActionGfxPacket(otherPackets, player.CharID, 19) {
		t.Fatal("不同 ShowID 玩家不應收到隱身施法動作")
	}
}

func TestSkillDetectionAffectsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		ShowID:    11,
	})
	sameHidden := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "same-hidden",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    11,
		Invisible: true,
	})
	otherHidden := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 3,
		Session:   newSkillTestSession(t, 3),
		CharID:    1003,
		Name:      "other-hidden",
		X:         101,
		Y:         101,
		MapID:     4,
		ShowID:    12,
		Invisible: true,
	})
	sameViewer := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 4,
		Session:   newSkillTestSession(t, 4),
		CharID:    1004,
		Name:      "same-viewer",
		X:         102,
		Y:         100,
		MapID:     4,
		ShowID:    11,
	})
	otherViewer := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 5,
		Session:   newSkillTestSession(t, 5),
		CharID:    1005,
		Name:      "other-viewer",
		X:         102,
		Y:         101,
		MapID:     4,
		ShowID:    12,
	})
	sameHidden.AddBuff(&world.ActiveBuff{SkillID: 60, TicksLeft: 100, SetInvisible: true})
	otherHidden.AddBuff(&world.ActiveBuff{SkillID: 60, TicksLeft: 100, SetInvisible: true})

	newSkillTestSystem(t, ws).executeSelfSkill(caster.Session, caster, &data.SkillInfo{
		SkillID:  13,
		ActionID: 19,
	})

	if sameHidden.HasBuff(60) {
		t.Fatal("同 ShowID 隱身玩家應被無所遁形揭示")
	}
	if !otherHidden.HasBuff(60) {
		t.Fatal("不同 ShowID 隱身玩家不應被無所遁形揭示")
	}
	if !hasPutObjectPacket(drainSkillTestPackets(sameViewer.Session), sameHidden.CharID) {
		t.Fatal("同 ShowID 觀眾應收到被揭示玩家 put object")
	}
	if hasPutObjectPacket(drainSkillTestPackets(otherViewer.Session), sameHidden.CharID) {
		t.Fatal("不同 ShowID 觀眾不應收到被揭示玩家 put object")
	}
}

func TestSkillDetectionSkipsGmInvisiblePlayerLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 11,
		Session:   newSkillTestSession(t, 11),
		CharID:    1101,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		ShowID:    21,
	})
	gmHidden := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:   12,
		Session:     newSkillTestSession(t, 12),
		CharID:      1102,
		Name:        "gm-hidden",
		X:           101,
		Y:           100,
		MapID:       4,
		ShowID:      21,
		AccessLevel: 200,
		Invisible:   true,
	})
	viewer := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 13,
		Session:   newSkillTestSession(t, 13),
		CharID:    1103,
		Name:      "viewer",
		X:         102,
		Y:         100,
		MapID:     4,
		ShowID:    21,
	})
	gmHidden.AddBuff(&world.ActiveBuff{SkillID: 60, TicksLeft: 100, SetInvisible: true})

	newSkillTestSystem(t, ws).executeSelfSkill(caster.Session, caster, &data.SkillInfo{
		SkillID:  13,
		ActionID: 19,
	})

	if !gmHidden.HasBuff(60) || !gmHidden.Invisible {
		t.Fatalf("yiwei detection() 會跳過 isGmInvis 目標，buff60=%v Invisible=%v", gmHidden.HasBuff(60), gmHidden.Invisible)
	}
	if hasPutObjectPacket(drainSkillTestPackets(viewer.Session), gmHidden.CharID) {
		t.Fatal("GM 隱身目標未被揭露時，同 ShowID 觀眾不應收到 put object")
	}
}

func TestSkillDetectionSkipsGmInvisibleCasterLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:   21,
		Session:     newSkillTestSession(t, 21),
		CharID:      2101,
		Name:        "gm-caster",
		X:           100,
		Y:           100,
		MapID:       4,
		ShowID:      31,
		AccessLevel: 200,
		Invisible:   true,
	})
	caster.AddBuff(&world.ActiveBuff{SkillID: 60, TicksLeft: 100, SetInvisible: true})

	newSkillTestSystem(t, ws).executeSelfSkill(caster.Session, caster, &data.SkillInfo{
		SkillID:  13,
		ActionID: 19,
	})

	if !caster.HasBuff(60) || !caster.Invisible {
		t.Fatalf("yiwei detection() 會跳過 isGmInvis 的施法者自身，buff60=%v Invisible=%v", caster.HasBuff(60), caster.Invisible)
	}
}

func TestSkillDetectionDetectsScreenTrapsLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 31,
		Session:   newSkillTestSession(t, 31),
		CharID:    3101,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		ShowID:    41,
	})
	viewer := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 32,
		Session:   newSkillTestSession(t, 32),
		CharID:    3102,
		Name:      "viewer",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    41,
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 33,
		Session:   newSkillTestSession(t, 33),
		CharID:    3103,
		Name:      "other-show",
		X:         101,
		Y:         101,
		MapID:     4,
		ShowID:    42,
	})
	trapData := &data.TrapData{
		Templates: map[int32]*data.TrapTemplate{
			1: {TrapID: 1, Type: 1, GfxID: 2231, Detectionable: true},
			2: {TrapID: 2, Type: 1, GfxID: 2232, Detectionable: false},
			3: {TrapID: 3, Type: 1, GfxID: 2233, Detectionable: true},
		},
		Spawns: []data.TrapSpawn{
			{TrapID: 1, MapID: 4, X: 101, Y: 100, Count: 1},
			{TrapID: 2, MapID: 4, X: 102, Y: 100, Count: 1},
			{TrapID: 3, MapID: 4, X: 130, Y: 100, Count: 1},
		},
	}
	trapMgr := world.NewTrapManager(trapData, nil)
	detectableTrap := trapMgr.GetTrapsAt(101, 100, 4)[0]
	undetectableTrap := trapMgr.GetTrapsAt(102, 100, 4)[0]
	outsideTrap := trapMgr.GetTrapsAt(130, 100, 4)[0]
	deps := &handler.Deps{World: ws, Log: zap.NewNop(), TrapMgr: trapMgr}
	deps.Trap = NewTrapSystem(deps)
	sys := &SkillSystem{deps: deps}

	sys.executeSelfSkill(caster.Session, caster, &data.SkillInfo{
		SkillID:  13,
		ActionID: 19,
	})

	if detectableTrap.Alive {
		t.Fatal("yiwei WorldTrap.onDetection 應停用畫面內陷阱")
	}
	if undetectableTrap.Alive {
		t.Fatal("yiwei WorldTrap.onDetection 即使不可探查也會停用畫面內陷阱")
	}
	if !outsideTrap.Alive {
		t.Fatal("畫面外陷阱不應被 DETECTION 偵測或停用")
	}
	viewerPackets := drainSkillTestPackets(viewer.Session)
	if !hasEffectLocationPacket(viewerPackets, detectableTrap.X, detectableTrap.Y, detectableTrap.Template.GfxID) {
		t.Fatal("可探查陷阱應對同 ShowID 觀眾送出 S_EffectLocation")
	}
	if hasEffectLocationPacket(viewerPackets, undetectableTrap.X, undetectableTrap.Y, undetectableTrap.Template.GfxID) {
		t.Fatal("不可探查陷阱不應送出陷阱特效")
	}
	if hasEffectLocationPacket(drainSkillTestPackets(otherShow.Session), detectableTrap.X, detectableTrap.Y, detectableTrap.Template.GfxID) {
		t.Fatal("陷阱偵測特效不應跨 ShowID 廣播")
	}
}

func hasYiweiUpdateERPacket(packets [][]byte, wantER int16) bool {
	for _, pkt := range packets {
		if len(pkt) >= 4 && pkt[0] == packet.S_OPCODE_EVENT && pkt[1] == 132 {
			got := int16(uint16(pkt[2]) | uint16(pkt[3])<<8)
			return got == wantER
		}
	}
	return false
}
