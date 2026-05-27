package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

func TestSkillBuffADLV80BlessingsApplyYiweiStatsThroughGoBuffSystem(t *testing.T) {
	tests := []struct {
		name          string
		skillID       int32
		wantAC        int16
		wantDex       int16
		wantMaxHP     int32
		wantMaxMP     int32
		wantHit       int16
		wantDmg       int16
		wantBowHit    int16
		wantBowDmg    int16
		wantMR        int16
		wantHPR       int16
		wantMPR       int16
		wantEarth     int16
		wantWater     int16
		wantWind      int16
		wantWeightPct int32
	}{
		{
			name:          "4009 卡瑞的祝福",
			skillID:       4009,
			wantMaxHP:     100,
			wantMaxMP:     50,
			wantHit:       5,
			wantDmg:       1,
			wantBowHit:    5,
			wantBowDmg:    1,
			wantHPR:       3,
			wantMPR:       3,
			wantEarth:     30,
			wantWeightPct: 4,
		},
		{
			name:       "4010 莎爾的祝福",
			skillID:    4010,
			wantAC:     -8,
			wantMaxHP:  80,
			wantMaxMP:  10,
			wantBowHit: 6,
			wantBowDmg: 3,
			wantHPR:    8,
			wantMPR:    1,
			wantWater:  30,
		},
		{
			name:       "4018 甘特的祝福",
			skillID:    4018,
			wantDex:    5,
			wantMaxHP:  100,
			wantMaxMP:  40,
			wantBowHit: 7,
			wantBowDmg: 5,
			wantMR:     15,
			wantHPR:    10,
			wantMPR:    3,
			wantWind:   30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := world.NewState()
			player := addSkillTestPlayer(ws, &world.PlayerInfo{
				SessionID: 1,
				Session:   newSkillTestSession(t, 1),
				CharID:    1001,
				Name:      "adlv80",
				X:         100,
				Y:         100,
				MapID:     4,
				Level:     80,
				HP:        500,
				MaxHP:     500,
				MP:        200,
				MaxMP:     200,
				Str:       20,
				Dex:       18,
				Con:       18,
				Wis:       12,
				Intel:     12,
				Cha:       10,
			})
			s := newSkillBuffTestSystem(t, ws)
			baseWeight := world.PlayerMaxWeight(player)

			s.applyBuffEffect(player, &data.SkillInfo{SkillID: tt.skillID, BuffDuration: 30})

			if !player.HasBuff(tt.skillID) {
				t.Fatalf("yiwei ADLV80 skillmode 應透過 Go buff 系統註冊 buff %d", tt.skillID)
			}
			assertADLV80PlayerDeltas(t, player, tt.wantAC, tt.wantDex, tt.wantMaxHP, tt.wantMaxMP, tt.wantHit, tt.wantDmg, tt.wantBowHit, tt.wantBowDmg, tt.wantMR, tt.wantHPR, tt.wantMPR, tt.wantEarth, tt.wantWater, tt.wantWind)
			if tt.wantWeightPct != 0 {
				wantWeight := baseWeight * (100 + tt.wantWeightPct) / 100
				if got := world.PlayerMaxWeight(player); got != wantWeight {
					t.Fatalf("ADLV80_1 add_weightUP(4) 應讓最大負重 +4%%，got=%d want=%d", got, wantWeight)
				}
			}

			packets := drainSkillTestPackets(player.Session)
			if !hasOpcodePacket(packets, packet.S_OPCODE_HIT_POINT) {
				t.Fatalf("ADLV80 MaxHP 變化應同步 S_HPUpdate，skill=%d", tt.skillID)
			}
			if !hasOpcodePacket(packets, packet.S_OPCODE_MANA_POINT) {
				t.Fatalf("ADLV80 MaxMP 變化應同步 S_MPUpdate，skill=%d", tt.skillID)
			}
			if !hasOpcodePacket(packets, packet.S_OPCODE_ABILITY_SCORES) {
				t.Fatalf("ADLV80 元素抗性/AC 變化應同步 S_OwnCharAttrDef，skill=%d", tt.skillID)
			}
			if tt.wantMR != 0 && !hasOpcodePacket(packets, packet.S_OPCODE_MAGIC_STATUS) {
				t.Fatalf("ADLV80_3 MR 變化應同步 S_SPMR")
			}

			s.removeBuffAndRevert(player, tt.skillID)
			assertADLV80PlayerDeltas(t, player, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
			if tt.wantWeightPct != 0 {
				if got := world.PlayerMaxWeight(player); got != baseWeight {
					t.Fatalf("ADLV80_1 stop 後最大負重應還原，got=%d want=%d", got, baseWeight)
				}
			}

			packets = drainSkillTestPackets(player.Session)
			if !hasOpcodePacket(packets, packet.S_OPCODE_HIT_POINT) {
				t.Fatalf("ADLV80 stop MaxHP 還原應同步 S_HPUpdate，skill=%d", tt.skillID)
			}
			if !hasOpcodePacket(packets, packet.S_OPCODE_MANA_POINT) {
				t.Fatalf("ADLV80 stop MaxMP 還原應同步 S_MPUpdate，skill=%d", tt.skillID)
			}
		})
	}
}

func assertADLV80PlayerDeltas(t *testing.T, p *world.PlayerInfo, ac, dex int16, maxHP, maxMP int32, hit, dmg, bowHit, bowDmg, mr, hpr, mpr, earth, water, wind int16) {
	t.Helper()
	if p.AC != ac || p.Dex != 18+dex || p.MaxHP != 500+maxHP || p.MaxMP != 200+maxMP ||
		p.HitMod != hit || p.DmgMod != dmg || p.BowHitMod != bowHit || p.BowDmgMod != bowDmg ||
		p.MR != mr || p.HPR != hpr || p.MPR != mpr ||
		p.EarthRes != earth || p.WaterRes != water || p.WindRes != wind {
		t.Fatalf("ADLV80 buff delta mismatch: AC=%d DEX=%d MaxHP=%d MaxMP=%d Hit=%d Dmg=%d BowHit=%d BowDmg=%d MR=%d HPR=%d MPR=%d Earth=%d Water=%d Wind=%d",
			p.AC, p.Dex, p.MaxHP, p.MaxMP, p.HitMod, p.DmgMod, p.BowHitMod, p.BowDmgMod, p.MR, p.HPR, p.MPR, p.EarthRes, p.WaterRes, p.WindRes)
	}
}
