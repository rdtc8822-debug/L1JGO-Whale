package system

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

const (
	clanCreateCost = 30000 // 建立血盟需要 30,000 金幣
)

// ClanSystem 負責所有血盟邏輯（建立、加入、離開、踢除、階級、稱號、盟徽）。
// 實作 handler.ClanManager 介面。
type ClanSystem struct {
	deps *handler.Deps
}

// NewClanSystem 建立血盟系統。
func NewClanSystem(deps *handler.Deps) *ClanSystem {
	return &ClanSystem{deps: deps}
}

// ==================== 建立血盟 ====================

// Create 建立新血盟。
func (s *ClanSystem) Create(sess *net.Session, player *world.PlayerInfo, clanName string) {
	if clanName == "" {
		return
	}

	// 只有王族（Prince/Princess）可建立血盟
	if player.ClassType != 0 {
		handler.SendServerMessage(sess, 85) // "王子和公主才可創立血盟"
		return
	}

	// 不可已在血盟中
	if player.ClanID != 0 {
		handler.SendServerMessage(sess, 86) // "已經創立血盟"
		return
	}

	// 檢查名稱唯一性
	if s.deps.World.Clans.ClanNameExists(clanName) {
		handler.SendServerMessage(sess, 99) // "血盟名稱已存在"
		return
	}

	// 檢查金幣
	adena := player.Inv.FindByItemID(world.AdenaItemID)
	if adena == nil || adena.Count < clanCreateCost {
		handler.SendServerMessage(sess, 189) // "金幣不足"
		return
	}

	// DB 交易：建立血盟 + 加入領袖為成員
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	foundDate := int32(time.Now().Unix())
	clanID, err := s.deps.ClanRepo.CreateClan(ctx, player.CharID, player.Name, clanName, foundDate)
	if err != nil {
		s.deps.Log.Error(fmt.Sprintf("建立血盟失敗  player=%s  clan=%s  err=%v", player.Name, clanName, err))
		return
	}

	// DB 成功 — 更新記憶體
	adena.Count -= clanCreateCost
	if adena.Count <= 0 {
		player.Inv.RemoveItem(adena.ObjectID, 0)
		handler.SendRemoveInventoryItem(sess, adena.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, adena)
	}

	// 在記憶體建立血盟資料
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
	s.deps.World.Clans.AddClan(clan)

	// 更新玩家欄位
	player.ClanID = clanID
	player.ClanName = clanName
	player.ClanRank = world.ClanRankPrince

	// 發送封包
	handler.SendServerMessageArgs(sess, 84, clanName) // "創立%0血盟"
	handler.SendClanName(sess, player.CharID, clanName, clanID, true)
	handler.SendPledgeEmblemStatus(sess, 0)
	handler.SendClanAttention(sess)

	// 廣播到附近玩家
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		handler.SendClanName(other.Session, player.CharID, clanName, clanID, true)
	}

	s.deps.Log.Info(fmt.Sprintf("血盟建立  player=%s  clan=%s  id=%d", player.Name, clanName, clanID))
}

// ==================== 加入血盟 ====================

// JoinRequest 發送加入血盟請求（面對面機制）。
func (s *ClanSystem) JoinRequest(sess *net.Session, player *world.PlayerInfo) {
	// 不可已在血盟中
	if player.ClanID != 0 {
		handler.SendServerMessage(sess, 89) // "已加入血盟"
		return
	}

	// 尋找 3 格內最近的 Crown/Guardian
	var target *world.PlayerInfo
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	bestDist := int32(999)
	for _, other := range nearby {
		if other.ClanID == 0 {
			continue
		}
		clan := s.deps.World.Clans.GetClan(other.ClanID)
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
		handler.SendServerMessage(sess, 90) // "對方沒有創設血盟"
		return
	}

	clan := s.deps.World.Clans.GetClan(target.ClanID)
	if clan == nil {
		handler.SendServerMessage(sess, 90)
		return
	}

	// 發送 Y/N 對話框到目標（盟主/守護騎士）
	target.PendingYesNoType = 97
	target.PendingYesNoData = player.CharID

	handler.SendYesNoDialog(target.Session, 97, player.Name) // "%0想加入你的血盟，是否同意？"
}

// JoinResponse 處理加入血盟的 Yes/No 回應。
func (s *ClanSystem) JoinResponse(sess *net.Session, responder *world.PlayerInfo, applicantCharID int32, accepted bool) {
	applicant := s.deps.World.GetByCharID(applicantCharID)
	if applicant == nil {
		return
	}

	if !accepted {
		handler.SendServerMessageArgs(applicant.Session, 96, responder.Name) // "拒絕你的請求"
		return
	}

	// 申請者必須仍未加入血盟
	if applicant.ClanID != 0 {
		handler.SendServerMessage(sess, 89)
		return
	}

	if responder.ClanID == 0 {
		return
	}

	clan := s.deps.World.Clans.GetClan(responder.ClanID)
	if clan == nil {
		return
	}

	// DB：加入成員
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rank := world.ClanRankPublic // 7 = 一般成員
	err := s.deps.ClanRepo.AddMember(ctx, clan.ClanID, clan.ClanName, applicant.CharID, applicant.Name, rank)
	if err != nil {
		s.deps.Log.Error(fmt.Sprintf("血盟加入失敗  applicant=%s  clan=%s  err=%v", applicant.Name, clan.ClanName, err))
		return
	}

	// 記憶體更新
	s.deps.World.Clans.AddMember(clan.ClanID, &world.ClanMember{
		CharID:   applicant.CharID,
		CharName: applicant.Name,
		Rank:     rank,
	})

	applicant.ClanID = clan.ClanID
	applicant.ClanName = clan.ClanName
	applicant.ClanRank = rank
	applicant.Title = "" // Java: joinPc.setTitle("")
	applicant.Dirty = true

	// 通知所有在線血盟成員
	for _, m := range clan.Members {
		online := s.deps.World.GetByCharID(m.CharID)
		if online != nil {
			handler.SendServerMessageArgs(online.Session, 94, applicant.Name) // "你接受%0當你的血盟成員"
		}
	}

	// 清除申請者稱號 + 廣播
	sendCharTitle(applicant.Session, applicant.CharID, "")
	nearbyApp := s.deps.World.GetNearbyPlayers(applicant.X, applicant.Y, applicant.MapID, applicant.SessionID)
	for _, other := range nearbyApp {
		sendCharTitle(other.Session, applicant.CharID, "")
	}

	// 通知申請者 — 封包順序匹配 Java
	sendRankChanged(applicant.Session, byte(rank), applicant.Name)
	handler.SendServerMessageArgs(applicant.Session, 95, clan.ClanName) // "加入%0血盟"
	handler.SendClanName(applicant.Session, applicant.CharID, clan.ClanName, clan.ClanID, true)
	sendCharResetEmblem(applicant.Session, applicant.CharID, clan.ClanID)
	handler.SendPledgeEmblemStatus(applicant.Session, int(clan.EmblemStatus))
	handler.SendClanAttention(applicant.Session)

	// 廣播 S_CharReset 到所有在線成員及其附近玩家
	for _, m := range clan.Members {
		online := s.deps.World.GetByCharID(m.CharID)
		if online != nil {
			sendCharResetEmblem(online.Session, applicant.CharID, clan.EmblemID)
			nearby := s.deps.World.GetNearbyPlayers(online.X, online.Y, online.MapID, online.SessionID)
			for _, other := range nearby {
				sendCharResetEmblem(other.Session, online.CharID, clan.EmblemID)
			}
		}
	}

	// 廣播血盟名稱更新到申請者附近玩家
	for _, other := range nearbyApp {
		handler.SendClanName(other.Session, applicant.CharID, clan.ClanName, clan.ClanID, true)
	}

	s.deps.Log.Info(fmt.Sprintf("血盟加入  player=%s  clan=%s", applicant.Name, clan.ClanName))
}

// ==================== 離開/解散 ====================

// Leave 離開或解散血盟。
func (s *ClanSystem) Leave(sess *net.Session, player *world.PlayerInfo, clanNamePkt string) {
	if player.ClanID == 0 {
		return
	}

	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if player.ClassType == 0 && player.CharID == clan.LeaderID {
		// 盟主解散血盟
		s.dissolveClan(sess, player, clan, ctx)
	} else {
		// 成員退出
		s.memberLeave(sess, player, clan, ctx)
	}
}

// dissolveClan 解散血盟（盟主專用）。
func (s *ClanSystem) dissolveClan(sess *net.Session, player *world.PlayerInfo, clan *world.ClanInfo, ctx context.Context) {
	// 擁有城堡或房屋不可解散
	if clan.HasCastle != 0 {
		handler.SendServerMessage(sess, 665)
		return
	}
	if clan.HasHouse != 0 {
		handler.SendServerMessage(sess, 665)
		return
	}

	clanID := clan.ClanID
	clanName := clan.ClanName
	leaderName := player.Name

	// DB 操作前先收集成員 ID
	memberIDs := make([]int32, 0, len(clan.Members))
	for charID := range clan.Members {
		memberIDs = append(memberIDs, charID)
	}

	// DB：解散血盟
	err := s.deps.ClanRepo.DissolveClan(ctx, clanID)
	if err != nil {
		s.deps.Log.Error(fmt.Sprintf("血盟解散失敗  clan=%s  err=%v", clanName, err))
		return
	}

	// 記憶體更新 & 通知所有在線成員
	for _, charID := range memberIDs {
		member := s.deps.World.GetByCharID(charID)
		if member != nil {
			member.ClanID = 0
			member.ClanName = ""
			member.ClanRank = 0

			handler.SendServerMessageArgs(member.Session, 269, leaderName) // "血盟盟主%0解散了血盟"
			handler.SendClanName(member.Session, member.CharID, "", 0, false)
			handler.SendClanAttention(member.Session)

			// 廣播到附近玩家
			nearby := s.deps.World.GetNearbyPlayers(member.X, member.Y, member.MapID, member.SessionID)
			for _, other := range nearby {
				handler.SendClanName(other.Session, member.CharID, "", 0, false)
			}
		}
	}

	s.deps.World.Clans.RemoveClan(clanID)

	s.deps.Log.Info(fmt.Sprintf("血盟解散  clan=%s  leader=%s", clanName, leaderName))
}

// memberLeave 非盟主退出血盟。
func (s *ClanSystem) memberLeave(sess *net.Session, player *world.PlayerInfo, clan *world.ClanInfo, ctx context.Context) {
	clanID := clan.ClanID
	clanName := clan.ClanName

	// 退盟前釋放血盟倉庫鎖定
	if clan.WarehouseUsingCharID == player.CharID {
		clan.WarehouseUsingCharID = 0
	}

	// DB：移除成員
	err := s.deps.ClanRepo.RemoveMember(ctx, clanID, player.CharID)
	if err != nil {
		s.deps.Log.Error(fmt.Sprintf("血盟脫退失敗  player=%s  clan=%s  err=%v", player.Name, clanName, err))
		return
	}

	// 記憶體更新
	s.deps.World.Clans.RemoveMember(clanID, player.CharID)

	playerName := player.Name
	player.ClanID = 0
	player.ClanName = ""
	player.ClanRank = 0

	// 通知退出者
	handler.SendClanName(sess, player.CharID, "", 0, false)
	handler.SendClanAttention(sess)

	// 通知在線成員
	for charID := range clan.Members {
		member := s.deps.World.GetByCharID(charID)
		if member != nil {
			handler.SendServerMessageArgs(member.Session, 178, playerName, clanName) // "%0脫退了%1血盟"
		}
	}

	// 廣播到附近玩家
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		handler.SendClanName(other.Session, player.CharID, "", 0, false)
	}

	s.deps.Log.Info(fmt.Sprintf("血盟脫退  player=%s  clan=%s", playerName, clanName))
}

// ==================== 驅逐 ====================

// BanMember 驅逐血盟成員。
func (s *ClanSystem) BanMember(sess *net.Session, player *world.PlayerInfo, targetName string) {
	if targetName == "" {
		return
	}

	// 必須是王族且為盟主
	if player.ClassType != 0 || player.ClanID == 0 {
		handler.SendServerMessage(sess, 518) // "血盟君主才可使用此命令"
		return
	}

	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	if clan.LeaderID != player.CharID {
		handler.SendServerMessage(sess, 518)
		return
	}

	// 不能踢自己
	if targetName == player.Name {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 先嘗試在線目標
	target := s.deps.World.GetByName(targetName)
	if target != nil {
		// 驗證同血盟
		if target.ClanID != player.ClanID {
			handler.SendServerMessage(sess, 109) // "沒有叫%0的人"
			return
		}

		// DB：移除成員
		err := s.deps.ClanRepo.RemoveMember(ctx, clan.ClanID, target.CharID)
		if err != nil {
			s.deps.Log.Error(fmt.Sprintf("血盟驅逐失敗  target=%s  err=%v", targetName, err))
			return
		}

		// 記憶體更新
		s.deps.World.Clans.RemoveMember(clan.ClanID, target.CharID)

		target.ClanID = 0
		target.ClanName = ""
		target.ClanRank = 0

		// 通知目標
		handler.SendServerMessageArgs(target.Session, 238, clan.ClanName) // "你被%0血盟驅逐了"
		handler.SendClanName(target.Session, target.CharID, "", 0, false)
		handler.SendClanAttention(target.Session)

		// 廣播到目標附近
		nearby := s.deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
		for _, other := range nearby {
			handler.SendClanName(other.Session, target.CharID, "", 0, false)
		}
	} else {
		// 離線目標 — 從 DB 查詢
		charID, clanID, _, _, err := s.deps.ClanRepo.LoadOfflineCharClan(ctx, targetName)
		if err != nil {
			handler.SendServerMessage(sess, 109) // 找不到
			return
		}
		if clanID != player.ClanID {
			handler.SendServerMessage(sess, 109)
			return
		}

		// DB：移除成員
		err = s.deps.ClanRepo.RemoveMember(ctx, clan.ClanID, charID)
		if err != nil {
			s.deps.Log.Error(fmt.Sprintf("血盟驅逐失敗(離線)  target=%s  err=%v", targetName, err))
			return
		}

		// 記憶體更新
		s.deps.World.Clans.RemoveMember(clan.ClanID, charID)
	}

	// 通知執行者
	handler.SendServerMessageArgs(sess, 240, targetName) // "%0被你從血盟驅逐了"

	s.deps.Log.Info(fmt.Sprintf("血盟驅逐  target=%s  clan=%s  by=%s", targetName, clan.ClanName, player.Name))
}

// ==================== 血盟資訊 ====================

// ShowClanInfo 顯示血盟資訊。
func (s *ClanSystem) ShowClanInfo(sess *net.Session, player *world.PlayerInfo) {
	if player.ClanID == 0 {
		handler.SendServerMessage(sess, 1064) // "不屬於血盟"
		return
	}

	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		handler.SendServerMessage(sess, 1064)
		return
	}

	// 血盟公告 (S_PacketBox subtype 167)
	sendPledgeAnnounce(sess, clan)

	// 全成員列表 (S_PacketBox subtype 170)
	sendPledgeMembers(sess, clan, s.deps, false)

	// 在線成員列表 (S_PacketBox subtype 171)
	sendPledgeMembers(sess, clan, s.deps, true)
}

// ==================== 設定 ====================

// UpdateSettings 更新血盟公告或成員備註。
func (s *ClanSystem) UpdateSettings(sess *net.Session, player *world.PlayerInfo, dataType byte, content string) {
	if player.ClanID == 0 {
		return
	}

	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch dataType {
	case 15: // 設定血盟公告（盟主專用）
		if clan.LeaderID != player.CharID {
			handler.SendServerMessage(sess, 518)
			return
		}

		announcement := truncateBig5(content, 478)

		err := s.deps.ClanRepo.UpdateAnnouncement(ctx, clan.ClanID, announcement)
		if err != nil {
			s.deps.Log.Error(fmt.Sprintf("更新血盟公告失敗  err=%v", err))
			return
		}

		clan.Announcement = announcement

	case 16: // 設定個人備註
		notes := truncateBig5(content, 62)

		err := s.deps.ClanRepo.UpdateMemberNotes(ctx, clan.ClanID, player.CharID, notes)
		if err != nil {
			s.deps.Log.Error(fmt.Sprintf("更新成員備註失敗  err=%v", err))
			return
		}

		member := clan.Members[player.CharID]
		if member != nil {
			member.Notes = notes
		}
	}
}

// ==================== 階級 ====================

// ChangeRank 變更成員階級。
func (s *ClanSystem) ChangeRank(sess *net.Session, player *world.PlayerInfo, rank int16, targetName string) {
	if player.ClanID == 0 {
		return
	}

	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	// 不能變更自己的階級
	if targetName == player.Name {
		handler.SendServerMessage(sess, 2068)
		return
	}

	// 階級範圍 (2-10)
	if rank < 2 || rank > 10 {
		handler.SendServerMessage(sess, 781)
		return
	}

	// 聯盟階級 (2-6) 未實作
	if rank >= 2 && rank <= 6 {
		return
	}

	// 權限矩陣
	myRank := player.ClanRank
	if !canGrantRank(myRank, rank) {
		handler.SendServerMessage(sess, 2065)
		return
	}

	// 目標必須在線
	target := s.deps.World.GetByName(targetName)
	if target == nil {
		handler.SendServerMessage(sess, 2069) // "對方不在線上"
		return
	}

	// 目標必須在同血盟
	if target.ClanID != player.ClanID {
		handler.SendServerMessage(sess, 414) // "並非血盟成員"
		return
	}

	// 不能變更 rank 9/10 成員的階級（除非自己是 rank 10）
	if (target.ClanRank == world.ClanRankGuardian || target.ClanRank == world.ClanRankPrince) && myRank != world.ClanRankPrince {
		handler.SendServerMessage(sess, 2065)
		return
	}

	// 守護騎士 (rank 9) 等級需求
	if rank == world.ClanRankGuardian {
		if myRank != world.ClanRankPrince && player.Level < 40 {
			handler.SendServerMessage(sess, 2472)
			return
		}
		if target.Level < 40 {
			handler.SendServerMessage(sess, 2473)
			return
		}
	}

	// DB 更新
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := s.deps.ClanRepo.UpdateMemberRank(ctx, clan.ClanID, target.CharID, rank)
	if err != nil {
		s.deps.Log.Error(fmt.Sprintf("階級變更失敗  target=%s  err=%v", targetName, err))
		return
	}

	// 記憶體更新
	target.ClanRank = rank
	member := clan.Members[target.CharID]
	if member != nil {
		member.Rank = rank
	}

	// 通知
	sendRankChanged(sess, byte(rank), targetName)
	sendRankChanged(target.Session, byte(rank), targetName)

	s.deps.Log.Info(fmt.Sprintf("階級變更  target=%s  rank=%d  by=%s", targetName, rank, player.Name))
}

// canGrantRank 檢查操作者是否有權授予目標階級。
// 權限矩陣（Java C_RestartMenu.java）：
//
//	rank 10 (Prince): 可授予 7, 8, 9
//	rank 9  (Guardian): 可授予 7, 8
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

// ==================== 稱號 ====================

// SetTitle 設定稱號。
func (s *ClanSystem) SetTitle(sess *net.Session, player *world.PlayerInfo, charName, title string) {
	// 截斷稱號（Java: 16 字元）
	if len(title) > 48 { // ~16 CJK 字 × 3 bytes UTF-8
		title = title[:48]
	}

	settingSelf := charName == player.Name

	if settingSelf {
		// --- 設定自己的稱號 ---
		if player.ClanID != 0 {
			clan := s.deps.World.Clans.GetClan(player.ClanID)
			if clan != nil && clan.LeaderID == player.CharID {
				if player.Level < 10 {
					handler.SendServerMessage(sess, 197)
					return
				}
			} else {
				if !s.deps.Config.Character.ChangeTitleByOneself {
					handler.SendServerMessage(sess, 198)
					return
				}
				if player.Level < 10 {
					handler.SendServerMessage(sess, 197)
					return
				}
			}
		} else {
			if player.Level < 40 {
				handler.SendServerMessage(sess, 200)
				return
			}
		}

		// 套用稱號
		player.Title = title
		player.Dirty = true
		sendCharTitle(sess, player.CharID, title)

		// 廣播到附近
		nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
		for _, other := range nearby {
			sendCharTitle(other.Session, player.CharID, title)
		}
	} else {
		// --- 設定他人稱號 ---
		if player.ClanID == 0 {
			return
		}
		clan := s.deps.World.Clans.GetClan(player.ClanID)
		if clan == nil || clan.LeaderID != player.CharID {
			return
		}

		if player.Level < 10 {
			handler.SendServerMessage(sess, 197)
			return
		}

		target := s.deps.World.GetByName(charName)
		if target == nil {
			return
		}

		if target.ClanID != player.ClanID {
			handler.SendServerMessage(sess, 199)
			return
		}

		if target.Level < 10 {
			handler.SendServerMessage(sess, 202)
			return
		}

		target.Title = title
		target.Dirty = true
		sendCharTitle(target.Session, target.CharID, title)

		nearby := s.deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
		for _, other := range nearby {
			sendCharTitle(other.Session, target.CharID, title)
		}

		for charID := range clan.Members {
			member := s.deps.World.GetByCharID(charID)
			if member != nil {
				handler.SendServerMessageArgs(member.Session, 203, charName, title)
			}
		}
	}
}

// ==================== 盟徽 ====================

// UploadEmblem 上傳盟徽。
func (s *ClanSystem) UploadEmblem(sess *net.Session, player *world.PlayerInfo, emblemData []byte) {
	if player.ClanID == 0 {
		return
	}

	if player.ClanRank != world.ClanRankPrince {
		return
	}

	clan := s.deps.World.Clans.GetClan(player.ClanID)
	if clan == nil {
		return
	}

	if len(emblemData) < 384 {
		return
	}

	// 產生新盟徽 ID
	newEmblemID := world.NextEmblemID()

	// 寫入盟徽檔案
	emblemPath := fmt.Sprintf("emblem/%d", newEmblemID)
	if err := os.WriteFile(emblemPath, emblemData, 0644); err != nil {
		s.deps.Log.Error(fmt.Sprintf("盟徽寫入失敗  clanID=%d  err=%v", clan.ClanID, err))
		return
	}

	// DB 更新
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.deps.ClanRepo.UpdateEmblemID(ctx, clan.ClanID, newEmblemID); err != nil {
		s.deps.Log.Error(fmt.Sprintf("盟徽DB更新失敗  clanID=%d  err=%v", clan.ClanID, err))
		return
	}

	// 記憶體更新
	clan.EmblemID = newEmblemID
	clan.EmblemStatus = 1

	// 廣播到所有在線成員
	for charID := range clan.Members {
		member := s.deps.World.GetByCharID(charID)
		if member != nil {
			sendCharResetEmblem(member.Session, member.CharID, newEmblemID)
			handler.SendPledgeEmblemStatus(member.Session, 1)
		}
	}

	s.deps.Log.Info(fmt.Sprintf("盟徽上傳  clan=%s  emblemID=%d", clan.ClanName, newEmblemID))
}

// DownloadEmblem 下載盟徽。
func (s *ClanSystem) DownloadEmblem(sess *net.Session, emblemID int32) {
	if emblemID <= 0 {
		return
	}

	emblemPath := fmt.Sprintf("emblem/%d", emblemID)
	emblemData, err := os.ReadFile(emblemPath)
	if err != nil {
		return
	}

	sendEmblem(sess, emblemID, emblemData)
}

// ==================== 封包建構（血盟專用） ====================

// sendCharTitle 發送 S_OPCODE_CHARTITLE (183) — 更新玩家稱號。
func sendCharTitle(sess *net.Session, objID int32, title string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARTITLE)
	w.WriteD(objID)
	w.WriteS(title)
	sess.Send(w.Bytes())
}

// sendRankChanged 發送 S_PacketBox(27) — 階級變更通知。
func sendRankChanged(sess *net.Session, rank byte, name string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(27)
	w.WriteC(rank)
	w.WriteS(name)
	sess.Send(w.Bytes())
}

// sendCharResetEmblem 發送 S_OPCODE_VOICE_CHAT (64) sub-type 0x3c — 盟徽更新。
func sendCharResetEmblem(sess *net.Session, pcObjID int32, emblemID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARSYNACK)
	w.WriteC(0x3c)
	w.WriteD(pcObjID)
	w.WriteD(emblemID)
	sess.Send(w.Bytes())
}

// sendEmblem 發送 S_OPCODE_EMBLEM (118) — 盟徽資料。
func sendEmblem(sess *net.Session, emblemID int32, data []byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EMBLEM)
	w.WriteD(emblemID)
	w.WriteBytes(data)
	sess.Send(w.Bytes())
}

// sendPledgeAnnounce 發送 S_PacketBox subtype 167 — 血盟公告視窗。
func sendPledgeAnnounce(sess *net.Session, clan *world.ClanInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(167)
	w.WriteS(clan.ClanName)
	w.WriteS(clan.LeaderName)
	w.WriteD(clan.EmblemID)
	w.WriteD(clan.FoundDate)

	ann := make([]byte, 478)
	copy(ann, clan.Announcement)
	w.WriteBytes(ann)

	sess.Send(w.Bytes())
}

// sendPledgeMembers 發送 S_PacketBox subtype 170 或 171 — 成員列表。
func sendPledgeMembers(sess *net.Session, clan *world.ClanInfo, deps *handler.Deps, onlineOnly bool) {
	if onlineOnly {
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
	w.WriteH(1)
	w.WriteC(byte(len(members)))

	for _, m := range members {
		w.WriteS(m.name)
		w.WriteC(byte(m.rank))
		w.WriteC(byte(m.level))

		notes := make([]byte, 62)
		copy(notes, m.notes)
		w.WriteBytes(notes)

		w.WriteD(m.memberID)
		w.WriteC(byte(m.classType))
	}

	sess.Send(w.Bytes())
}

// ==================== 工具函式 ====================

// truncateBig5 截斷字串到指定位元組數。
func truncateBig5(s string, maxLen int) []byte {
	b := []byte(s)
	if len(b) > maxLen {
		b = b[:maxLen]
	}
	return b
}
