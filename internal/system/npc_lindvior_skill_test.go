package system

import (
	"path/filepath"
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/world"
)

func TestNpcLindviorSkySpikedSendsDirectionalEffectLocationLikeJava(t *testing.T) {
	tests := []struct {
		name    string
		targetX int32
		targetY int32
		wantGFX int32
	}{
		{name: "south-east", targetX: 101, targetY: 101, wantGFX: 7987},
		{name: "south-west", targetX: 99, targetY: 101, wantGFX: 8050},
		{name: "west", targetX: 99, targetY: 100, wantGFX: 8051},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := world.NewState()
			target := addSkillTestPlayer(ws, &world.PlayerInfo{
				SessionID: 1,
				Session:   newSkillTestSession(t, 1),
				CharID:    1001,
				Name:      "target",
				X:         tt.targetX,
				Y:         tt.targetY,
				MapID:     900,
				HP:        5000,
				MaxHP:     5000,
			})
			otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
				SessionID: 2,
				Session:   newSkillTestSession(t, 2),
				CharID:    1002,
				Name:      "other-show",
				X:         tt.targetX,
				Y:         tt.targetY,
				MapID:     900,
				ShowID:    9,
				HP:        5000,
				MaxHP:     5000,
			})
			npc := &world.NpcInfo{
				ID:          2001,
				NpcID:       145684,
				Impl:        "L1Monster",
				Name:        "lindvior",
				X:           100,
				Y:           100,
				MapID:       900,
				HP:          10000,
				MaxHP:       10000,
				MP:          100,
				MaxMP:       100,
				Level:       80,
				STR:         30,
				DEX:         30,
				AggroTarget: target.SessionID,
			}
			ws.AddNpc(npc)
			s := newNpcAILOSTestSystem(t, ws)
			skills, err := data.LoadSkillTable(filepath.Join("..", "..", "data", "yaml", "skill_list.yaml"))
			if err != nil {
				t.Fatalf("載入技能資料失敗: %v", err)
			}
			s.deps.Skills = skills

			if !s.executeNpcSkill(npc, target, 11061, 12, 0, 0) {
				t.Fatal("yiwei LINDVIOR_SKY_SPIKED 應可施放")
			}

			if !hasEffectLocationPacket(drainSkillTestPackets(target.Session), tt.targetX, tt.targetY, tt.wantGFX) {
				t.Fatalf("yiwei 11061 方向 %s 應在目標座標送 S_EffectLocation gfx=%d", tt.name, tt.wantGFX)
			}
			if hasEffectLocationPacket(drainSkillTestPackets(otherShow.Session), tt.targetX, tt.targetY, tt.wantGFX) {
				t.Fatal("不同 ShowID 玩家不應收到 11061 S_EffectLocation")
			}
		})
	}
}
