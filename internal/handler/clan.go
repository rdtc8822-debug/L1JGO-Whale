package handler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
)

const (
	clanCreateCost = 30000 // 30,000 adena to create a clan
)

// --- Handlers ---

// HandleCreateClan processes C_CREATE_PLEDGE (opcode 222) — create a new clan.
// Java: C_CreateClan.java
// Packet: [S clanName]
func HandleCreateClan(sess *net.Session, r *packet.Reader, deps *Deps) {
	clanName := r.ReadS()
	if clanName == "" {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Only Crown (Prince/Princess) can create clans
	if player.ClassType != 0 {
		sendServerMessage(sess, 85) // "王子和公主才可創立血盟"
		return
	}

	// Must not already be in a clan
	if player.ClanID != 0 {
		sendServerMessage(sess, 86) // "已經創立血盟"
		return
	}

	// Check name uniqueness (case-insensitive)
	if deps.World.Clans.ClanNameExists(clanName) {
		sendServerMessage(sess, 99) // "血盟名稱已存在"
		return
	}

	// Check gold in memory
	adena := player.Inv.FindByItemID(world.AdenaItemID)
	if adena == nil || adena.Count < clanCreateCost {
		sendServerMessage(sess, 189) // "金幣不足"
		return
	}

	// DB transaction: deduct gold + create clan + add leader as member
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	foundDate := int32(time.Now().Unix())
	clanID, err := deps.ClanRepo.CreateClan(ctx, player.CharID, player.Name, clanName, foundDate, clanCreateCost)
	if err != nil {
		if errors.Is(err, persist.ErrInsufficientGold) {
			sendServerMessage(sess, 189)
			return
		}
		deps.Log.Error(fmt.Sprintf("建立血盟失敗  player=%s  clan=%s  err=%v", player.Name, clanName, err))
		return
	}

	// DB succeeded — update memory
	adena.Count -= clanCreateCost
	if adena.Count <= 0 {
		player.Inv.RemoveItem(adena.ObjectID, 0)
		sendRemoveInventoryItem(sess, adena.ObjectID)
	} else {
		sendItemCountUpdate(sess, adena)
	}

	// Build clan info in memory
	clan := &world.ClanInfo{
		ClanID:     clanID,
		ClanName:   clanName,
		LeaderID:   player.CharID,
		LeaderName: player.Name,
		FoundDate:  foundDate,
		Members: map[int32]*world.ClanMember{
			player.CharID: {
				CharID:   player.CharID,
				CharName: player.Name,
				Rank:     world.ClanRankPrince,
			},
		},
	}
	deps.World.Clans.AddClan(clan)

	// Update player fields
	player.ClanID = clanID
	player.ClanName = clanName
	player.ClanRank = world.ClanRankPrince

	// Send packets
	sendServerMessageArgs(sess, 84, clanName) // "創立%0血盟"
	sendClanName(sess, player.CharID, clanName, clanID, true)
	sendClanAttention(sess)

	// Broadcast to nearby players
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendClanName(other.Session, player.CharID, clanName, clanID, true)
	}

	deps.Log.Info(fmt.Sprintf("血盟建立  player=%s  clan=%s  id=%d", player.Name, clanName, clanID))
}

// HandleJoinClan processes C_JOIN_PLEDGE (opcode 194) — request to join a clan.
// Java: C_JoinClan.java — face-to-face mechanic.
// Packet: no additional data. Player must be standing next to a Crown/Guardian.
func HandleJoinClan(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Player must not be in a clan
	if player.ClanID != 0 {
		sendServerMessage(sess, 89) // "已加入血盟"
		return
	}

	// Find the nearest Crown/Guardian within range 3 (face-to-face)
	var target *world.PlayerInfo
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	bestDist := int32(999)
	for _, other := range nearby {
		if other.ClanID == 0 {
			continue
		}
		// Must be Crown (classType=0) or clan leader/guardian rank
		clan := deps.World.Clans.GetClan(other.ClanID)
		if clan == nil {
			continue
		}
		member := clan.Members[other.CharID]
		if member == nil {
			continue
		}
		if member.Rank != world.ClanRankPrince && member.Rank != world.ClanRankGuardian {
			continue
		}

		dx := player.X - other.X
		dy := player.Y - other.Y
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		dist := dx
		if dy > dist {
			dist = dy
		}
		if dist <= 3 && dist < bestDist {
			bestDist = dist
			target = other
		}
	}

	if target == nil {
		sendServerMessage(sess, 90) // "對方沒有創設血盟"
		return
	}

	// Verify target is Crown or Guardian
	clan := deps.World.Clans.GetClan(target.ClanID)
	if clan == nil {
		sendServerMessage(sess, 90)
		return
	}

	// Send Y/N dialog to the target (clan leader/guardian)
	target.PendingYesNoType = 97
	target.PendingYesNoData = player.CharID

	sendYesNoDialog(target.Session, 97, player.Name) // "%0想加入你的血盟，是否同意？"
}

// HandleClanJoinResponse is called from HandleAttr when msgType=97 (clan join Y/N).
// The responder is the clan leader/guardian; data = applicant CharID.
func HandleClanJoinResponse(sess *net.Session, responder *world.PlayerInfo, applicantCharID int32, accepted bool, deps *Deps) {
	if !accepted {
		return
	}

	// Find applicant
	applicant := deps.World.GetByCharID(applicantCharID)
	if applicant == nil {
		return
	}

	// Applicant must still not be in a clan
	if applicant.ClanID != 0 {
		sendServerMessage(sess, 89) // already in clan
		return
	}

	// Responder must be in a clan
	if responder.ClanID == 0 {
		return
	}

	clan := deps.World.Clans.GetClan(responder.ClanID)
	if clan == nil {
		return
	}

	// DB: add member
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rank := world.ClanRankPublic // 7 = regular member
	err := deps.ClanRepo.AddMember(ctx, clan.ClanID, clan.ClanName, applicant.CharID, applicant.Name, rank)
	if err != nil {
		deps.Log.Error(fmt.Sprintf("血盟加入失敗  applicant=%s  clan=%s  err=%v", applicant.Name, clan.ClanName, err))
		return
	}

	// Memory update
	deps.World.Clans.AddMember(clan.ClanID, &world.ClanMember{
		CharID:   applicant.CharID,
		CharName: applicant.Name,
		Rank:     rank,
	})

	applicant.ClanID = clan.ClanID
	applicant.ClanName = clan.ClanName
	applicant.ClanRank = rank

	// Notify applicant
	sendServerMessageArgs(applicant.Session, 95, clan.ClanName) // "加入%0血盟"
	sendClanName(applicant.Session, applicant.CharID, clan.ClanName, clan.ClanID, true)
	sendClanAttention(applicant.Session)

	// Notify responder
	sendServerMessageArgs(sess, 94, applicant.Name) // "你接受%0當你的血盟成員"

	// Broadcast clan name update to nearby players of the applicant
	nearby := deps.World.GetNearbyPlayers(applicant.X, applicant.Y, applicant.MapID, applicant.SessionID)
	for _, other := range nearby {
		sendClanName(other.Session, applicant.CharID, clan.ClanName, clan.ClanID, true)
	}

	deps.Log.Info(fmt.Sprintf("血盟加入  player=%s  clan=%s", applicant.Name, clan.ClanName))
}

// HandleLeaveClan processes C_LEAVE_PLEDGE (opcode 61) — leave or dissolve clan.
// Java: C_LeaveClan.java
// Packet: [S clanName]
func HandleLeaveClan(sess *net.Session, r *packet.Reader, deps *Deps) {
	_ = r.ReadS() // clanName (not used for validation; we use player's actual clan)

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if player.ClanID == 0 {
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if player.ClassType == 0 && player.CharID == clan.LeaderID {
		// Leader dissolving the clan
		handleDissolveClan(sess, player, clan, ctx, deps)
	} else {
		// Member leaving
		handleMemberLeave(sess, player, clan, ctx, deps)
	}
}

// handleDissolveClan dissolves a clan (leader only).
func handleDissolveClan(sess *net.Session, player *world.PlayerInfo, clan *world.ClanInfo, ctx context.Context, deps *Deps) {
	// Cannot dissolve if clan owns castle or house
	if clan.HasCastle != 0 {
		sendServerMessage(sess, 665) // cannot dissolve with castle/house
		return
	}
	if clan.HasHouse != 0 {
		sendServerMessage(sess, 665)
		return
	}

	clanID := clan.ClanID
	clanName := clan.ClanName
	leaderName := player.Name

	// Collect member charIDs before DB operation
	memberIDs := make([]int32, 0, len(clan.Members))
	for charID := range clan.Members {
		memberIDs = append(memberIDs, charID)
	}

	// DB: dissolve clan
	err := deps.ClanRepo.DissolveClan(ctx, clanID)
	if err != nil {
		deps.Log.Error(fmt.Sprintf("血盟解散失敗  clan=%s  err=%v", clanName, err))
		return
	}

	// Memory update & notify all online members
	for _, charID := range memberIDs {
		member := deps.World.GetByCharID(charID)
		if member != nil {
			member.ClanID = 0
			member.ClanName = ""
			member.ClanRank = 0

			sendServerMessageArgs(member.Session, 269, leaderName) // "血盟盟主%0解散了血盟"
			sendClanName(member.Session, member.CharID, "", 0, false)
			sendClanAttention(member.Session)

			// Broadcast to nearby players
			nearby := deps.World.GetNearbyPlayers(member.X, member.Y, member.MapID, member.SessionID)
			for _, other := range nearby {
				sendClanName(other.Session, member.CharID, "", 0, false)
			}
		}
	}

	deps.World.Clans.RemoveClan(clanID)

	deps.Log.Info(fmt.Sprintf("血盟解散  clan=%s  leader=%s", clanName, leaderName))
}

// handleMemberLeave handles a non-leader member leaving a clan.
func handleMemberLeave(sess *net.Session, player *world.PlayerInfo, clan *world.ClanInfo, ctx context.Context, deps *Deps) {
	clanID := clan.ClanID
	clanName := clan.ClanName

	// DB: remove member
	err := deps.ClanRepo.RemoveMember(ctx, clanID, player.CharID)
	if err != nil {
		deps.Log.Error(fmt.Sprintf("血盟脫退失敗  player=%s  clan=%s  err=%v", player.Name, clanName, err))
		return
	}

	// Memory update
	deps.World.Clans.RemoveMember(clanID, player.CharID)

	playerName := player.Name
	player.ClanID = 0
	player.ClanName = ""
	player.ClanRank = 0

	// Notify leaving player
	sendClanName(sess, player.CharID, "", 0, false)
	sendClanAttention(sess)

	// Notify online clan members
	for charID := range clan.Members {
		member := deps.World.GetByCharID(charID)
		if member != nil {
			sendServerMessageArgs(member.Session, 178, playerName, clanName) // "%0脫退了%1血盟"
		}
	}

	// Broadcast to nearby players
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendClanName(other.Session, player.CharID, "", 0, false)
	}

	deps.Log.Info(fmt.Sprintf("血盟脫退  player=%s  clan=%s", playerName, clanName))
}

// HandleBanMember processes C_BAN_MEMBER (opcode 69) — kick a member from clan.
// Java: C_BanClan.java
// Packet: [S targetName]
func HandleBanMember(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetName := r.ReadS()
	if targetName == "" {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Must be Crown and clan leader
	if player.ClassType != 0 || player.ClanID == 0 {
		sendServerMessage(sess, 518) // "血盟君主才可使用此命令"
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	if clan.LeaderID != player.CharID {
		sendServerMessage(sess, 518)
		return
	}

	// Cannot kick self
	if targetName == player.Name {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try online target first
	target := deps.World.GetByName(targetName)
	if target != nil {
		// Verify same clan
		if target.ClanID != player.ClanID {
			sendServerMessage(sess, 109) // "沒有叫%0的人"
			return
		}

		// DB: remove member
		err := deps.ClanRepo.RemoveMember(ctx, clan.ClanID, target.CharID)
		if err != nil {
			deps.Log.Error(fmt.Sprintf("血盟驅逐失敗  target=%s  err=%v", targetName, err))
			return
		}

		// Memory update
		deps.World.Clans.RemoveMember(clan.ClanID, target.CharID)

		target.ClanID = 0
		target.ClanName = ""
		target.ClanRank = 0

		// Notify target
		sendServerMessageArgs(target.Session, 238, clan.ClanName) // "你被%0血盟驅逐了"
		sendClanName(target.Session, target.CharID, "", 0, false)
		sendClanAttention(target.Session)

		// Broadcast to nearby of target
		nearby := deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
		for _, other := range nearby {
			sendClanName(other.Session, target.CharID, "", 0, false)
		}
	} else {
		// Offline target — lookup from DB
		charID, clanID, _, _, err := deps.ClanRepo.LoadOfflineCharClan(ctx, targetName)
		if err != nil {
			sendServerMessage(sess, 109) // not found
			return
		}
		if clanID != player.ClanID {
			sendServerMessage(sess, 109)
			return
		}

		// DB: remove member
		err = deps.ClanRepo.RemoveMember(ctx, clan.ClanID, charID)
		if err != nil {
			deps.Log.Error(fmt.Sprintf("血盟驅逐失敗(離線)  target=%s  err=%v", targetName, err))
			return
		}

		// Memory update
		deps.World.Clans.RemoveMember(clan.ClanID, charID)
	}

	// Notify executor
	sendServerMessageArgs(sess, 240, targetName) // "%0被你從血盟驅逐了"

	deps.Log.Info(fmt.Sprintf("血盟驅逐  target=%s  clan=%s  by=%s", targetName, clan.ClanName, player.Name))
}

// HandleWhoPledge processes C_WHO_PLEDGE (opcode 68) — view clan info.
// Java: C_Pledge.java
// Packet: no additional data
func HandleWhoPledge(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if player.ClanID == 0 {
		sendServerMessage(sess, 1064) // "不屬於血盟"
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		sendServerMessage(sess, 1064)
		return
	}

	// Send clan announcement (S_PacketBox subtype 167)
	sendPledgeAnnounce(sess, clan)

	// Send full member list (S_PacketBox subtype 170)
	sendPledgeMembers(sess, clan, deps, false)

	// Send online member list (S_PacketBox subtype 171)
	sendPledgeMembers(sess, clan, deps, true)
}

// HandlePledgeWatch processes C_PLEDGE_WATCH (opcode 78) — clan settings.
// Java: C_PledgeContent.java
// Packet: [C dataType][S content]
func HandlePledgeWatch(sess *net.Session, r *packet.Reader, deps *Deps) {
	dataType := r.ReadC()
	content := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if player.ClanID == 0 {
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch dataType {
	case 15: // Set clan announcement (leader only)
		if clan.LeaderID != player.CharID {
			sendServerMessage(sess, 518)
			return
		}

		// Encode to Big5 and truncate to 478 bytes
		announcement := truncateBig5(content, 478)

		err := deps.ClanRepo.UpdateAnnouncement(ctx, clan.ClanID, announcement)
		if err != nil {
			deps.Log.Error(fmt.Sprintf("更新血盟公告失敗  err=%v", err))
			return
		}

		clan.Announcement = announcement

	case 16: // Set personal notes
		notes := truncateBig5(content, 62)

		err := deps.ClanRepo.UpdateMemberNotes(ctx, clan.ClanID, player.CharID, notes)
		if err != nil {
			deps.Log.Error(fmt.Sprintf("更新成員備註失敗  err=%v", err))
			return
		}

		member := clan.Members[player.CharID]
		if member != nil {
			member.Notes = notes
		}
	}
}

// HandleRankControl processes C_RANK_CONTROL (opcode 63) — change member rank.
// Java: C_RankControl.java — not fully implemented yet, stub for now.
func HandleRankControl(sess *net.Session, r *packet.Reader, deps *Deps) {
	// TODO: implement rank control when needed
	// Packet: varies by sub-type
	deps.Log.Debug("C_RankControl received (not yet implemented)")
}

// --- Packet builders ---

// sendClanName sends S_OPCODE_CLANNAME (72) — update clan name display.
// Java: S_ClanName.java
// join=true → flag 0x0a (active), join=false → flag 0x0b (leaving)
func sendClanName(sess *net.Session, objID int32, clanName string, clanID int32, join bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CLANNAME)
	w.WriteD(objID)
	w.WriteS(clanName)
	w.WriteD(0) // unknown
	w.WriteC(0) // unknown
	if join {
		w.WriteC(0x0a) // clan active
		w.WriteD(0)     // 0 when joining
	} else {
		w.WriteC(0x0b) // clan inactive
		w.WriteD(clanID)
	}
	sess.Send(w.Bytes())
}

// sendCharTitle sends S_OPCODE_CHARTITLE (183) — update player title.
// Java: S_CharTitle.java
func sendCharTitle(sess *net.Session, objID int32, title string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARTITLE)
	w.WriteD(objID)
	w.WriteS(title)
	sess.Send(w.Bytes())
}

// sendClanAttention sends S_OPCODE_CLANATTENTION (200) — clan status notification.
// Java: S_ClanAttention.java — always sends fixed value 2.
func sendClanAttention(sess *net.Session) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CLANATTENTION)
	w.WriteD(2)
	sess.Send(w.Bytes())
}

// sendPledgeAnnounce sends S_PacketBox subtype 167 — clan announcement window.
// Java: S_Pledge.java (constructor with clan ID)
func sendPledgeAnnounce(sess *net.Session, clan *world.ClanInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(167) // HTML_PLEDGE_ANNOUNCE
	w.WriteS(clan.ClanName)
	w.WriteS(clan.LeaderName)
	w.WriteD(clan.EmblemID)
	w.WriteD(clan.FoundDate)

	// Announcement: fixed 478 bytes, zero-padded
	ann := make([]byte, 478)
	copy(ann, clan.Announcement)
	w.WriteBytes(ann)

	sess.Send(w.Bytes())
}

// sendPledgeMembers sends S_PacketBox subtype 170 or 171 — member list.
// Java: S_Pledge.java (constructor with L1PcInstance)
// onlineOnly=false → subtype 170 (all members), onlineOnly=true → subtype 171 (online only)
func sendPledgeMembers(sess *net.Session, clan *world.ClanInfo, deps *Deps, onlineOnly bool) {
	subType := byte(170) // HTML_PLEDGE_MEMBERS
	if onlineOnly {
		subType = 171 // HTML_PLEDGE_ONLINE_MEMBERS
	}

	// Collect members to send
	type memberData struct {
		name      string
		rank      int16
		level     int16
		notes     []byte
		memberID  int32
		classType int16
	}

	var members []memberData
	for _, m := range clan.Members {
		online := deps.World.GetByCharID(m.CharID)
		if onlineOnly && online == nil {
			continue
		}

		md := memberData{
			name:     m.CharName,
			rank:     m.Rank,
			notes:    m.Notes,
			memberID: m.CharID,
		}

		if online != nil {
			md.level = online.Level
			md.classType = online.ClassType
		}

		members = append(members, md)
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(subType)
	w.WriteH(1) // fixed value
	w.WriteC(byte(len(members)))

	for _, m := range members {
		w.WriteS(m.name)
		w.WriteC(byte(m.rank))
		w.WriteC(byte(m.level))

		// Notes: fixed 62 bytes, zero-padded
		notes := make([]byte, 62)
		copy(notes, m.notes)
		w.WriteBytes(notes)

		w.WriteD(m.memberID)
		w.WriteC(byte(m.classType))
	}

	sess.Send(w.Bytes())
}

// --- Utility ---

// truncateBig5 converts a string to Big5 encoding and truncates to maxLen bytes.
// Since the client uses Big5 (MS950), we encode at the Go side.
// For simplicity, we store the raw UTF-8 bytes truncated to maxLen.
// The client-side encoding handles the actual Big5 conversion.
func truncateBig5(s string, maxLen int) []byte {
	b := []byte(s)
	if len(b) > maxLen {
		b = b[:maxLen]
	}
	return b
}
