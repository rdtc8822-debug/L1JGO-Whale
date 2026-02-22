package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

const (
	statAllocAttrCode uint16 = 479 // Java C_Attr case 479 — stat allocation
	maxStatValue      int16  = 35  // per-stat cap
	maxTotalStats     int16  = 210 // sum of all 6 base stats cap
	bonusStatMinLevel int16  = 51  // minimum level to earn bonus stat points
)

// HandlePlate processes C_PLATE (opcode 10) — stat point allocation (bonus stats at level 51+).
// Java equivalent: C_Attr case 479.
// Format: [H attrcode(479)][C confirm(1=yes)][S statName]
func HandlePlate(sess *net.Session, r *packet.Reader, deps *Deps) {
	attrCode := r.ReadH()
	if attrCode != statAllocAttrCode {
		return
	}

	confirm := r.ReadC()
	if confirm != 1 {
		return
	}

	statName := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Must be level 51+
	if player.Level < bonusStatMinLevel {
		return
	}

	// Check available bonus points: (level - 50) total earned, BonusStats already used
	available := player.Level - 50 - player.BonusStats
	if available <= 0 {
		return
	}

	// Check total stats cap
	totalStats := player.Str + player.Dex + player.Con + player.Wis + player.Intel + player.Cha
	if totalStats >= maxTotalStats {
		return
	}

	// Apply stat increase
	switch statName {
	case "str":
		if player.Str >= maxStatValue {
			sendServerMessage(sess, 481)
			return
		}
		player.Str++
	case "dex":
		if player.Dex >= maxStatValue {
			sendServerMessage(sess, 481)
			return
		}
		player.Dex++
	case "con":
		if player.Con >= maxStatValue {
			sendServerMessage(sess, 481)
			return
		}
		player.Con++
	case "wis":
		if player.Wis >= maxStatValue {
			sendServerMessage(sess, 481)
			return
		}
		player.Wis++
	case "int":
		if player.Intel >= maxStatValue {
			sendServerMessage(sess, 481)
			return
		}
		player.Intel++
	case "cha":
		if player.Cha >= maxStatValue {
			sendServerMessage(sess, 481)
			return
		}
		player.Cha++
	default:
		return
	}

	player.BonusStats++

	deps.Log.Info(fmt.Sprintf("配點完成  角色=%s  屬性=%s  已用配點=%d", player.Name, statName, player.BonusStats))

	// Send updated status to client
	sendPlayerStatus(sess, player)
	sendAbilityScores(sess, player)

	// Show dialog again if more points available
	remainingBonus := player.Level - 50 - player.BonusStats
	newTotal := player.Str + player.Dex + player.Con + player.Wis + player.Intel + player.Cha
	if remainingBonus > 0 && newTotal < maxTotalStats {
		sendRaiseAttrDialog(sess, player.CharID)
	}
}

// sendAbilityScores sends S_ABILITY_SCORES (opcode 174) — AC + elemental resistances.
// Matches Java S_OwnCharAttrDef.
func sendAbilityScores(sess *net.Session, p *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ABILITY_SCORES)
	w.WriteC(byte(p.AC))
	w.WriteH(uint16(p.FireRes))
	w.WriteH(uint16(p.WaterRes))
	w.WriteH(uint16(p.WindRes))
	w.WriteH(uint16(p.EarthRes))
	sess.Send(w.Bytes())
}

// sendRaiseAttrDialog sends the "RaiseAttr" HTML dialog for bonus stat allocation.
// Matches Java S_bonusstats: S_OPCODE_SHOWHTML + charID + "RaiseAttr".
func sendRaiseAttrDialog(sess *net.Session, charID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(charID)
	w.WriteS("RaiseAttr")
	sess.Send(w.Bytes())
}
