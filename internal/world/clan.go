package world

import (
	"strings"
	"sync/atomic"
)

// emblemIDCounter generates unique emblem IDs.
// Starts at 1; initialized from DB max on startup.
var emblemIDCounter atomic.Int32

// NextEmblemID returns a unique emblem ID.
func NextEmblemID() int32 {
	return emblemIDCounter.Add(1)
}

// SetEmblemIDStart sets the emblem counter start value.
// Called on startup with max(persisted_max_emblem_id, 0) to avoid collisions.
func SetEmblemIDStart(v int32) {
	emblemIDCounter.Store(v)
}

// Clan rank constants (Java: L1Clan.java)
const (
	ClanRankLeaguePublic     int16 = 2  // 聯盟一般 (deferred)
	ClanRankLeagueVicePrince int16 = 3  // 聯盟副君主 (deferred)
	ClanRankLeaguePrince     int16 = 4  // 聯盟君主 (deferred)
	ClanRankLeagueProbation  int16 = 5  // 聯盟見習 (deferred)
	ClanRankLeagueGuardian   int16 = 6  // 聯盟守護騎士 (deferred)
	ClanRankPublic           int16 = 7  // 一般成員
	ClanRankProbation        int16 = 8  // 見習成員
	ClanRankGuardian         int16 = 9  // 守護騎士
	ClanRankPrince           int16 = 10 // 君主（盟主）
)

// ClanMember holds data for a single clan member.
type ClanMember struct {
	CharID   int32
	CharName string
	Rank     int16  // ClanRankPrince, ClanRankPublic, etc.
	Notes    []byte // up to 62 bytes Big5 encoded
}

// ClanInfo holds in-memory data for a clan.
type ClanInfo struct {
	ClanID       int32
	ClanName     string
	LeaderID     int32
	LeaderName   string
	FoundDate    int32  // Unix timestamp in seconds
	HasCastle    int32
	HasHouse     int32
	Announcement []byte // up to 478 bytes Big5 encoded
	EmblemID     int32
	EmblemStatus int16
	Members      map[int32]*ClanMember // charID → member

	// 血盟倉庫單人使用鎖定（Java: L1Clan._warehouse）
	// 0 = 無人使用；>0 = 該角色 ID 正在使用血盟倉庫。
	// 設定時機：開啟血盟倉庫時設為 charID。
	// 清除時機：操作完成、按取消、離線、傳送、退盟。
	WarehouseUsingCharID int32
}

// MemberCount returns the number of members in the clan.
func (c *ClanInfo) MemberCount() int {
	return len(c.Members)
}

// ClanManager manages all clans in memory.
// Single-goroutine access only (game loop).
type ClanManager struct {
	clans      map[int32]*ClanInfo // clanID → clan
	playerClan map[int32]int32     // charID → clanID
	clanByName map[string]int32    // lowercase clanName → clanID
}

// NewClanManager creates an empty ClanManager.
func NewClanManager() *ClanManager {
	return &ClanManager{
		clans:      make(map[int32]*ClanInfo),
		playerClan: make(map[int32]int32),
		clanByName: make(map[string]int32),
	}
}

// GetClan returns a clan by its ID, or nil.
func (m *ClanManager) GetClan(clanID int32) *ClanInfo {
	return m.clans[clanID]
}

// GetClanByName returns a clan by its name (case-insensitive), or nil.
func (m *ClanManager) GetClanByName(name string) *ClanInfo {
	cid, ok := m.clanByName[strings.ToLower(name)]
	if !ok {
		return nil
	}
	return m.clans[cid]
}

// GetPlayerClanID returns the clanID for a player, or 0 if not in a clan.
func (m *ClanManager) GetPlayerClanID(charID int32) int32 {
	return m.playerClan[charID]
}

// ClanNameExists returns true if a clan with this name exists (case-insensitive).
func (m *ClanManager) ClanNameExists(name string) bool {
	_, ok := m.clanByName[strings.ToLower(name)]
	return ok
}

// ClanCount returns the total number of clans.
func (m *ClanManager) ClanCount() int {
	return len(m.clans)
}

// IsLeader returns true if the character is a clan leader.
func (m *ClanManager) IsLeader(charID int32) bool {
	cid := m.playerClan[charID]
	if cid == 0 {
		return false
	}
	clan := m.clans[cid]
	return clan != nil && clan.LeaderID == charID
}

// AddClan registers a clan in memory. Called after DB insert succeeds.
func (m *ClanManager) AddClan(clan *ClanInfo) {
	m.clans[clan.ClanID] = clan
	m.clanByName[strings.ToLower(clan.ClanName)] = clan.ClanID
	for charID := range clan.Members {
		m.playerClan[charID] = clan.ClanID
	}
}

// RemoveClan removes a clan and all member mappings. Called after DB delete succeeds.
func (m *ClanManager) RemoveClan(clanID int32) {
	clan := m.clans[clanID]
	if clan == nil {
		return
	}
	for charID := range clan.Members {
		delete(m.playerClan, charID)
	}
	delete(m.clanByName, strings.ToLower(clan.ClanName))
	delete(m.clans, clanID)
}

// AddMember adds a member to a clan. Called after DB insert succeeds.
func (m *ClanManager) AddMember(clanID int32, member *ClanMember) {
	clan := m.clans[clanID]
	if clan == nil {
		return
	}
	clan.Members[member.CharID] = member
	m.playerClan[member.CharID] = clanID
}

// RemoveMember removes a member from a clan. Called after DB delete succeeds.
func (m *ClanManager) RemoveMember(clanID, charID int32) {
	clan := m.clans[clanID]
	if clan == nil {
		return
	}
	delete(clan.Members, charID)
	delete(m.playerClan, charID)
}
