package handler

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	l1net "github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

type capturePolymorphManager struct {
	skillName        string
	directPolyItemID int32
}

func (m *capturePolymorphManager) DoPoly(_ *world.PlayerInfo, _ int32, _ int, _ int) {}

func (m *capturePolymorphManager) UndoPoly(_ *world.PlayerInfo) {}

func (m *capturePolymorphManager) UsePolyScroll(_ *l1net.Session, _ *world.PlayerInfo, _ *world.InvItem, _ string) {
}

func (m *capturePolymorphManager) UsePolySkill(_ *l1net.Session, player *world.PlayerInfo, monsterName string) {
	m.skillName = monsterName
	player.PendingPolySkill = false
}

func (m *capturePolymorphManager) UseDirectPolyScroll(_ *l1net.Session, _ *world.PlayerInfo, invItem *world.InvItem) {
	if invItem != nil {
		m.directPolyItemID = invItem.ItemID
	}
}

type captureSummonManager struct {
	targetID int32
}

func (m *captureSummonManager) ExecuteSummonMonster(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, targetID int32) {
	m.targetID = targetID
}

func (m *captureSummonManager) ExecuteElementalSummon(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo) {
}

func (m *captureSummonManager) ExecuteTamingMonster(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, _ int32) {
}

func (m *captureSummonManager) ExecuteCreateZombie(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, _ int32) {
}

func (m *captureSummonManager) ExecuteReturnToNature(_ *l1net.Session, _ *world.PlayerInfo, _ *data.SkillInfo, _ int32) {
}

func (m *captureSummonManager) DismissSummon(_ *world.SummonInfo, _ *world.PlayerInfo) {}

func TestHandleNpcActionRoutesMonlistSelectionToPolySkill(t *testing.T) {
	ws := world.NewState()
	sess := &l1net.Session{ID: 1}
	player := &world.PlayerInfo{
		SessionID:        sess.ID,
		Session:          sess,
		CharID:           1001,
		Name:             "poly",
		PendingPolySkill: true,
		Inv:              world.NewInventory(),
	}
	ws.AddPlayer(player)
	poly := &capturePolymorphManager{}
	deps := &Deps{World: ws, Polymorph: poly, Log: zap.NewNop()}

	HandleNpcAction(sess, npcActionReader(player.CharID, "floating eye"), deps)

	if poly.skillName != "floating eye" {
		t.Fatalf("monlist 選擇應轉交 Polymorph.UsePolySkill，got=%q", poly.skillName)
	}
	if player.PendingPolySkill {
		t.Fatal("monlist 選擇完成後應清除 PendingPolySkill")
	}
}

func TestHandleNpcActionRoutesSummonlistSelectionToSummonSkill(t *testing.T) {
	ws := world.NewState()
	sess := &l1net.Session{ID: 1}
	player := &world.PlayerInfo{
		SessionID:           sess.ID,
		Session:             sess,
		CharID:              1001,
		Name:                "summoner",
		SummonSelectionMode: true,
		MP:                  100,
		MaxMP:               100,
		HP:                  100,
		MaxHP:               100,
		Inv:                 world.NewInventory(),
	}
	player.Inv.AddItem(40318, 10, "魔法寶石", 0, 0, true, 0)
	ws.AddPlayer(player)
	skills, err := data.LoadSkillTable("../../data/yaml/skill_list.yaml")
	if err != nil {
		t.Fatalf("讀取技能資料失敗: %v", err)
	}
	summon := &captureSummonManager{}
	deps := &Deps{World: ws, Skills: skills, Summon: summon, Log: zap.NewNop()}

	HandleNpcAction(sess, npcActionReader(player.CharID, "263"), deps)

	if summon.targetID != 263 {
		t.Fatalf("summonlist 選擇應轉交 Summon.ExecuteSummonMonster，got=%d", summon.targetID)
	}
}

func TestHandleNpcActionTogglesMassTeleportAskSetting(t *testing.T) {
	ws := world.NewState()
	sess := &l1net.Session{ID: 1}
	player := &world.PlayerInfo{
		SessionID: sess.ID,
		Session:   sess,
		CharID:    1001,
		Name:      "ask-toggle",
		Inv:       world.NewInventory(),
	}
	ws.AddPlayer(player)
	deps := &Deps{World: ws, Log: zap.NewNop()}

	HandleNpcAction(sess, npcActionReader(player.CharID, "teleport_close"), deps)

	if !player.NoAskMassTeleport {
		t.Fatal("teleport_close 應關閉集體傳送詢問，讓玩家直接接受集體傳送")
	}

	HandleNpcAction(sess, npcActionReader(player.CharID, "teleport_open"), deps)

	if player.NoAskMassTeleport {
		t.Fatal("teleport_open 應重新開啟集體傳送詢問")
	}
}

func npcActionReader(objID int32, action string) *packet.Reader {
	w := packet.NewWriterWithOpcode(packet.C_OPCODE_HACTION)
	w.WriteD(objID)
	w.WriteS(action)
	return packet.NewReader(w.RawBytes())
}
