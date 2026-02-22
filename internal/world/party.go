package world

const MaxPartySize = 8
const MaxChatPartySize = 8

// PartyType distinguishes normal parties from auto-share parties.
// Java: type 0 = normal, type 1 = auto-share (loot distribution).
// Both use the same L1Party class in Java — only the invite message differs.
type PartyType byte

const (
	PartyTypeNormal    PartyType = 0
	PartyTypeAutoShare PartyType = 1
)

// PartyInfo tracks a group of players.
type PartyInfo struct {
	LeaderID  int32     // CharID of party leader
	Members   []int32   // CharIDs of all members (including leader)
	PartyType PartyType // 0=normal, 1=auto-share
}

// PartyManager manages all active parties (normal/auto-share).
type PartyManager struct {
	parties        map[int32]*PartyInfo // partyID (=leaderID) → party
	playerParty    map[int32]int32      // charID → partyID
	pendingInvites map[int32]int32      // targetCharID → inviterCharID
}

func NewPartyManager() *PartyManager {
	return &PartyManager{
		parties:        make(map[int32]*PartyInfo),
		playerParty:    make(map[int32]int32),
		pendingInvites: make(map[int32]int32),
	}
}

// GetParty returns the party a player belongs to, or nil.
func (m *PartyManager) GetParty(charID int32) *PartyInfo {
	pid, ok := m.playerParty[charID]
	if !ok {
		return nil
	}
	return m.parties[pid]
}

// IsInParty returns true if the player is in any party.
func (m *PartyManager) IsInParty(charID int32) bool {
	_, ok := m.playerParty[charID]
	return ok
}

// IsLeader returns true if the player is the leader of their party.
func (m *PartyManager) IsLeader(charID int32) bool {
	p := m.GetParty(charID)
	if p == nil {
		return false
	}
	return p.LeaderID == charID
}

// CreateParty creates a new party with the leader and one member.
func (m *PartyManager) CreateParty(leaderID, memberID int32, pType PartyType) *PartyInfo {
	p := &PartyInfo{
		LeaderID:  leaderID,
		Members:   []int32{leaderID, memberID},
		PartyType: pType,
	}
	m.parties[leaderID] = p
	m.playerParty[leaderID] = leaderID
	m.playerParty[memberID] = leaderID
	return p
}

// AddMember adds a player to an existing party. Returns false if full.
func (m *PartyManager) AddMember(partyID, charID int32) bool {
	p := m.parties[partyID]
	if p == nil || len(p.Members) >= MaxPartySize {
		return false
	}
	p.Members = append(p.Members, charID)
	m.playerParty[charID] = partyID
	return true
}

// RemoveMember removes a single player from their party.
// Does NOT auto-promote or dissolve — caller handles that logic.
// Returns the remaining party (may have 0 or 1 members), or nil if not found.
func (m *PartyManager) RemoveMember(charID int32) *PartyInfo {
	pid, ok := m.playerParty[charID]
	if !ok {
		return nil
	}
	delete(m.playerParty, charID)

	p := m.parties[pid]
	if p == nil {
		return nil
	}

	// Remove from members list
	for i, id := range p.Members {
		if id == charID {
			p.Members = append(p.Members[:i], p.Members[i+1:]...)
			break
		}
	}

	return p
}

// Dissolve removes all members from a party and cleans up maps.
func (m *PartyManager) Dissolve(partyID int32) {
	p := m.parties[partyID]
	if p == nil {
		return
	}
	for _, id := range p.Members {
		delete(m.playerParty, id)
	}
	delete(m.parties, partyID)
}

// SetLeader transfers party leadership from oldLeader to newLeader.
// Both must be in the same party. The party is re-keyed under the new leader's ID.
func (m *PartyManager) SetLeader(oldLeaderID, newLeaderID int32) {
	p := m.GetParty(oldLeaderID)
	if p == nil {
		return
	}

	// Remove old party key
	delete(m.parties, p.LeaderID)

	// Set new leader
	p.LeaderID = newLeaderID

	// Re-register under new leader ID
	m.parties[newLeaderID] = p
	for _, id := range p.Members {
		m.playerParty[id] = newLeaderID
	}
}

// SetInvite records a pending party invite.
func (m *PartyManager) SetInvite(targetID, inviterID int32) {
	m.pendingInvites[targetID] = inviterID
}

// GetInvite returns and clears a pending invite for the target. Returns 0 if none.
func (m *PartyManager) GetInvite(targetID int32) int32 {
	inviterID, ok := m.pendingInvites[targetID]
	if !ok {
		return 0
	}
	delete(m.pendingInvites, targetID)
	return inviterID
}

// ClearInvite removes a pending invite.
func (m *PartyManager) ClearInvite(targetID int32) {
	delete(m.pendingInvites, targetID)
}

// --- Chat Party (separate from normal party) ---

// ChatPartyInfo tracks a chat-only party.
type ChatPartyInfo struct {
	LeaderID int32
	Members  []int32
}

// ChatPartyManager manages chat parties (type 2).
// A player can be in both a normal party AND a chat party simultaneously.
type ChatPartyManager struct {
	parties     map[int32]*ChatPartyInfo // partyID (=leaderID) → party
	playerParty map[int32]int32          // charID → partyID
}

func NewChatPartyManager() *ChatPartyManager {
	return &ChatPartyManager{
		parties:     make(map[int32]*ChatPartyInfo),
		playerParty: make(map[int32]int32),
	}
}

func (m *ChatPartyManager) GetParty(charID int32) *ChatPartyInfo {
	pid, ok := m.playerParty[charID]
	if !ok {
		return nil
	}
	return m.parties[pid]
}

func (m *ChatPartyManager) IsInParty(charID int32) bool {
	_, ok := m.playerParty[charID]
	return ok
}

func (m *ChatPartyManager) IsLeader(charID int32) bool {
	p := m.GetParty(charID)
	if p == nil {
		return false
	}
	return p.LeaderID == charID
}

func (m *ChatPartyManager) CreateParty(leaderID, memberID int32) *ChatPartyInfo {
	p := &ChatPartyInfo{
		LeaderID: leaderID,
		Members:  []int32{leaderID, memberID},
	}
	m.parties[leaderID] = p
	m.playerParty[leaderID] = leaderID
	m.playerParty[memberID] = leaderID
	return p
}

func (m *ChatPartyManager) AddMember(partyID, charID int32) bool {
	p := m.parties[partyID]
	if p == nil || len(p.Members) >= MaxChatPartySize {
		return false
	}
	p.Members = append(p.Members, charID)
	m.playerParty[charID] = partyID
	return true
}

func (m *ChatPartyManager) RemoveMember(charID int32) *ChatPartyInfo {
	pid, ok := m.playerParty[charID]
	if !ok {
		return nil
	}
	delete(m.playerParty, charID)

	p := m.parties[pid]
	if p == nil {
		return nil
	}

	for i, id := range p.Members {
		if id == charID {
			p.Members = append(p.Members[:i], p.Members[i+1:]...)
			break
		}
	}

	return p
}

func (m *ChatPartyManager) Dissolve(partyID int32) {
	p := m.parties[partyID]
	if p == nil {
		return
	}
	for _, id := range p.Members {
		delete(m.playerParty, id)
	}
	delete(m.parties, partyID)
}

// MembersNameList returns space-separated member names (matching Java getMembersNameList).
func (m *ChatPartyManager) MembersNameList(partyID int32, getPlayerName func(int32) string) string {
	p := m.parties[partyID]
	if p == nil {
		return ""
	}
	result := ""
	for _, id := range p.Members {
		name := getPlayerName(id)
		if name != "" {
			result += name + " "
		}
	}
	return result
}

// --- HP display utilities ---

// CalcPartyHP returns the party HP display byte (0-10, proportional to HP%).
// Used in S_PUT_OBJECT for the overhead HP bar. 0xFF = not in party.
func CalcPartyHP(hp, maxHP int16) byte {
	if maxHP <= 0 {
		return 0
	}
	pct := int(hp) * 10 / int(maxHP)
	if pct > 10 {
		pct = 10
	}
	if pct < 0 {
		pct = 0
	}
	return byte(pct)
}

// CalcHPPercent returns HP as 0-100 percentage.
// Used in S_HPMeter and S_PacketBox party list packets.
func CalcHPPercent(hp, maxHP int16) byte {
	if maxHP <= 0 {
		return 0
	}
	pct := int(hp) * 100 / int(maxHP)
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return byte(pct)
}
