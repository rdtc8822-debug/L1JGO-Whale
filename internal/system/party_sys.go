package system

import (
	"fmt"
	"strings"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// PartySystem 負責所有隊伍邏輯（一般隊伍 + 聊天隊伍）。
// 實作 handler.PartyManager 介面。
type PartySystem struct {
	deps *handler.Deps
}

// NewPartySystem 建立隊伍系統。
func NewPartySystem(deps *handler.Deps) *PartySystem {
	return &PartySystem{deps: deps}
}

// ==================== 一般隊伍 ====================

// Invite 發送一般隊伍邀請（type 0=普通, 1=自動分配）。
func (s *PartySystem) Invite(sess *net.Session, player *world.PlayerInfo, targetID int32, partyType byte) {
	target := s.deps.World.GetByCharID(targetID)
	if target == nil || target.CharID == player.CharID {
		return
	}

	// 距離檢查（7 格 + 同地圖）
	if !isInRange(player, target, 7) {
		handler.SendServerMessage(sess, 952) // 對象不在畫面內
		return
	}

	// 目標已在隊伍中
	if s.deps.World.Parties.IsInParty(target.CharID) {
		handler.SendServerMessage(sess, 415) // 您無法邀請已經參加其他隊伍的人。
		return
	}

	if s.deps.World.Parties.IsInParty(player.CharID) {
		// 已在隊伍中 — 必須是隊長才能邀請
		if !s.deps.World.Parties.IsLeader(player.CharID) {
			handler.SendServerMessage(sess, 416) // 只有領導者才能邀請其他的成員。
			return
		}
		party := s.deps.World.Parties.GetParty(player.CharID)
		if party != nil && len(party.Members) >= world.MaxPartySize {
			handler.SendServerMessage(sess, 417) // 你的隊伍已經滿了，無法再接受隊員。
			return
		}
	}

	// 在目標上儲存邀請上下文
	target.PendingYesNoData = player.CharID
	target.PartyInviteType = partyType

	// 邀請者若尚未在隊伍中，也記錄類型
	if !s.deps.World.Parties.IsInParty(player.CharID) {
		player.PartyInviteType = partyType
	}

	// 發送 S_Message_YN 到目標
	var msgType uint16
	if partyType == 0 {
		msgType = 953 // 玩家 %0 邀請您加入隊伍？(Y/N)
	} else {
		msgType = 954 // 玩家 %0 邀請您加入自動分配隊伍？(Y/N)
	}
	target.PendingYesNoType = int16(msgType)
	handler.SendYesNoDialog(target.Session, msgType, player.Name)

	s.deps.Log.Debug("隊伍邀請已發送",
		zap.String("inviter", player.Name),
		zap.String("target", target.Name),
		zap.Uint8("type", partyType),
	)
}

// ChatInvite 發送聊天隊伍邀請（type 2）。
func (s *PartySystem) ChatInvite(sess *net.Session, player *world.PlayerInfo, targetName string) {
	target := s.deps.World.GetByName(targetName)

	if target == nil {
		handler.SendServerMessage(sess, 109) // 沒有叫%0的人。
		return
	}
	if target.CharID == player.CharID {
		return
	}
	if !isInRange(player, target, 7) {
		handler.SendServerMessage(sess, 952) // 對象不在畫面內
		return
	}
	if s.deps.World.ChatParties.IsInParty(target.CharID) {
		handler.SendServerMessage(sess, 415) // 您無法邀請已經參加其他隊伍的人。
		return
	}
	if s.deps.World.ChatParties.IsInParty(player.CharID) {
		if !s.deps.World.ChatParties.IsLeader(player.CharID) {
			handler.SendServerMessage(sess, 416) // 只有領導者才能邀請其他的成員。
			return
		}
	}

	// 儲存邀請上下文
	target.PendingYesNoType = 951 // 聊天隊伍邀請
	target.PendingYesNoData = player.CharID

	handler.SendYesNoDialog(target.Session, 951, player.Name) // 您要接受玩家 %0 提出的隊伍對話邀請嗎？(Y/N)
}

// TransferLeader 轉移隊長。
func (s *PartySystem) TransferLeader(sess *net.Session, player *world.PlayerInfo, targetID int32) {
	party := s.deps.World.Parties.GetParty(player.CharID)
	if party == nil || !s.deps.World.Parties.IsLeader(player.CharID) {
		handler.SendServerMessage(sess, 1697) // 非隊長
		return
	}

	target := s.deps.World.GetByCharID(targetID)
	if target == nil || target.CharID == player.CharID {
		return
	}

	if !s.deps.World.Parties.IsInParty(target.CharID) || target.PartyID != player.PartyID {
		handler.SendServerMessage(sess, 1696) // 目標不在隊伍中
		return
	}

	if !isInRange(player, target, 7) {
		handler.SendServerMessage(sess, 1695) // 對象不在畫面內
		return
	}

	// Java: 發 S_Message_YN(1703) 但立即執行轉移（不等待回應）
	handler.SendYesNoDialog(sess, 1703, "")

	// 立即轉移（匹配 Java 行為）
	s.deps.World.Parties.SetLeader(player.CharID, target.CharID)

	// 通知所有成員
	newParty := s.deps.World.Parties.GetParty(target.CharID)
	if newParty != nil {
		for _, memberID := range newParty.Members {
			member := s.deps.World.GetByCharID(memberID)
			if member != nil {
				member.PartyID = newParty.LeaderID
				member.PartyLeader = (memberID == newParty.LeaderID)
				sendPacketBoxSetMaster(member.Session, target.CharID)
			}
		}
	}

	s.deps.Log.Info(fmt.Sprintf("隊長轉移  原隊長=%s  新隊長=%s", player.Name, target.Name))
}

// ShowPartyInfo 顯示隊伍成員 HTML 對話框。
func (s *PartySystem) ShowPartyInfo(sess *net.Session, player *world.PlayerInfo) {
	if !s.deps.World.Parties.IsInParty(player.CharID) {
		handler.SendServerMessage(sess, 425) // 您並沒有參加任何隊伍。
		return
	}

	party := s.deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}

	sendPartyHtmlDialog(sess, player.CharID, party, s.deps)
}

// ==================== 一般隊伍 Leave/Kick ====================

// Leave 自願離開隊伍。
// Java L1Party.leaveMember: 隊長離開或只剩 2 人 → 解散。
func (s *PartySystem) Leave(player *world.PlayerInfo) {
	party := s.deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}

	isLeader := party.LeaderID == player.CharID
	memberCount := len(party.Members)

	if isLeader || memberCount == 2 {
		// 解散整個隊伍
		s.partyBreakup(party)
	} else {
		// 非隊長離開
		partyID := party.LeaderID
		s.deps.World.Parties.RemoveMember(player.CharID)
		player.PartyID = 0
		player.PartyLeader = false

		// 清除 HP 條
		s.sendHpMeterClear(player, party.Members)

		// 通知剩餘成員
		remainingParty := s.deps.World.Parties.GetParty(partyID)
		if remainingParty != nil {
			for _, memberID := range remainingParty.Members {
				member := s.deps.World.GetByCharID(memberID)
				if member != nil {
					handler.SendServerMessageArgs(member.Session, 420, player.Name) // %0離開了隊伍
				}
			}
		}

		// 通知離開的玩家
		handler.SendServerMessageArgs(player.Session, 420, player.Name) // %0離開了隊伍
	}
}

// BanishMember 踢除隊員（隊長專用，依名稱）。
func (s *PartySystem) BanishMember(sess *net.Session, player *world.PlayerInfo, targetName string) {
	if !s.deps.World.Parties.IsInParty(player.CharID) {
		return
	}

	if !s.deps.World.Parties.IsLeader(player.CharID) {
		handler.SendServerMessage(sess, 427) // 只有領導者才有驅逐隊伍成員的權力。
		return
	}

	party := s.deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}

	// 依名稱尋找目標（不分大小寫，匹配 Java）
	var target *world.PlayerInfo
	for _, memberID := range party.Members {
		member := s.deps.World.GetByCharID(memberID)
		if member != nil && strings.EqualFold(member.Name, targetName) {
			target = member
			break
		}
	}

	if target == nil {
		handler.SendServerMessageArgs(sess, 426, targetName) // %0 不屬於任何隊伍。
		return
	}

	s.partyKickMember(target)
}

// partyKickMember 踢除成員。
// Java L1Party.kickMember: 2 人 → 解散。否則只移除。
func (s *PartySystem) partyKickMember(target *world.PlayerInfo) {
	party := s.deps.World.Parties.GetParty(target.CharID)
	if party == nil {
		return
	}

	if len(party.Members) == 2 {
		s.partyBreakup(party)
	} else {
		partyID := party.LeaderID
		s.deps.World.Parties.RemoveMember(target.CharID)
		target.PartyID = 0
		target.PartyLeader = false

		// 清除 HP 條
		s.sendHpMeterClear(target, party.Members)

		// 通知剩餘成員
		remainingParty := s.deps.World.Parties.GetParty(partyID)
		if remainingParty != nil {
			for _, memberID := range remainingParty.Members {
				member := s.deps.World.GetByCharID(memberID)
				if member != nil {
					handler.SendServerMessageArgs(member.Session, 420, target.Name) // %0離開了隊伍
				}
			}
		}

		// 通知被踢玩家
		handler.SendServerMessage(target.Session, 419) // 被踢出隊伍
	}
}

// partyBreakup 解散整個隊伍。匹配 Java L1Party.breakup()。
func (s *PartySystem) partyBreakup(party *world.PartyInfo) {
	// 解散前先收集所有成員引用
	members := make([]*world.PlayerInfo, 0, len(party.Members))
	for _, id := range party.Members {
		m := s.deps.World.GetByCharID(id)
		if m != nil {
			members = append(members, m)
		}
	}

	// 解散隊伍資料
	s.deps.World.Parties.Dissolve(party.LeaderID)

	// 清除所有成員之間的 HP 條
	for i, a := range members {
		for j, b := range members {
			if i != j {
				handler.SendHpMeter(a.Session, b.CharID, 0xFF)
			}
		}
		// 也清除自己的 HP 條
		handler.SendHpMeter(a.Session, a.CharID, 0xFF)
	}

	// 重置狀態並通知
	for _, m := range members {
		m.PartyID = 0
		m.PartyLeader = false
		handler.SendServerMessage(m.Session, 418) // 您解散您的隊伍了!!
	}
}

// ==================== YES/NO 回應 ====================

// InviteResponse 處理一般隊伍邀請的 Yes/No 回應（953/954）。
func (s *PartySystem) InviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool) {
	inviter := s.deps.World.GetByCharID(inviterID)

	if !accepted {
		if inviter != nil {
			handler.SendServerMessageArgs(inviter.Session, 423, player.Name) // %0 拒絕了您的邀請。
		}
		return
	}

	if inviter == nil {
		return
	}

	// 從邀請上下文取得隊伍類型
	pType := world.PartyType(player.PartyInviteType)

	if s.deps.World.Parties.IsInParty(inviter.CharID) {
		// 邀請者已在隊伍中 — 加入現有隊伍
		party := s.deps.World.Parties.GetParty(inviter.CharID)
		if party == nil || len(party.Members) >= world.MaxPartySize {
			handler.SendServerMessage(inviter.Session, 417) // 隊伍已滿
			return
		}
		if !s.deps.World.Parties.AddMember(party.LeaderID, player.CharID) {
			return
		}
		player.PartyID = party.LeaderID
		player.PartyLeader = false

		// 通知
		s.partyAddMemberNotify(player, party)
	} else {
		// 建立新隊伍
		party := s.deps.World.Parties.CreateParty(inviter.CharID, player.CharID, pType)
		inviter.PartyID = party.LeaderID
		inviter.PartyLeader = true
		player.PartyID = party.LeaderID
		player.PartyLeader = false

		// 通知
		s.partyAddMemberNotify(player, party)
	}

	// 通知邀請者
	handler.SendServerMessageArgs(inviter.Session, 424, player.Name) // %0 加入了您的隊伍。

	s.deps.Log.Info(fmt.Sprintf("加入隊伍  邀請者=%s  加入者=%s", inviter.Name, player.Name))
}

// ChatInviteResponse 處理聊天隊伍邀請的 Yes/No 回應（951）。
func (s *PartySystem) ChatInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool) {
	inviter := s.deps.World.GetByCharID(inviterID)

	if !accepted {
		if inviter != nil {
			handler.SendServerMessageArgs(inviter.Session, 423, player.Name) // %0 拒絕了您的邀請。
		}
		return
	}

	if inviter == nil {
		return
	}

	if s.deps.World.ChatParties.IsInParty(inviter.CharID) {
		chatParty := s.deps.World.ChatParties.GetParty(inviter.CharID)
		if chatParty == nil || len(chatParty.Members) >= world.MaxChatPartySize {
			handler.SendServerMessage(inviter.Session, 417) // 隊伍已滿
			return
		}
		s.deps.World.ChatParties.AddMember(chatParty.LeaderID, player.CharID)
	} else {
		s.deps.World.ChatParties.CreateParty(inviter.CharID, player.CharID)
	}

	handler.SendServerMessageArgs(inviter.Session, 424, player.Name) // %0 加入了您的隊伍。
}

// ==================== 聊天隊伍 Leave/Kick ====================

// ChatLeave 離開聊天隊伍。
// Java L1ChatParty.leaveMember: 隊長 → 解散。2 人 → 解散。否則只移除。
func (s *PartySystem) ChatLeave(player *world.PlayerInfo) {
	chatParty := s.deps.World.ChatParties.GetParty(player.CharID)
	if chatParty == nil {
		return
	}

	isLeader := chatParty.LeaderID == player.CharID

	if isLeader {
		// 隊長離開 → 解散
		s.chatPartyBreakup(chatParty)
	} else if len(chatParty.Members) == 2 {
		// 只剩 2 人，非隊長離開 → 解散
		s.deps.World.ChatParties.RemoveMember(player.CharID)
		leader := s.deps.World.GetByCharID(chatParty.LeaderID)
		if leader != nil {
			s.deps.World.ChatParties.Dissolve(chatParty.LeaderID)
			handler.SendServerMessageArgs(player.Session, 420, player.Name)
			handler.SendServerMessageArgs(leader.Session, 420, player.Name)
		}
	} else {
		partyID := chatParty.LeaderID
		s.deps.World.ChatParties.RemoveMember(player.CharID)
		remaining := s.deps.World.ChatParties.GetParty(partyID)
		if remaining != nil {
			for _, memberID := range remaining.Members {
				member := s.deps.World.GetByCharID(memberID)
				if member != nil {
					handler.SendServerMessageArgs(member.Session, 420, player.Name)
				}
			}
		}
		handler.SendServerMessageArgs(player.Session, 420, player.Name)
	}
}

// ChatKick 踢除聊天隊伍成員。
func (s *PartySystem) ChatKick(sess *net.Session, player *world.PlayerInfo, targetName string) {
	if !s.deps.World.ChatParties.IsInParty(player.CharID) {
		handler.SendServerMessage(sess, 425) // 您並沒有參加任何隊伍。
		return
	}
	if !s.deps.World.ChatParties.IsLeader(player.CharID) {
		handler.SendServerMessage(sess, 427) // 只有領導者才有驅逐隊伍成員的權力。
		return
	}
	target := s.deps.World.GetByName(targetName)
	if target == nil {
		handler.SendServerMessage(sess, 109) // 沒有叫%0的人。
		return
	}
	if target.CharID == player.CharID {
		return
	}
	chatParty := s.deps.World.ChatParties.GetParty(player.CharID)
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
		handler.SendServerMessageArgs(sess, 426, targetName) // %0 不屬於任何隊伍。
		return
	}
	s.chatPartyKickMember(target)
}

// ShowChatPartyInfo 顯示聊天隊伍成員 HTML 對話框。
func (s *PartySystem) ShowChatPartyInfo(sess *net.Session, player *world.PlayerInfo) {
	if !s.deps.World.ChatParties.IsInParty(player.CharID) {
		handler.SendServerMessage(sess, 425) // 您並沒有參加任何隊伍。
		return
	}
	chatParty := s.deps.World.ChatParties.GetParty(player.CharID)
	if chatParty == nil {
		return
	}
	sendChatPartyHtmlDialog(sess, player.CharID, chatParty, s.deps)
}

// chatPartyKickMember 踢除聊天隊伍成員。
func (s *PartySystem) chatPartyKickMember(target *world.PlayerInfo) {
	chatParty := s.deps.World.ChatParties.GetParty(target.CharID)
	if chatParty == nil {
		return
	}

	if len(chatParty.Members) == 2 {
		s.deps.World.ChatParties.RemoveMember(target.CharID)
		leader := s.deps.World.GetByCharID(chatParty.LeaderID)
		if leader != nil {
			s.deps.World.ChatParties.Dissolve(chatParty.LeaderID)
		}
	} else {
		s.deps.World.ChatParties.RemoveMember(target.CharID)
	}
	handler.SendServerMessage(target.Session, 419) // 被踢出隊伍
}

// chatPartyBreakup 解散整個聊天隊伍。
func (s *PartySystem) chatPartyBreakup(chatParty *world.ChatPartyInfo) {
	members := make([]*world.PlayerInfo, 0, len(chatParty.Members))
	for _, id := range chatParty.Members {
		m := s.deps.World.GetByCharID(id)
		if m != nil {
			members = append(members, m)
		}
	}
	s.deps.World.ChatParties.Dissolve(chatParty.LeaderID)
	for _, m := range members {
		handler.SendServerMessage(m.Session, 418) // 隊伍已解散
	}
}

// ==================== 加入通知 ====================

// partyAddMemberNotify 新成員加入時發送正確的封包順序。
// Java L1Party.showAddPartyInfo:
//   - 新成員收到 S_Party(0x68) = 完整隊伍列表
//   - 現有成員收到 S_Party(0x69) = 新成員資訊
//   - 所有成員收到 S_Party(0x6E) = 位置更新
//   - 所有成員交換 S_HPMeter
func (s *PartySystem) partyAddMemberNotify(newMember *world.PlayerInfo, party *world.PartyInfo) {
	// 1. 新成員收到完整列表: sub-type 0x68 (104)
	sendPacketBoxFullPartyList(newMember.Session, party, s.deps)

	// 2. 現有成員收到新成員通知: sub-type 0x69 (105)
	for _, memberID := range party.Members {
		if memberID == newMember.CharID {
			continue
		}
		member := s.deps.World.GetByCharID(memberID)
		if member != nil {
			sendPacketBoxNewMember(member.Session, newMember)
		}
	}

	// 3. 所有成員收到位置更新: sub-type 0x6E (110)
	for _, memberID := range party.Members {
		member := s.deps.World.GetByCharID(memberID)
		if member != nil {
			sendPacketBoxPartyRefresh(member.Session, party, s.deps)
		}
	}

	// 4. 交換 HP 條
	for _, memberID := range party.Members {
		member := s.deps.World.GetByCharID(memberID)
		if member == nil {
			continue
		}
		for _, otherID := range party.Members {
			if otherID == memberID {
				continue
			}
			other := s.deps.World.GetByCharID(otherID)
			if other != nil {
				hp := world.CalcHPPercent(other.HP, other.MaxHP)
				handler.SendHpMeter(member.Session, other.CharID, int16(hp))
			}
		}
	}
}

// ==================== HP 更新 ====================

// UpdateMiniHP 廣播 HP 變化到所有隊伍成員。
// Java L1Party.updateMiniHP。
func (s *PartySystem) UpdateMiniHP(player *world.PlayerInfo) {
	party := s.deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}
	hp := world.CalcHPPercent(player.HP, player.MaxHP)
	for _, memberID := range party.Members {
		member := s.deps.World.GetByCharID(memberID)
		if member != nil {
			handler.SendHpMeter(member.Session, player.CharID, int16(hp))
		}
	}
}

// RefreshPositions 發送位置更新 (sub-type 0x6E) 到該玩家。
// 由 PartyRefreshSystem 每固定間隔呼叫。
func (s *PartySystem) RefreshPositions(player *world.PlayerInfo) {
	party := s.deps.World.Parties.GetParty(player.CharID)
	if party == nil {
		return
	}
	sendPacketBoxPartyRefresh(player.Session, party, s.deps)
}

// ==================== 輔助函式 ====================

// sendHpMeterClear 清除離開者與剩餘成員之間的 HP 條。
// Java L1Party.deleteMiniHp: 雙向清除 + 自身。
func (s *PartySystem) sendHpMeterClear(leaver *world.PlayerInfo, remainingIDs []int32) {
	for _, memberID := range remainingIDs {
		if memberID == leaver.CharID {
			continue
		}
		member := s.deps.World.GetByCharID(memberID)
		if member != nil {
			handler.SendHpMeter(member.Session, leaver.CharID, 0xFF)
			handler.SendHpMeter(leaver.Session, member.CharID, 0xFF)
		}
	}
	handler.SendHpMeter(leaver.Session, leaver.CharID, 0xFF)
}

// isInRange 檢查兩個玩家是否在指定格距內且在同一地圖。
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

// ==================== 封包建構 ====================

// sendPacketBoxFullPartyList 發送 S_PacketBox sub-type 104 (UPDATE_OLD_PART_MEMBER) — 完整隊伍列表。
// Java newMember(): [C 250][C 104][C nonLeaderCount]
//
//	[D leaderID][S name][C hp%][D mapID][H x][H y]
//	然後每個非隊長: 相同格式... [C 0x00]
func sendPacketBoxFullPartyList(sess *net.Session, party *world.PartyInfo, deps *handler.Deps) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x68) // sub-type 104: UPDATE_OLD_PART_MEMBER

	nonLeaderCount := len(party.Members) - 1
	if nonLeaderCount < 0 {
		nonLeaderCount = 0
	}
	w.WriteC(byte(nonLeaderCount))

	// 隊長先
	leader := deps.World.GetByCharID(party.LeaderID)
	if leader != nil {
		w.WriteD(leader.CharID)
		w.WriteS(leader.Name)
		w.WriteC(world.CalcHPPercent(leader.HP, leader.MaxHP))
		w.WriteD(int32(leader.MapID))
		w.WriteH(uint16(leader.X))
		w.WriteH(uint16(leader.Y))
	}

	// 非隊長成員
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

	w.WriteC(0x00) // 終止符
	sess.Send(w.Bytes())
}

// sendPacketBoxNewMember 發送 S_PacketBox sub-type 105 (PATRY_UPDATE_MEMBER)。
// 當新成員加入時發給現有成員。
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

// sendPacketBoxSetMaster 發送 S_PacketBox sub-type 106 (PATRY_SET_MASTER)。
// Java changeLeader(): [C 250][C 106][D newLeaderID][H 0]
func sendPacketBoxSetMaster(sess *net.Session, newLeaderID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(0x6A) // sub-type 106: PATRY_SET_MASTER
	w.WriteD(newLeaderID)
	w.WriteH(0)
	sess.Send(w.Bytes())
}

// sendPacketBoxPartyRefresh 發送 S_PacketBox sub-type 110 (PATRY_MEMBERS) — 位置更新。
// Java refreshParty(): [C 250][C 110][C memberCount]
//
//	每個: [D id][D mapID][H x][H y] ... [C 0xFF][C 0xFF]
func sendPacketBoxPartyRefresh(sess *net.Session, party *world.PartyInfo, deps *handler.Deps) {
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

// sendPartyHtmlDialog 發送隊伍資訊 HTML 對話框。
// Java 格式: [C 39][D objID][S "party"][H 1][H 2][S leaderName][S membersNameList]
func sendPartyHtmlDialog(sess *net.Session, selfCharID int32, party *world.PartyInfo, deps *handler.Deps) {
	leader := deps.World.GetByCharID(party.LeaderID)
	if leader == nil {
		return
	}

	// 建構空格分隔的成員名稱列表
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

// sendChatPartyHtmlDialog 發送聊天隊伍資訊 HTML 對話框。
func sendChatPartyHtmlDialog(sess *net.Session, selfCharID int32, chatParty *world.ChatPartyInfo, deps *handler.Deps) {
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
