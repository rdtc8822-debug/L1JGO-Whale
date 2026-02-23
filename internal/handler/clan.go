package handler

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
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

	// DB transaction: create clan + add leader as member
	// Gold deduction is memory-only; batch save persists it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	foundDate := int32(time.Now().Unix())
	clanID, err := deps.ClanRepo.CreateClan(ctx, player.CharID, player.Name, clanName, foundDate)
	if err != nil {
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
	sendPledgeEmblemStatus(sess, 0) // emblem off (new clan, no emblem)
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
	// Find applicant
	applicant := deps.World.GetByCharID(applicantCharID)
	if applicant == nil {
		return
	}

	if !accepted {
		// Java: S_ServerMessage(96, pc.getName()) — "拒絕你的請求"
		sendServerMessageArgs(applicant.Session, 96, responder.Name)
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
	applicant.Title = "" // Clear title on join (Java: joinPc.setTitle(""))

	// Notify ALL online clan members (Java: for each clanMembers: S_ServerMessage(94))
	for _, m := range clan.Members {
		online := deps.World.GetByCharID(m.CharID)
		if online != nil {
			sendServerMessageArgs(online.Session, 94, applicant.Name) // "你接受%0當你的血盟成員"
		}
	}

	// Clear title on applicant + broadcast (Java: S_CharTitle)
	sendCharTitle(applicant.Session, applicant.CharID, "")
	nearbyApp := deps.World.GetNearbyPlayers(applicant.X, applicant.Y, applicant.MapID, applicant.SessionID)
	for _, other := range nearbyApp {
		sendCharTitle(other.Session, applicant.CharID, "")
	}

	// Notify applicant — packet sequence matches Java C_Attr case 97
	sendRankChanged(applicant.Session, byte(rank), applicant.Name) // S_PacketBox(27)
	sendServerMessageArgs(applicant.Session, 95, clan.ClanName)    // "加入%0血盟"
	sendClanName(applicant.Session, applicant.CharID, clan.ClanName, clan.ClanID, true)
	sendCharResetEmblem(applicant.Session, applicant.CharID, clan.ClanID) // S_CharReset(objId, clanId)
	sendPledgeEmblemStatus(applicant.Session, int(clan.EmblemStatus))
	sendClanAttention(applicant.Session)

	// Broadcast S_CharReset (emblem update) to ALL online clan members + their nearby players
	// Java: for each online member → send S_CharReset(joinee.id, emblemId) to member,
	//       member.broadcastPacket(S_CharReset(member.id, emblemId)) to nearby
	for _, m := range clan.Members {
		online := deps.World.GetByCharID(m.CharID)
		if online != nil {
			sendCharResetEmblem(online.Session, applicant.CharID, clan.EmblemID)
			nearby := deps.World.GetNearbyPlayers(online.X, online.Y, online.MapID, online.SessionID)
			for _, other := range nearby {
				sendCharResetEmblem(other.Session, online.CharID, clan.EmblemID)
			}
		}
	}

	// Broadcast clan name update to nearby players of the applicant
	for _, other := range nearbyApp {
		sendClanName(other.Session, applicant.CharID, clan.ClanName, clan.ClanID, true)
	}

	deps.Log.Info(fmt.Sprintf("血盟加入  player=%s  clan=%s", applicant.Name, clan.ClanName))
}

// HandleLeaveClan processes C_LEAVE_PLEDGE (opcode 61) — leave or dissolve clan.
// Java: C_LeaveClan.java
// Packet: [S clanName]
func HandleLeaveClan(sess *net.Session, r *packet.Reader, deps *Deps) {
	clanNamePkt := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		deps.Log.Warn(fmt.Sprintf("退盟: player not found  sessID=%d", sess.ID))
		return
	}

	deps.Log.Info(fmt.Sprintf("退盟請求  player=%s  clanID=%d  pktClanName=%s", player.Name, player.ClanID, clanNamePkt))

	if player.ClanID == 0 {
		deps.Log.Warn(fmt.Sprintf("退盟: player無血盟  player=%s", player.Name))
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		deps.Log.Warn(fmt.Sprintf("退盟: 找不到clan  player=%s  clanID=%d", player.Name, player.ClanID))
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
// Java: C_RestartMenu.java (data=1 branch) — rank change authority matrix.
// Packet: [C data][C rank][S name]
func HandleRankControl(sess *net.Session, r *packet.Reader, deps *Deps) {
	data := r.ReadC()
	if data != 1 {
		return // only rank control sub-type (data=1) is handled
	}

	rank := int16(r.ReadC())
	targetName := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Must be in a clan
	if player.ClanID == 0 {
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	// Cannot change own rank
	if targetName == player.Name {
		sendServerMessage(sess, 2068) // "無法變更自己的階級"
		return
	}

	// Validate rank range (2-10)
	if rank < 2 || rank > 10 {
		sendServerMessage(sess, 781) // invalid rank
		return
	}

	// Alliance ranks (2-6) not implemented
	if rank >= 2 && rank <= 6 {
		return
	}

	// Authority matrix check
	myRank := player.ClanRank
	if !canGrantRank(myRank, rank) {
		sendServerMessage(sess, 2065) // "不具此權限"
		return
	}

	// Find target player (must be online)
	target := deps.World.GetByName(targetName)
	if target == nil {
		sendServerMessage(sess, 2069) // "對方不在線上"
		return
	}

	// Target must be in same clan
	if target.ClanID != player.ClanID {
		sendServerMessage(sess, 414) // "並非血盟成員"
		return
	}

	// Cannot change rank of rank 9/10 members unless operator is rank 10
	if (target.ClanRank == world.ClanRankGuardian || target.ClanRank == world.ClanRankPrince) && myRank != world.ClanRankPrince {
		sendServerMessage(sess, 2065)
		return
	}

	// Guardian (rank 9) level requirements
	if rank == world.ClanRankGuardian {
		// Operator must be level >= 40 (unless rank 10)
		if myRank != world.ClanRankPrince && player.Level < 40 {
			sendServerMessage(sess, 2472) // level too low to grant guardian
			return
		}
		// Target must be level >= 40
		if target.Level < 40 {
			sendServerMessage(sess, 2473) // target level too low for guardian
			return
		}
	}

	// DB update
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := deps.ClanRepo.UpdateMemberRank(ctx, clan.ClanID, target.CharID, rank)
	if err != nil {
		deps.Log.Error(fmt.Sprintf("階級變更失敗  target=%s  err=%v", targetName, err))
		return
	}

	// Memory update
	target.ClanRank = rank
	member := clan.Members[target.CharID]
	if member != nil {
		member.Rank = rank
	}

	// Send S_PacketBox(27) to both operator and target
	sendRankChanged(sess, byte(rank), targetName)
	sendRankChanged(target.Session, byte(rank), targetName)

	deps.Log.Info(fmt.Sprintf("階級變更  target=%s  rank=%d  by=%s", targetName, rank, player.Name))
}

// canGrantRank checks if an operator with myRank can grant targetRank.
// Authority matrix (Java C_RestartMenu.java):
//   rank 10 (Prince): can grant 7, 8, 9
//   rank 9  (Guardian): can grant 7, 8
func canGrantRank(myRank, targetRank int16) bool {
	switch myRank {
	case world.ClanRankPrince: // 10
		return targetRank == 7 || targetRank == 8 || targetRank == 9
	case world.ClanRankGuardian: // 9
		return targetRank == 7 || targetRank == 8
	default:
		return false
	}
}

// HandleTitle processes C_TITLE (opcode 90) — set character title.
// Java: C_Title.java
// Packet: [S charName][S title]
func HandleTitle(sess *net.Session, r *packet.Reader, deps *Deps) {
	charName := r.ReadS()
	title := r.ReadS()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Truncate title to reasonable length (Java: 16 chars)
	if len(title) > 48 { // ~16 CJK chars × 3 bytes UTF-8
		title = title[:48]
	}

	settingSelf := charName == player.Name

	if settingSelf {
		// --- Setting own title ---
		if player.ClanID != 0 {
			clan := deps.World.Clans.GetClan(player.ClanID)
			if clan != nil && clan.LeaderID == player.CharID {
				// Clan leader setting own title: level >= 10
				if player.Level < 10 {
					sendServerMessage(sess, 197) // "等級10以上才可設定稱號"
					return
				}
			} else {
				// Non-leader clan member setting own title
				if !deps.Config.Character.ChangeTitleByOneself {
					sendServerMessage(sess, 198) // "除了血盟君主之外，不可變更稱號"
					return
				}
				if player.Level < 10 {
					sendServerMessage(sess, 197)
					return
				}
			}
		} else {
			// No clan: level >= 40
			if player.Level < 40 {
				sendServerMessage(sess, 200) // "等級40以上才可設定稱號"
				return
			}
		}

		// Apply title
		player.Title = title
		sendCharTitle(sess, player.CharID, title)

		// Broadcast to nearby
		nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		for _, other := range nearby {
			sendCharTitle(other.Session, player.CharID, title)
		}

		// DB: title is saved by periodic saveAllPlayers via SaveCharacter
	} else {
		// --- Setting another player's title ---
		// Must be clan leader
		if player.ClanID == 0 {
			return
		}
		clan := deps.World.Clans.GetClan(player.ClanID)
		if clan == nil || clan.LeaderID != player.CharID {
			return // only leader can set other's title
		}

		// Leader must be level >= 10
		if player.Level < 10 {
			sendServerMessage(sess, 197)
			return
		}

		// Target must be online
		target := deps.World.GetByName(charName)
		if target == nil {
			return
		}

		// Target must be in same clan
		if target.ClanID != player.ClanID {
			sendServerMessage(sess, 199) // "並非你血盟成員"
			return
		}

		// Target level >= 10
		if target.Level < 10 {
			sendServerMessage(sess, 202) // "對方等級10以上才可設定稱號"
			return
		}

		// Apply title
		target.Title = title
		sendCharTitle(target.Session, target.CharID, title)

		// Broadcast to nearby of target
		nearby := deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
		for _, other := range nearby {
			sendCharTitle(other.Session, target.CharID, title)
		}

		// Notify all online clan members
		for charID := range clan.Members {
			member := deps.World.GetByCharID(charID)
			if member != nil {
				sendServerMessageArgs(member.Session, 203, charName, title) // "盟主賦予%0稱號%1"
			}
		}
	}
}

// HandleEmblemUpload processes C_UPLOAD_EMBLEM (opcode 18) — upload clan emblem.
// Java: C_EmblemUpload.java
// Packet: [384 bytes raw emblem data]
func HandleEmblemUpload(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if player.ClanID == 0 {
		return
	}

	// Only clan leader (rank 10) can upload emblem
	if player.ClanRank != world.ClanRankPrince {
		return
	}

	clan := deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	// Read 384 bytes of emblem data
	emblemData := r.ReadBytes(384)
	if len(emblemData) < 384 {
		return
	}

	// Generate new emblem ID
	newEmblemID := world.NextEmblemID()

	// Write emblem file to disk
	emblemPath := fmt.Sprintf("emblem/%d", newEmblemID)
	if err := os.WriteFile(emblemPath, emblemData, 0644); err != nil {
		deps.Log.Error(fmt.Sprintf("盟徽寫入失敗  clanID=%d  err=%v", clan.ClanID, err))
		return
	}

	// DB update
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := deps.ClanRepo.UpdateEmblemID(ctx, clan.ClanID, newEmblemID); err != nil {
		deps.Log.Error(fmt.Sprintf("盟徽DB更新失敗  clanID=%d  err=%v", clan.ClanID, err))
		return
	}

	// Memory update
	clan.EmblemID = newEmblemID
	clan.EmblemStatus = 1

	// Broadcast S_CharReset (emblem sub-type 0x3c) to all online clan members
	for charID := range clan.Members {
		member := deps.World.GetByCharID(charID)
		if member != nil {
			sendCharResetEmblem(member.Session, member.CharID, newEmblemID)
			sendPledgeEmblemStatus(member.Session, 1)
		}
	}

	deps.Log.Info(fmt.Sprintf("盟徽上傳  clan=%s  emblemID=%d", clan.ClanName, newEmblemID))
}

// HandleEmblemDownload processes C_ALT_ATTACK / C_EMBLEM_DOWNLOAD (opcode 72) — download clan emblem.
// Java: C_EmblemDownload.java
// Packet: [D emblemId]
func HandleEmblemDownload(sess *net.Session, r *packet.Reader, deps *Deps) {
	emblemID := r.ReadD()
	if emblemID <= 0 {
		return
	}

	// Read emblem file from disk
	emblemPath := fmt.Sprintf("emblem/%d", emblemID)
	emblemData, err := os.ReadFile(emblemPath)
	if err != nil {
		// File not found or read error — silently ignore
		return
	}

	// Send S_Emblem
	sendEmblem(sess, emblemID, emblemData)
}

// --- Packet builders ---

// sendClanName sends S_OPCODE_CLANNAME (72) — update clan name display.
// sendPledgeEmblemStatus sends S_PacketBox(173) — notify client of clan emblem status.
// Java: S_PacketBox case PLEDGE_EMBLEM_STATUS (173)
// value: 0=off, 1=on
func sendPledgeEmblemStatus(sess *net.Session, emblemStatus int) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(173) // PLEDGE_EMBLEM_STATUS subtype
	w.WriteC(1)
	if emblemStatus == 0 {
		w.WriteC(0)
	} else {
		w.WriteC(1)
	}
	w.WriteD(0)
	sess.Send(w.Bytes())
}

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
	if onlineOnly {
		// Subtype 171: lightweight online member list — names only.
		// Java: S_PacketBox case 171 (HTML_PLEDGE_ONLINE_MEMBERS)
		// Format: [C 171][H count][S name]...
		var names []string
		for _, m := range clan.Members {
			if deps.World.GetByCharID(m.CharID) != nil {
				names = append(names, m.CharName)
			}
		}

		w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
		w.WriteC(171)
		w.WriteH(uint16(len(names)))
		for _, name := range names {
			w.WriteS(name)
		}
		sess.Send(w.Bytes())
		return
	}

	// Subtype 170: full member list (all members, online + offline).
	// Java: S_Pledge.java
	// Format: [C 170][H 1][C count][per member: S name, C rank, C level, 62B notes, D id, C classType]
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
		md := memberData{
			name:     m.CharName,
			rank:     m.Rank,
			notes:    m.Notes,
			memberID: m.CharID,
		}

		online := deps.World.GetByCharID(m.CharID)
		if online != nil {
			md.level = online.Level
			md.classType = online.ClassType
		}

		members = append(members, md)
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(170)
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

// sendRankChanged sends S_PacketBox(27) — rank change notification.
// Java: S_PacketBox case 27 (MSG_RANK_CHANGED)
// Format: [C 27][C rank][S name]
func sendRankChanged(sess *net.Session, rank byte, name string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(27) // MSG_RANK_CHANGED
	w.WriteC(rank)
	w.WriteS(name)
	sess.Send(w.Bytes())
}

// sendCharResetEmblem sends S_OPCODE_VOICE_CHAT (64) sub-type 0x3c — emblem update.
// Java: S_CharReset.java (EMBLEM variant)
// Format: [C 0x3c][D pcObjId][D emblemId]
func sendCharResetEmblem(sess *net.Session, pcObjID int32, emblemID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_VOICE_CHAT)
	w.WriteC(0x3c) // sub-type: emblem update
	w.WriteD(pcObjID)
	w.WriteD(emblemID)
	sess.Send(w.Bytes())
}

// sendEmblem sends S_OPCODE_EMBLEM (118) — emblem data for client rendering.
// Java: S_Emblem.java
// Format: [D emblemId][N bytes emblem data]
func sendEmblem(sess *net.Session, emblemID int32, data []byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EMBLEM)
	w.WriteD(emblemID)
	w.WriteBytes(data)
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
