package handler

import (
	"fmt"
	"strings"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// ==================== Client Packet Handlers ====================

// HandleInviteParty processes C_WHO_PARTY (opcode 230) = Java C_CreateParty.
// Format: [C type] + type 0/1: [D targetID], type 2: [S name], type 3: [D targetID]
// Type 0 = normal party invite, 1 = auto-share party invite,
// 2 = chat party invite, 3 = leader transfer.
func HandleInviteParty(sess *net.Session, r *packet.Reader, deps *Deps) {
	partyType := r.ReadC()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	switch partyType {
	case 0, 1:
		handlePartyInvite(sess, player, r, deps, partyType)
	case 2:
		handleChatPartyInvite(sess, player, r, deps)
	case 3:
		handleLeaderTransfer(sess, player, r, deps)
	}
}

// HandleWhoParty processes C_INVITE_PARTY_TARGET (opcode 43) = Java C_Party.
// Format: (no extra data) — queries the player's own party info.
func HandleWhoParty(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if !deps.World.Parties.IsInParty(player.CharID) {
		sendServerMessage(sess, 425) // 您並沒有參加任何隊伍。
		return
	}

	party := deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}

	// Java: S_Party("party", objid, leaderName, membersNameList)
	// Uses S_OPCODE_SHOWHTML (39) — an HTML dialog showing party info
	sendPartyHtmlDialog(sess, player.CharID, party, deps)
}

// HandleLeaveParty processes C_LEAVE_PARTY (opcode 33).
// Java: if in party → party.leaveMember(player)
func HandleLeaveParty(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || !deps.World.Parties.IsInParty(player.CharID) {
		return
	}

	partyLeaveMember(player, deps)
}

// HandleBanishParty processes C_BANISH_PARTY (opcode 255) — kick member.
// Java C_BanParty format: [S memberName] (reads NAME, not ID!)
func HandleBanishParty(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetName := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil || !deps.World.Parties.IsInParty(player.CharID) {
		return
	}

	if !deps.World.Parties.IsLeader(player.CharID) {
		sendServerMessage(sess, 427) // 只有領導者才有驅逐隊伍成員的權力。
		return
	}

	party := deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}

	// Find target by name (case-insensitive, matching Java)
	var target *world.PlayerInfo
	for _, memberID := range party.Members {
		member := deps.World.GetByCharID(memberID)
		if member != nil && strings.EqualFold(member.Name, targetName) {
			target = member
			break
		}
	}

	if target == nil {
		sendServerMessageArgs(sess, 426, targetName) // %0 不屬於任何隊伍。
		return
	}

	partyKickMember(target, deps)
}

// HandlePartyControl processes C_CHAT_PARTY_CONTROL (opcode 199) = Java C_ChatParty.
// Format: [C action] + action 0: [S name], action 1: (none), action 2: (none)
// Action 0 = chat party kick, 1 = chat party leave, 2 = chat party list.
func HandlePartyControl(sess *net.Session, r *packet.Reader, deps *Deps) {
	action := r.ReadC()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	switch action {
	case 0: // /chatbanish — kick member by name
		name := r.ReadS()
		if !deps.World.ChatParties.IsInParty(player.CharID) {
			sendServerMessage(sess, 425) // 您並沒有參加任何隊伍。
			return
		}
		if !deps.World.ChatParties.IsLeader(player.CharID) {
			sendServerMessage(sess, 427) // 只有領導者才有驅逐隊伍成員的權力。
			return
		}
		target := deps.World.GetByName(name)
		if target == nil {
			sendServerMessage(sess, 109) // 沒有叫%0的人。
			return
		}
		if target.CharID == player.CharID {
			return
		}
		chatParty := deps.World.ChatParties.GetParty(player.CharID)
		if chatParty == nil {
			return
		}
		found := false
		for _, id := range chatParty.Members {
			if id == target.CharID {
				found = true
				break
			}
		}
		if !found {
			sendServerMessageArgs(sess, 426, name) // %0 不屬於任何隊伍。
			return
		}
		chatPartyKickMember(target, deps)

	case 1: // /chatoutparty — leave chat party
		if deps.World.ChatParties.IsInParty(player.CharID) {
			chatPartyLeaveMember(player, deps)
		}

	case 2: // /chatparty — list chat party members
		if !deps.World.ChatParties.IsInParty(player.CharID) {
			sendServerMessage(sess, 425) // 您並沒有參加任何隊伍。
			return
		}
		chatParty := deps.World.ChatParties.GetParty(player.CharID)
		if chatParty == nil {
			return
		}
		sendChatPartyHtmlDialog(sess, player.CharID, chatParty, deps)
	}
}

// ==================== Invite Logic ====================

// handlePartyInvite processes type 0/1 (normal/auto-share party invite).
// Java: sends S_Message_YN(953 or 954) to target, waits for C_Attr response.
func handlePartyInvite(sess *net.Session, player *world.PlayerInfo, r *packet.Reader, deps *Deps, partyType byte) {
	targetID := r.ReadD()
	target := deps.World.GetByCharID(targetID)
	if target == nil || target.CharID == player.CharID {
		return
	}

	// Distance check (7 tiles + same screen + same map)
	if !isInRange(player, target, 7) {
		sendServerMessage(sess, 952) // 對象不在畫面內
		return
	}

	// Target already in a party
	if deps.World.Parties.IsInParty(target.CharID) {
		sendServerMessage(sess, 415) // 您無法邀請已經參加其他隊伍的人。
		return
	}

	if deps.World.Parties.IsInParty(player.CharID) {
		// Already in a party — must be leader to invite
		if !deps.World.Parties.IsLeader(player.CharID) {
			sendServerMessage(sess, 416) // 只有領導者才能邀請其他的成員。
			return
		}
		party := deps.World.Parties.GetParty(player.CharID)
		if party != nil && len(party.Members) >= world.MaxPartySize {
			sendServerMessage(sess, 417) // 你的隊伍已經滿了，無法再接受隊員。
			return
		}
	}

	// Store invite context on target
	// Java: targetPc.setPartyType(type); targetPc.setPartyID(pc.getId());
	target.PendingYesNoData = player.CharID
	target.PartyInviteType = partyType

	// Also store on inviter if they're not in a party yet (for type tracking)
	if !deps.World.Parties.IsInParty(player.CharID) {
		player.PartyInviteType = partyType
	}

	// Send S_Message_YN to target
	var msgType uint16
	if partyType == 0 {
		msgType = 953 // 玩家 %0 邀請您加入隊伍？(Y/N)
	} else {
		msgType = 954 // 玩家 %0 邀請您加入自動分配隊伍？(Y/N)
	}
	target.PendingYesNoType = int16(msgType)
	sendYesNoDialog(target.Session, msgType, player.Name)

	deps.Log.Debug("party invite sent",
		zap.String("inviter", player.Name),
		zap.String("target", target.Name),
		zap.Uint8("type", partyType),
	)
}

// handleChatPartyInvite processes type 2 (chat party invite).
// Java: reads target name (string), sends S_Message_YN(951).
func handleChatPartyInvite(sess *net.Session, player *world.PlayerInfo, r *packet.Reader, deps *Deps) {
	targetName := r.ReadS()
	target := deps.World.GetByName(targetName)

	if target == nil {
		sendServerMessage(sess, 109) // 沒有叫%0的人。
		return
	}
	if target.CharID == player.CharID {
		return
	}
	if !isInRange(player, target, 7) {
		sendServerMessage(sess, 952) // 對象不在畫面內
		return
	}
	if deps.World.ChatParties.IsInParty(target.CharID) {
		sendServerMessage(sess, 415) // 您無法邀請已經參加其他隊伍的人。
		return
	}
	if deps.World.ChatParties.IsInParty(player.CharID) {
		if !deps.World.ChatParties.IsLeader(player.CharID) {
			sendServerMessage(sess, 416) // 只有領導者才能邀請其他的成員。
			return
		}
	}

	// Store invite context
	target.PendingYesNoType = 951 // chat party invite
	target.PendingYesNoData = player.CharID

	sendYesNoDialog(target.Session, 951, player.Name) // 您要接受玩家 %0 提出的隊伍對話邀請嗎？(Y/N)
}

// handleLeaderTransfer processes type 3 (leader transfer).
// Java: checks leader status, sends S_Message_YN(1703) to self, then passLeader.
func handleLeaderTransfer(sess *net.Session, player *world.PlayerInfo, r *packet.Reader, deps *Deps) {
	targetID := r.ReadD()

	party := deps.World.Parties.GetParty(player.CharID)
	if party == nil || !deps.World.Parties.IsLeader(player.CharID) {
		sendServerMessage(sess, 1697) // 非隊長
		return
	}

	target := deps.World.GetByCharID(targetID)
	if target == nil || target.CharID == player.CharID {
		return
	}

	if !deps.World.Parties.IsInParty(target.CharID) || target.PartyID != player.PartyID {
		sendServerMessage(sess, 1696) // 目標不在隊伍中
		return
	}

	if !isInRange(player, target, 7) {
		sendServerMessage(sess, 1695) // 對象不在畫面內
		return
	}

	// Java: pc.sendPackets(new S_Message_YN(1703, ""));
	// Then immediately calls passLeader. The 1703 YN is a self-confirmation
	// but Java doesn't actually wait for the response — it calls passLeader right away.
	// We match this behavior.
	sendYesNoDialog(sess, 1703, "")

	// Transfer leadership immediately (matching Java)
	deps.World.Parties.SetLeader(player.CharID, target.CharID)

	// Update all members
	newParty := deps.World.Parties.GetParty(target.CharID)
	if newParty != nil {
		for _, memberID := range newParty.Members {
			member := deps.World.GetByCharID(memberID)
			if member != nil {
				member.PartyID = newParty.LeaderID
				member.PartyLeader = (memberID == newParty.LeaderID)
				// Java: S_Party(0x6A, pc) — PATRY_SET_MASTER
				sendPacketBoxSetMaster(member.Session, target.CharID)
			}
		}
	}

	deps.Log.Info(fmt.Sprintf("隊長轉移  原隊長=%s  新隊長=%s", player.Name, target.Name))
}

// ==================== YES/NO Response Handlers (called from attr.go) ====================

// HandlePartyInviteResponse handles C_Attr case 953 (normal party) and 954 (auto-share party).
// inviterID is passed from HandleAttr (saved before PendingYesNoData was cleared).
func HandlePartyInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool, deps *Deps) {
	inviter := deps.World.GetByCharID(inviterID)

	if !accepted {
		if inviter != nil {
			sendServerMessageArgs(inviter.Session, 423, player.Name) // %0 拒絕了您的邀請。
		}
		return
	}

	if inviter == nil {
		return
	}

	// Determine party type from the invite context
	pType := world.PartyType(player.PartyInviteType)

	if deps.World.Parties.IsInParty(inviter.CharID) {
		// Inviter already in party — add target to existing party
		party := deps.World.Parties.GetParty(inviter.CharID)
		if party == nil || len(party.Members) >= world.MaxPartySize {
			sendServerMessage(inviter.Session, 417) // 隊伍已滿
			return
		}
		if !deps.World.Parties.AddMember(party.LeaderID, player.CharID) {
			return
		}
		player.PartyID = party.LeaderID
		player.PartyLeader = false

		// Notify: send correct packet order per Java
		partyAddMemberNotify(player, party, deps)
	} else {
		// Create new party
		party := deps.World.Parties.CreateParty(inviter.CharID, player.CharID, pType)
		inviter.PartyID = party.LeaderID
		inviter.PartyLeader = true
		player.PartyID = party.LeaderID
		player.PartyLeader = false

		// Notify: Java showAddPartyInfo flow
		partyAddMemberNotify(player, party, deps)
	}

	// Notify inviter
	sendServerMessageArgs(inviter.Session, 424, player.Name) // %0 加入了您的隊伍。

	deps.Log.Info(fmt.Sprintf("加入隊伍  邀請者=%s  加入者=%s", inviter.Name, player.Name))
}

// HandleChatPartyInviteResponse handles C_Attr case 951 (chat party invite).
// inviterID is passed from HandleAttr (saved before PendingYesNoData was cleared).
func HandleChatPartyInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool, deps *Deps) {
	inviter := deps.World.GetByCharID(inviterID)

	if !accepted {
		if inviter != nil {
			sendServerMessageArgs(inviter.Session, 423, player.Name) // %0 拒絕了您的邀請。
		}
		return
	}

	if inviter == nil {
		return
	}

	if deps.World.ChatParties.IsInParty(inviter.CharID) {
		chatParty := deps.World.ChatParties.GetParty(inviter.CharID)
		if chatParty == nil || len(chatParty.Members) >= world.MaxChatPartySize {
			sendServerMessage(inviter.Session, 417) // 隊伍已滿
			return
		}
		deps.World.ChatParties.AddMember(chatParty.LeaderID, player.CharID)
	} else {
		deps.World.ChatParties.CreateParty(inviter.CharID, player.CharID)
	}

	sendServerMessageArgs(inviter.Session, 424, player.Name) // %0 加入了您的隊伍。
}

// ==================== Party Leave/Kick Logic (matching Java) ====================

// partyLeaveMember handles a player voluntarily leaving their party.
// Java L1Party.leaveMember: leader leaves OR only 2 members → breakup (dissolve all).
func partyLeaveMember(player *world.PlayerInfo, deps *Deps) {
	party := deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}

	isLeader := party.LeaderID == player.CharID
	memberCount := len(party.Members)

	if isLeader || memberCount == 2 {
		// Java: breakup() — dissolve entire party
		partyBreakup(party, deps)
	} else {
		// Non-leader leaves, party continues
		partyID := party.LeaderID
		deps.World.Parties.RemoveMember(player.CharID)
		player.PartyID = 0
		player.PartyLeader = false

		// Send HP meter clear (0xFF) between leaving player and remaining members
		sendHpMeterClear(player, party.Members, deps)

		// Notify remaining members: "%0 離開了隊伍"
		remainingParty := deps.World.Parties.GetParty(partyID)
		if remainingParty != nil {
			for _, memberID := range remainingParty.Members {
				member := deps.World.GetByCharID(memberID)
				if member != nil {
					sendServerMessageArgs(member.Session, 420, player.Name) // %0離開了隊伍
				}
			}
		}

		// Notify the leaving player
		sendServerMessageArgs(player.Session, 420, player.Name) // %0離開了隊伍
	}
}

// partyKickMember handles kicking a member from the party.
// Java L1Party.kickMember: 2 members → breakup. Otherwise just remove.
func partyKickMember(target *world.PlayerInfo, deps *Deps) {
	party := deps.World.Parties.GetParty(target.CharID)
	if party == nil {
		return
	}

	if len(party.Members) == 2 {
		// Java: breakup()
		partyBreakup(party, deps)
	} else {
		partyID := party.LeaderID
		deps.World.Parties.RemoveMember(target.CharID)
		target.PartyID = 0
		target.PartyLeader = false

		// Send HP meter clear
		sendHpMeterClear(target, party.Members, deps)

		// Notify remaining
		remainingParty := deps.World.Parties.GetParty(partyID)
		if remainingParty != nil {
			for _, memberID := range remainingParty.Members {
				member := deps.World.GetByCharID(memberID)
				if member != nil {
					sendServerMessageArgs(member.Session, 420, target.Name) // %0離開了隊伍
				}
			}
		}

		// Notify kicked player
		sendServerMessage(target.Session, 419) // 被踢出隊伍
	}
}

// partyBreakup dissolves an entire party. Matches Java L1Party.breakup().
func partyBreakup(party *world.PartyInfo, deps *Deps) {
	// Collect all member refs before dissolving
	members := make([]*world.PlayerInfo, 0, len(party.Members))
	for _, id := range party.Members {
		m := deps.World.GetByCharID(id)
		if m != nil {
			members = append(members, m)
		}
	}

	// Dissolve party data
	deps.World.Parties.Dissolve(party.LeaderID)

	// Clear HP meters between all members
	for i, a := range members {
		for j, b := range members {
			if i != j {
				sendHpMeter(a.Session, b.CharID, 0xFF)
			}
		}
		// Also clear self HP meter
		sendHpMeter(a.Session, a.CharID, 0xFF)
	}

	// Reset state and notify
	for _, m := range members {
		m.PartyID = 0
		m.PartyLeader = false
		sendServerMessage(m.Session, 418) // 您解散您的隊伍了!!
	}
}

// ==================== Chat Party Leave/Kick ====================

// chatPartyLeaveMember handles leaving a chat party.
// Java L1ChatParty.leaveMember: leader → breakup. 2 members → dissolve. Otherwise just remove.
func chatPartyLeaveMember(player *world.PlayerInfo, deps *Deps) {
	chatParty := deps.World.ChatParties.GetParty(player.CharID)
	if chatParty == nil {
		return
	}

	isLeader := chatParty.LeaderID == player.CharID

	if isLeader {
		// Leader leaves → breakup entire chat party
		chatPartyBreakup(chatParty, deps)
	} else if len(chatParty.Members) == 2 {
		// Only 2 members, non-leader leaves → dissolve
		deps.World.ChatParties.RemoveMember(player.CharID)
		leader := deps.World.GetByCharID(chatParty.LeaderID)
		if leader != nil {
			deps.World.ChatParties.Dissolve(chatParty.LeaderID)
			sendServerMessageArgs(player.Session, 420, player.Name)
			sendServerMessageArgs(leader.Session, 420, player.Name)
		}
	} else {
		partyID := chatParty.LeaderID
		deps.World.ChatParties.RemoveMember(player.CharID)
		remaining := deps.World.ChatParties.GetParty(partyID)
		if remaining != nil {
			for _, memberID := range remaining.Members {
				member := deps.World.GetByCharID(memberID)
				if member != nil {
					sendServerMessageArgs(member.Session, 420, player.Name)
				}
			}
		}
		sendServerMessageArgs(player.Session, 420, player.Name)
	}
}

// chatPartyKickMember handles kicking from chat party.
func chatPartyKickMember(target *world.PlayerInfo, deps *Deps) {
	chatParty := deps.World.ChatParties.GetParty(target.CharID)
	if chatParty == nil {
		return
	}

	if len(chatParty.Members) == 2 {
		deps.World.ChatParties.RemoveMember(target.CharID)
		leader := deps.World.GetByCharID(chatParty.LeaderID)
		if leader != nil {
			deps.World.ChatParties.Dissolve(chatParty.LeaderID)
		}
	} else {
		deps.World.ChatParties.RemoveMember(target.CharID)
	}
	sendServerMessage(target.Session, 419) // 被踢出隊伍
}

// chatPartyBreakup dissolves an entire chat party.
func chatPartyBreakup(chatParty *world.ChatPartyInfo, deps *Deps) {
	members := make([]*world.PlayerInfo, 0, len(chatParty.Members))
	for _, id := range chatParty.Members {
		m := deps.World.GetByCharID(id)
		if m != nil {
			members = append(members, m)
		}
	}
	deps.World.ChatParties.Dissolve(chatParty.LeaderID)
	for _, m := range members {
		sendServerMessage(m.Session, 418) // 隊伍已解散
	}
}

// ==================== Party Join Notification (matching Java packet order) ====================

// partyAddMemberNotify sends the correct packet sequence when a new member joins.
// Java L1Party.showAddPartyInfo:
//   - New member receives S_Party(0x68) = full party list
//   - Existing members receive S_Party(0x69) = new member info
//   - All members receive S_Party(0x6E) = position refresh
//   - All members exchange S_HPMeter
func partyAddMemberNotify(newMember *world.PlayerInfo, party *world.PartyInfo, deps *Deps) {
	// 1. New member gets full party list: sub-type 0x68 (104)
	sendPacketBoxFullPartyList(newMember.Session, party, deps)

	// 2. Existing members get new member notification: sub-type 0x69 (105)
	for _, memberID := range party.Members {
		if memberID == newMember.CharID {
			continue
		}
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			sendPacketBoxNewMember(member.Session, newMember)
		}
	}

	// 3. All members get position refresh: sub-type 0x6E (110)
	for _, memberID := range party.Members {
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			sendPacketBoxPartyRefresh(member.Session, party, deps)
		}
	}

	// 4. Exchange HP meters between all members
	for _, memberID := range party.Members {
		member := deps.World.GetByCharID(memberID)
		if member == nil {
			continue
		}
		for _, otherID := range party.Members {
			if otherID == memberID {
				continue
			}
			other := deps.World.GetByCharID(otherID)
			if other != nil {
				hp := world.CalcHPPercent(other.HP, other.MaxHP)
				sendHpMeter(member.Session, other.CharID, int16(hp))
			}
		}
	}
}

// ==================== Packet Builders ====================

// sendYesNoDialog sends S_Message_YN (opcode 219).
// Java format: [H 0x0000][D yesNoCount][H messageType][S param...]
func sendYesNoDialog(sess *net.Session, msgType uint16, args ...string) {
	count := yesNoCounter.Add(1)
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_YES_NO)
	w.WriteH(0)
	w.WriteD(count)
	w.WriteH(msgType)
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// sendHpMeterClear sends HP meter clear (0xFF) between a leaving player and remaining members.
// Java L1Party.deleteMiniHp: clear both directions + self.
func sendHpMeterClear(leaver *world.PlayerInfo, remainingIDs []int32, deps *Deps) {
	for _, memberID := range remainingIDs {
		if memberID == leaver.CharID {
			continue
		}
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			sendHpMeter(member.Session, leaver.CharID, 0xFF)
			sendHpMeter(leaver.Session, member.CharID, 0xFF)
		}
	}
	sendHpMeter(leaver.Session, leaver.CharID, 0xFF)
}

// sendPacketBoxFullPartyList sends S_PacketBox sub-type 104 (UPDATE_OLD_PART_MEMBER) — full party list.
// Java newMember(): [C 250][C 104][C nonLeaderCount]
//   [D leaderID][S name][C hp%][D mapID][H x][H y]
//   then for each non-leader: same format... [C 0x00]
func sendPacketBoxFullPartyList(sess *net.Session, party *world.PartyInfo, deps *Deps) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x68) // sub-type 104: UPDATE_OLD_PART_MEMBER

	nonLeaderCount := len(party.Members) - 1
	if nonLeaderCount < 0 {
		nonLeaderCount = 0
	}
	w.WriteC(byte(nonLeaderCount))

	// Leader first
	leader := deps.World.GetByCharID(party.LeaderID)
	if leader != nil {
		w.WriteD(leader.CharID)
		w.WriteS(leader.Name)
		w.WriteC(world.CalcHPPercent(leader.HP, leader.MaxHP))
		w.WriteD(int32(leader.MapID))
		w.WriteH(uint16(leader.X))
		w.WriteH(uint16(leader.Y))
	}

	// Non-leader members
	for _, memberID := range party.Members {
		if memberID == party.LeaderID {
			continue
		}
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			w.WriteD(member.CharID)
			w.WriteS(member.Name)
			w.WriteC(world.CalcHPPercent(member.HP, member.MaxHP))
			w.WriteD(int32(member.MapID))
			w.WriteH(uint16(member.X))
			w.WriteH(uint16(member.Y))
		}
	}

	w.WriteC(0x00) // terminator
	sess.Send(w.Bytes())
}

// sendPacketBoxNewMember sends S_PacketBox sub-type 105 (PATRY_UPDATE_MEMBER).
// Sent to existing members when a new member joins.
// Java oldMember(): [C 250][C 105][D id][S name][D mapID][H x][H y]
func sendPacketBoxNewMember(sess *net.Session, newMember *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x69) // sub-type 105: PATRY_UPDATE_MEMBER
	w.WriteD(newMember.CharID)
	w.WriteS(newMember.Name)
	w.WriteD(int32(newMember.MapID))
	w.WriteH(uint16(newMember.X))
	w.WriteH(uint16(newMember.Y))
	sess.Send(w.Bytes())
}

// sendPacketBoxSetMaster sends S_PacketBox sub-type 106 (PATRY_SET_MASTER).
// Java changeLeader(): [C 250][C 106][D newLeaderID][H 0]
func sendPacketBoxSetMaster(sess *net.Session, newLeaderID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x6A) // sub-type 106: PATRY_SET_MASTER
	w.WriteD(newLeaderID)
	w.WriteH(0)
	sess.Send(w.Bytes())
}

// sendPacketBoxPartyRefresh sends S_PacketBox sub-type 110 (PATRY_MEMBERS) — position refresh.
// Java refreshParty(): [C 250][C 110][C memberCount]
//   for each: [D id][D mapID][H x][H y] ... [C 0xFF][C 0xFF]
func sendPacketBoxPartyRefresh(sess *net.Session, party *world.PartyInfo, deps *Deps) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x6E) // sub-type 110: PATRY_MEMBERS
	w.WriteC(byte(len(party.Members)))
	for _, memberID := range party.Members {
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			w.WriteD(member.CharID)
			w.WriteD(int32(member.MapID))
			w.WriteH(uint16(member.X))
			w.WriteH(uint16(member.Y))
		}
	}
	w.WriteC(0xFF)
	w.WriteC(0xFF)
	sess.Send(w.Bytes())
}

// sendPartyHtmlDialog sends the party info HTML dialog (Java S_Party with S_OPCODE_SHOWHTML).
// Used by C_Party (opcode 43) to show party member list in an HTML window.
// Java format: [C 39][D objID][S "party"][H 1][H 2][S leaderName][S membersNameList]
func sendPartyHtmlDialog(sess *net.Session, selfCharID int32, party *world.PartyInfo, deps *Deps) {
	leader := deps.World.GetByCharID(party.LeaderID)
	if leader == nil {
		return
	}

	// Build space-separated member name list
	nameList := ""
	for _, memberID := range party.Members {
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			nameList += member.Name + " "
		}
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(selfCharID)
	w.WriteS("party")
	w.WriteH(1)
	w.WriteH(2)
	w.WriteS(leader.Name)
	w.WriteS(nameList)
	sess.Send(w.Bytes())
}

// sendChatPartyHtmlDialog sends the chat party info HTML dialog.
func sendChatPartyHtmlDialog(sess *net.Session, selfCharID int32, chatParty *world.ChatPartyInfo, deps *Deps) {
	leader := deps.World.GetByCharID(chatParty.LeaderID)
	if leader == nil {
		return
	}

	nameList := ""
	for _, memberID := range chatParty.Members {
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			nameList += member.Name + " "
		}
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(selfCharID)
	w.WriteS("party")
	w.WriteH(1)
	w.WriteH(2)
	w.WriteS(leader.Name)
	w.WriteS(nameList)
	sess.Send(w.Bytes())
}

// ==================== HP Update (for party members seeing each other's HP change) ====================

// UpdatePartyMiniHP sends updated HP meter to all party members of the given player.
// Should be called whenever a player's HP changes (combat, regen, etc.).
// Java L1Party.updateMiniHP.
func UpdatePartyMiniHP(player *world.PlayerInfo, deps *Deps) {
	party := deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}
	hp := world.CalcHPPercent(player.HP, player.MaxHP)
	for _, memberID := range party.Members {
		member := deps.World.GetByCharID(memberID)
		if member != nil {
			sendHpMeter(member.Session, player.CharID, int16(hp))
		}
	}
}

// RefreshPartyPositions sends party position refresh (sub-type 0x6E) to a specific player.
// Called by the party refresh timer (every 25 seconds per Java).
func RefreshPartyPositions(player *world.PlayerInfo, deps *Deps) {
	party := deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}
	sendPacketBoxPartyRefresh(player.Session, party, deps)
}

// ==================== Utility ====================

// isInRange checks if two players are within the given tile distance and on the same map.
func isInRange(a, b *world.PlayerInfo, maxDist int32) bool {
	if a.MapID != b.MapID {
		return false
	}
	dx := a.X - b.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - b.Y
	if dy < 0 {
		dy = -dy
	}
	dist := dx
	if dy > dist {
		dist = dy
	}
	return dist <= maxDist
}
