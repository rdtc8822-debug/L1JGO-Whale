package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	l1net "github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

type fakeSummonManager struct {
	summonTargetID int32
}

func (f *fakeSummonManager) ExecuteSummonMonster(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, targetID int32) {
	f.summonTargetID = targetID
}

func (f *fakeSummonManager) ExecuteElementalSummon(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo) {
}

func (f *fakeSummonManager) ExecuteTamingMonster(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, _ int32) {
}

func (f *fakeSummonManager) ExecuteCreateZombie(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, _ int32) {
}

func (f *fakeSummonManager) ExecuteReturnToNature(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, _ int32) {
}

func (f *fakeSummonManager) DismissSummon(_ *world.SummonInfo, _ *world.PlayerInfo) {
}

func newTeleportSummonTestSystem(t *testing.T, ws *world.State) *SkillSystem {
	t.Helper()
	skills, err := data.LoadSkillTable("../../data/yaml/skill_list.yaml")
	if err != nil {
		t.Fatalf("載入技能資料失敗: %v", err)
	}
	return &SkillSystem{deps: &handler.Deps{
		World:  ws,
		Skills: skills,
		Log:    zap.NewNop(),
	}}
}

func TestSkillTeleportSummonSummonMonsterUsesParsedSummonID(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "summoner",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     60,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			51,
		},
	})
	player.Inv.AddItem(40318, 3, "magic gem", 0, 0, true, 1)
	summon := &fakeSummonManager{}
	s := newTeleportSummonTestSystem(t, ws)
	s.deps.Summon = summon

	s.processSkill(handler.SkillRequest{
		SessionID: player.SessionID,
		SkillID:   51,
		SummonID:  263,
	})

	if summon.summonTargetID != 263 {
		t.Fatalf("召喚術應使用封包解析出的 SummonID，got=%d", summon.summonTargetID)
	}
}

func TestSkillTeleportSummonControlRingChecksAllRingSlots(t *testing.T) {
	player := &world.PlayerInfo{Inv: world.NewInventory()}
	ring := &world.InvItem{ObjectID: 7001, ItemID: 20284, Equipped: true}
	player.Equip.Set(world.SlotRing3, ring)

	if !hasSummonRing(player) {
		t.Fatal("召喚控制戒指在 Ring3/Ring4 欄位也應被視為已裝備")
	}

	ring.Equipped = false
	if hasSummonRing(player) {
		t.Fatal("戒指欄物品未標記 Equipped 時不應視為已裝備")
	}
}

func TestSkillTeleportSummonTeleportUsesParsedBookmarkID(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "teleporter",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     52,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			5,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  player.SessionID,
		SkillID:    5,
		BookmarkID: 77,
		MapID:      4,
	})

	if player.X != 32710 || player.Y != 32820 || player.MapID != 4 {
		t.Fatalf("瞬間移動應使用封包解析出的 BookmarkID，位置=%d,%d,%d", player.X, player.Y, player.MapID)
	}
}

func TestSkillTeleportDoesNotSendGenericActionGfxMatchesJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "teleport-gfx",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     52,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			5,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  player.SessionID,
		SkillID:    5,
		BookmarkID: 77,
		MapID:      4,
	})

	packets := drainSkillTestPackets(player.Session)
	if hasOpcodePacket(packets, packet.S_OPCODE_ACTION) {
		t.Fatal("Java 傳送技能不送 generic S_DoActionGFX，Go 不應送 S_OPCODE_ACTION")
	}
	if !hasSkillEffectPacket(packets, player.CharID, 169) {
		t.Fatal("傳送成功仍應送 Java S_SkillSound 對應的 169 特效")
	}
}

func TestSkillTeleportDepartureEffectBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "teleport-show",
		X:         100,
		Y:         100,
		MapID:     4,
		ShowID:    10,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     52,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			5,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	sameShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "same-show",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    10,
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 3,
		Session:   newSkillTestSession(t, 3),
		CharID:    1003,
		Name:      "other-show",
		X:         102,
		Y:         100,
		MapID:     4,
		ShowID:    99,
	})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  player.SessionID,
		SkillID:    5,
		BookmarkID: 77,
		MapID:      4,
	})

	if !hasSkillEffectPacket(drainSkillTestPackets(sameShow.Session), player.CharID, 169) {
		t.Fatal("同 ShowID 玩家應看到 TELEPORT 出發 169 特效")
	}
	if hasSkillEffectPacket(drainSkillTestPackets(otherShow.Session), player.CharID, 169) {
		t.Fatal("yiwei sendPacketsAll 會用 showId 隔離，不同 ShowID 玩家不應看到 TELEPORT 出發特效")
	}
}

func TestSkillTeleportFailureUnlocksClientMatchesJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "teleport-no-mp",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        0,
		MaxMP:     100,
		Level:     52,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			5,
		},
	})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID: player.SessionID,
		SkillID:   5,
	})

	packets := drainSkillTestPackets(player.Session)
	if !hasTeleportUnlockPacket(packets) {
		t.Fatal("Java failSkill 對 TELEPORT 失敗會送 TeleportUnlock，Go 也應解除客戶端傳送鎖定")
	}
	if player.X != 100 || player.Y != 100 || player.MapID != 4 {
		t.Fatalf("MP 不足時不應傳送，位置=%d,%d,%d", player.X, player.Y, player.MapID)
	}
}

func hasTeleportUnlockPacket(packets [][]byte) bool {
	for _, pkt := range packets {
		if len(pkt) >= 2 && pkt[0] == packet.S_OPCODE_PARALYSIS && pkt[1] == handler.TeleportUnlock {
			return true
		}
	}
	return false
}

func TestSkillTeleportBookmarkMapRuleMatchesJava(t *testing.T) {
	tests := []struct {
		name      string
		skillID   int32
		info      *data.MapInfo
		wantOK    bool
		wantMsgID uint16
	}{
		{
			name:      "單人指定傳送依 teleportable 允許，不受 escapable=false 阻擋",
			skillID:   5,
			info:      &data.MapInfo{Teleportable: true, Escapable: false},
			wantOK:    true,
			wantMsgID: 0,
		},
		{
			name:      "單人指定傳送在 teleportable=false 時使用 Java 訊息 647",
			skillID:   5,
			info:      &data.MapInfo{Teleportable: false, Escapable: true},
			wantOK:    false,
			wantMsgID: 647,
		},
		{
			name:      "集體指定傳送依 escapable 拒絕並使用 Java 訊息 276",
			skillID:   69,
			info:      &data.MapInfo{Teleportable: true, Escapable: false},
			wantOK:    false,
			wantMsgID: 276,
		},
		{
			name:      "集體指定傳送有書籤時依 escapable 允許",
			skillID:   69,
			info:      &data.MapInfo{Teleportable: false, Escapable: true},
			wantOK:    true,
			wantMsgID: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotMsgID := checkBookmarkTeleportMapRule(tt.skillID, tt.info, nil)
			if gotOK != tt.wantOK || gotMsgID != tt.wantMsgID {
				t.Fatalf("got ok=%v msg=%d, want ok=%v msg=%d", gotOK, gotMsgID, tt.wantOK, tt.wantMsgID)
			}
		})
	}
}

func TestSkillTeleportRandomMapRuleMatchesJava(t *testing.T) {
	tests := []struct {
		name      string
		skillID   int32
		info      *data.MapInfo
		wantOK    bool
		wantMsgID uint16
	}{
		{
			name:      "單人隨機傳送在 teleportable=false 時使用 Java 訊息 647",
			skillID:   5,
			info:      &data.MapInfo{Teleportable: false},
			wantOK:    false,
			wantMsgID: 647,
		},
		{
			name:      "集體隨機傳送在 teleportable=false 時使用 Java 訊息 276",
			skillID:   69,
			info:      &data.MapInfo{Teleportable: false},
			wantOK:    false,
			wantMsgID: 276,
		},
		{
			name:      "可隨機傳送地圖不拒絕",
			skillID:   5,
			info:      &data.MapInfo{Teleportable: true},
			wantOK:    true,
			wantMsgID: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotMsgID := checkRandomTeleportMapRule(tt.skillID, tt.info, nil)
			if gotOK != tt.wantOK || gotMsgID != tt.wantMsgID {
				t.Fatalf("got ok=%v msg=%d, want ok=%v msg=%d", gotOK, gotMsgID, tt.wantOK, tt.wantMsgID)
			}
		})
	}
}

func TestSkillTeleportTowerDominancePermitMatchesJava(t *testing.T) {
	player := &world.PlayerInfo{Inv: world.NewInventory()}
	player.Inv.AddItem(84041, 1, "tower permit 1f", 0, 0, true, 1)
	firstFloor := &data.MapInfo{MapID: 3301, Teleportable: false, Escapable: false}

	if ok, msgID := checkBookmarkTeleportMapRule(5, firstFloor, player); !ok || msgID != 0 {
		t.Fatalf("持有對應傲慢之塔支配傳送符時 TELEPORT 指定傳送應允許，ok=%v msg=%d", ok, msgID)
	}
	if ok, msgID := checkRandomTeleportMapRule(5, firstFloor, player); !ok || msgID != 0 {
		t.Fatalf("持有對應傲慢之塔支配傳送符時 TELEPORT 隨機傳送應允許，ok=%v msg=%d", ok, msgID)
	}
	if ok, msgID := checkBookmarkTeleportMapRule(69, firstFloor, player); ok || msgID != 276 {
		t.Fatalf("MASS_TELEPORT 不應套用 TELEPORT 的支配傳送符例外，ok=%v msg=%d", ok, msgID)
	}

	secondFloor := &data.MapInfo{MapID: 3302, Teleportable: false, Escapable: true}
	if ok, msgID := checkBookmarkTeleportMapRule(5, secondFloor, player); ok || msgID != 647 {
		t.Fatalf("支配傳送符樓層不符時 TELEPORT 應維持拒絕，ok=%v msg=%d", ok, msgID)
	}

	player.Inv.AddItem(84071, 1, "phantom tower permit", 0, 0, true, 1)
	if ok, msgID := checkBookmarkTeleportMapRule(5, secondFloor, player); !ok || msgID != 0 {
		t.Fatalf("持有幻象傲慢之塔傳送符時 3301-3310 TELEPORT 應允許，ok=%v msg=%d", ok, msgID)
	}
}

func TestSkillTeleportIgnoresRemovedWarriorDesperadoBuffFor380C(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "desperado-target",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     52,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			5,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	player.AddBuff(&world.ActiveBuff{SkillID: 230, TicksLeft: 100})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  player.SessionID,
		SkillID:    5,
		BookmarkID: 77,
		MapID:      4,
	})

	if player.X != 32710 || player.Y != 32820 || player.MapID != 4 {
		t.Fatalf("3.80C 已剔除的 230 buff 不應阻擋瞬移，位置=%d,%d,%d", player.X, player.Y, player.MapID)
	}
	if player.MP != 90 {
		t.Fatalf("瞬移成功應正常消耗 MP，MP=%d", player.MP)
	}
}

func TestSkillTeleportStatusFreezeBlocksSingleTeleportBeforeCost(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "freeze-target",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     52,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			5,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	player.AddBuff(&world.ActiveBuff{SkillID: 4000, TicksLeft: 100})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  player.SessionID,
		SkillID:    5,
		BookmarkID: 77,
		MapID:      4,
	})

	if player.X != 100 || player.Y != 100 || player.MapID != 4 {
		t.Fatalf("束縛 4000 狀態下瞬移應被拒絕，位置=%d,%d,%d", player.X, player.Y, player.MapID)
	}
	if player.MP != 100 {
		t.Fatalf("束縛 4000 拒絕應發生在消耗 MP 前，MP=%d", player.MP)
	}
}

func TestSkillTeleportThunderGrabBlocksSingleTeleportBeforeCost(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "thunder-grab-target",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     52,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			5,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	player.AddBuff(&world.ActiveBuff{SkillID: 192, TicksLeft: 100})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  player.SessionID,
		SkillID:    5,
		BookmarkID: 77,
		MapID:      4,
	})

	if player.X != 100 || player.Y != 100 || player.MapID != 4 {
		t.Fatalf("奪命之雷 192 狀態下瞬移應被拒絕，位置=%d,%d,%d", player.X, player.Y, player.MapID)
	}
	if player.MP != 100 {
		t.Fatalf("奪命之雷 192 拒絕應發生在消耗 MP 前，MP=%d", player.MP)
	}
}

func TestSkillMassTeleportSkipsPrivateShopClanMember(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "mass-caster",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     60,
		ClanID:    10,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			69,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	caster.Inv.AddItem(40318, 1, "magic gem", 0, 0, true, 1)
	member := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:   2,
		Session:     newSkillTestSession(t, 2),
		CharID:      1002,
		Name:        "shop-member",
		X:           101,
		Y:           100,
		MapID:       4,
		HP:          100,
		MaxHP:       100,
		MP:          100,
		MaxMP:       100,
		Level:       60,
		ClanID:      10,
		PrivateShop: true,
		Inv:         world.NewInventory(),
	})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  caster.SessionID,
		SkillID:    69,
		BookmarkID: 77,
		MapID:      4,
	})

	if caster.X != 32710 || caster.Y != 32820 || caster.MapID != 4 {
		t.Fatalf("施法者應完成集體傳送，位置=%d,%d,%d", caster.X, caster.Y, caster.MapID)
	}
	if member.X != 101 || member.Y != 100 || member.MapID != 4 {
		t.Fatalf("擺攤血盟成員不應被集體傳送，位置=%d,%d,%d", member.X, member.Y, member.MapID)
	}
}

func TestSkillMassTeleportAsksClanMemberByDefault(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "mass-caster",
		X:         100,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     60,
		ClanID:    10,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			69,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	caster.Inv.AddItem(40318, 1, "magic gem", 0, 0, true, 1)
	member := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "ask-member",
		X:         101,
		Y:         100,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     60,
		ClanID:    10,
		Inv:       world.NewInventory(),
	})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  caster.SessionID,
		SkillID:    69,
		BookmarkID: 77,
		MapID:      4,
	})

	if caster.X != 32710 || caster.Y != 32820 || caster.MapID != 4 {
		t.Fatalf("施法者應完成集體傳送，位置=%d,%d,%d", caster.X, caster.Y, caster.MapID)
	}
	if member.X != 101 || member.Y != 100 || member.MapID != 4 {
		t.Fatalf("預設詢問的血盟成員不應立即傳送，位置=%d,%d,%d", member.X, member.Y, member.MapID)
	}
	if member.PendingYesNoType != 748 {
		t.Fatalf("預設詢問的血盟成員應收到 748 確認，got=%d", member.PendingYesNoType)
	}
	if member.TeleportX != 32710 || member.TeleportY != 32820 || member.TeleportMapID != 4 {
		t.Fatalf("確認前應暫存傳送目的地，got=%d,%d,%d", member.TeleportX, member.TeleportY, member.TeleportMapID)
	}
}

func TestSkillMassTeleportSkipsDifferentShowClanMemberLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "mass-caster",
		X:         100,
		Y:         100,
		MapID:     4,
		ShowID:    10,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     60,
		ClanID:    10,
		Inv:       world.NewInventory(),
		KnownSpells: []int32{
			69,
		},
		Bookmarks: []world.Bookmark{
			{ID: 77, Name: "target", X: 32710, Y: 32820, MapID: 4},
		},
	})
	caster.Inv.AddItem(40318, 1, "magic gem", 0, 0, true, 1)
	member := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "other-show-member",
		X:         101,
		Y:         100,
		MapID:     4,
		ShowID:    99,
		HP:        100,
		MaxHP:     100,
		MP:        100,
		MaxMP:     100,
		Level:     60,
		ClanID:    10,
		Inv:       world.NewInventory(),
	})
	s := newTeleportSummonTestSystem(t, ws)

	s.processSkill(handler.SkillRequest{
		SessionID:  caster.SessionID,
		SkillID:    69,
		BookmarkID: 77,
		MapID:      4,
	})

	if member.PendingYesNoType == 748 || member.TeleportMapID != 0 {
		t.Fatalf("不同 ShowID 血盟成員不在 yiwei visiblePlayer 內，不應收到集體傳送詢問或暫存目的地，Pending=%d TeleportMapID=%d",
			member.PendingYesNoType, member.TeleportMapID)
	}
	if member.X != 101 || member.Y != 100 || member.MapID != 4 {
		t.Fatalf("不同 ShowID 血盟成員不應被 MASS_TELEPORT 傳送，got=(%d,%d,%d)", member.X, member.Y, member.MapID)
	}
}
