package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// Chat type constants matching Java C_Chat.
const (
	ChatNormal = 0
	ChatShout  = 2
	ChatWorld  = 3
	ChatClan   = 4
	ChatParty  = 11
	ChatTrade  = 12
)

// HandleChat processes C_CHAT (opcode 40) — multi-channel chat.
func HandleChat(sess *net.Session, r *packet.Reader, deps *Deps) {
	chatType := r.ReadC()
	text := r.ReadS()

	if text == "" {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// GM commands: intercept "." prefix in normal chat
	if chatType == ChatNormal && HandleGMCommand(sess, player, text, deps) {
		return
	}

	deps.Log.Debug("C_Chat",
		zap.String("player", player.Name),
		zap.Uint8("type", chatType),
		zap.String("text", text),
	)

	switch chatType {
	case ChatNormal:
		// Normal chat: broadcast to nearby players via S_SAY (opcode 81)
		msg := fmt.Sprintf("%s: %s", player.Name, text)
		sendNormalChat(sess, player.CharID, msg)
		// Broadcast to nearby
		nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		for _, other := range nearby {
			sendNormalChat(other.Session, player.CharID, msg)
		}

	case ChatShout:
		// Shout: wider range chat via S_SAY (opcode 81) type 2
		msg := fmt.Sprintf("<%s> %s", player.Name, text)
		sendShoutChat(sess, player.CharID, msg, player.X, player.Y)
		nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		for _, other := range nearby {
			sendShoutChat(other.Session, player.CharID, msg, player.X, player.Y)
		}

	case ChatWorld:
		// World/Global chat requires food >= 6, costs 5 food (Java C_Chat.java)
		if player.Food < 6 {
			return
		}
		player.Food -= 5
		sendPlayerStatus(sess, player)

		// World/Global chat: all players via S_MESSAGE (opcode 243)
		msg := fmt.Sprintf("[%s] %s", player.Name, text)
		sendGlobalChat(sess, ChatWorld, msg)
		deps.World.AllPlayers(func(other *world.PlayerInfo) {
			if other.SessionID != sess.ID {
				sendGlobalChat(other.Session, ChatWorld, msg)
			}
		})

	case ChatTrade:
		// Trade chat: all players via S_MESSAGE (opcode 243)
		msg := fmt.Sprintf("[%s] %s", player.Name, text)
		sendGlobalChat(sess, ChatTrade, msg)
		deps.World.AllPlayers(func(other *world.PlayerInfo) {
			if other.SessionID != sess.ID {
				sendGlobalChat(other.Session, ChatTrade, msg)
			}
		})

	case ChatClan:
		// Clan chat: send to all online clan members
		if player.ClanID == 0 {
			return
		}
		clan := deps.World.Clans.GetClan(player.ClanID)
		if clan == nil {
			return
		}
		msg := fmt.Sprintf("{%s} %s", player.Name, text)
		for charID := range clan.Members {
			member := deps.World.GetByCharID(charID)
			if member != nil {
				sendGlobalChat(member.Session, ChatClan, msg)
			}
		}

	case ChatParty:
		// Party chat: send to all party members
		party := deps.World.Parties.GetParty(player.CharID)
		if party == nil {
			return
		}
		msg := fmt.Sprintf("((%s)) %s", player.Name, text)
		for _, memberID := range party.Members {
			member := deps.World.GetByCharID(memberID)
			if member != nil {
				sendGlobalChat(member.Session, ChatParty, msg)
			}
		}

	default:
		deps.Log.Debug("unhandled chat type", zap.Uint8("type", chatType))
	}
}

// HandleSay processes C_SAY (opcode 136).
// Java maps both C_SAY(136) and C_CHAT(40) to the same handler (C_Chat).
// Packet format is identical: [chatType:1byte][text:string].
func HandleSay(sess *net.Session, r *packet.Reader, deps *Deps) {
	// Same format as C_CHAT — read chatType first, then text
	HandleChat(sess, r, deps)
}

// HandleWhisper processes C_TELL (opcode 184) — private whisper.
func HandleWhisper(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetName := r.ReadS()
	text := r.ReadS()

	if targetName == "" || text == "" {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	target := deps.World.GetByName(targetName)
	if target == nil {
		sendServerMessage(sess, 73) // "Character not found"
		return
	}

	// Send to receiver: S_TELL (opcode 67)
	sendWhisperReceive(target.Session, player.Name, text)

	// Send confirmation to sender: S_MESSAGE (opcode 243) type 9
	outMsg := fmt.Sprintf("-> (%s) %s", targetName, text)
	sendGlobalChat(sess, 9, outMsg)
}

// --- Chat packet builders ---

// sendNormalChat sends S_SAY (opcode 81) type 0 — normal chat.
func sendNormalChat(sess *net.Session, senderID int32, msg string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SAY)
	w.WriteC(ChatNormal)
	w.WriteD(senderID)
	w.WriteS(msg)
	sess.Send(w.Bytes())
}

// sendShoutChat sends S_SAY (opcode 81) type 2 — shout.
func sendShoutChat(sess *net.Session, senderID int32, msg string, x, y int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SAY)
	w.WriteC(ChatShout)
	w.WriteD(senderID)
	w.WriteS(msg)
	w.WriteH(uint16(x))
	w.WriteH(uint16(y))
	sess.Send(w.Bytes())
}

// sendGlobalChat sends S_MESSAGE (opcode 243) — global/clan/trade/whisper-confirm.
func sendGlobalChat(sess *net.Session, chatType byte, msg string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE)
	w.WriteC(chatType)
	w.WriteS(msg)
	sess.Send(w.Bytes())
}

// sendWhisperReceive sends S_TELL (opcode 67) — incoming whisper.
func sendWhisperReceive(sess *net.Session, senderName, text string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_TELL)
	w.WriteS(senderName)
	w.WriteS(text)
	sess.Send(w.Bytes())
}
