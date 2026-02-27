package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// ==================== 封包處理器（薄層：解封包 → 委派 PartySystem） ====================

// HandleInviteParty 處理 C_WHO_PARTY (opcode 230) = Java C_CreateParty。
// 格式：[C type] + type 0/1: [D targetID], type 2: [S name], type 3: [D targetID]
func HandleInviteParty(sess *net.Session, r *packet.Reader, deps *Deps) {
	partyType := r.ReadC()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if deps.Party == nil {
		return
	}

	switch partyType {
	case 0, 1:
		targetID := r.ReadD()
		deps.Party.Invite(sess, player, targetID, partyType)
	case 2:
		targetName := r.ReadS()
		deps.Party.ChatInvite(sess, player, targetName)
	case 3:
		targetID := r.ReadD()
		deps.Party.TransferLeader(sess, player, targetID)
	}
}

// HandleWhoParty 處理 C_INVITE_PARTY_TARGET (opcode 43) = Java C_Party。
// 格式：（無額外資料）— 查詢自己的隊伍資訊。
func HandleWhoParty(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if deps.Party != nil {
		deps.Party.ShowPartyInfo(sess, player)
	}
}

// HandleLeaveParty 處理 C_LEAVE_PARTY (opcode 33)。
func HandleLeaveParty(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || !deps.World.Parties.IsInParty(player.CharID) {
		return
	}

	if deps.Party != nil {
		deps.Party.Leave(player)
	}
}

// HandleBanishParty 處理 C_BANISH_PARTY (opcode 255) — 踢除隊員。
// Java C_BanParty 格式：[S memberName]
func HandleBanishParty(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetName := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil || !deps.World.Parties.IsInParty(player.CharID) {
		return
	}

	if deps.Party != nil {
		deps.Party.BanishMember(sess, player, targetName)
	}
}

// HandlePartyControl 處理 C_CHAT_PARTY_CONTROL (opcode 199) = Java C_ChatParty。
// 格式：[C action] + action 0: [S name], action 1: (none), action 2: (none)
func HandlePartyControl(sess *net.Session, r *packet.Reader, deps *Deps) {
	action := r.ReadC()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if deps.Party == nil {
		return
	}

	switch action {
	case 0: // /chatbanish — 踢除聊天隊伍成員
		name := r.ReadS()
		deps.Party.ChatKick(sess, player, name)
	case 1: // /chatoutparty — 離開聊天隊伍
		if deps.World.ChatParties.IsInParty(player.CharID) {
			deps.Party.ChatLeave(player)
		}
	case 2: // /chatparty — 查看聊天隊伍成員
		deps.Party.ShowChatPartyInfo(sess, player)
	}
}

// ==================== YES/NO 回應包裝器（由 attr.go 呼叫） ====================

// HandlePartyInviteResponse 處理 C_Attr case 953/954（一般隊伍邀請回應）。
func HandlePartyInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool, deps *Deps) {
	if deps.Party != nil {
		deps.Party.InviteResponse(player, inviterID, accepted)
	}
}

// HandleChatPartyInviteResponse 處理 C_Attr case 951（聊天隊伍邀請回應）。
func HandleChatPartyInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool, deps *Deps) {
	if deps.Party != nil {
		deps.Party.ChatInviteResponse(player, inviterID, accepted)
	}
}

// ==================== 包裝器（供其他 handler / system 呼叫） ====================

// partyLeaveMember 離開隊伍包裝器（由 changechar.go 呼叫）。
func partyLeaveMember(player *world.PlayerInfo, deps *Deps) {
	if deps.Party != nil {
		deps.Party.Leave(player)
	}
}

// UpdatePartyMiniHP 廣播 HP 變化到隊伍成員（由 npcaction.go、combat 等呼叫）。
func UpdatePartyMiniHP(player *world.PlayerInfo, deps *Deps) {
	if deps.Party != nil {
		deps.Party.UpdateMiniHP(player)
	}
}

// RefreshPartyPositions 發送位置更新到該玩家的隊伍（由 system/party_refresh.go 呼叫）。
func RefreshPartyPositions(player *world.PlayerInfo, deps *Deps) {
	if deps.Party != nil {
		deps.Party.RefreshPositions(player)
	}
}
