package handler

import (
	"github.com/l1jgo/server/internal/config"
	"github.com/l1jgo/server/internal/core/event"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// AttackRequest is queued by the handler and processed by CombatSystem in Phase 2.
type AttackRequest struct {
	AttackerSessionID uint64
	TargetID          int32
	IsMelee           bool // true=melee (C_ATTACK), false=ranged (C_FAR_ATTACK)
}

// CombatQueue accepts attack requests from handlers for deferred Phase 2 processing.
type CombatQueue interface {
	QueueAttack(req AttackRequest)
}

// SkillRequest is queued by the handler and processed by SkillSystem in Phase 2.
type SkillRequest struct {
	SessionID uint64
	SkillID   int32
	TargetID  int32
}

// SkillManager 處理技能執行、buff 管理、buff 計時。由 system.SkillSystem 實作。
type SkillManager interface {
	// QueueSkill 將技能請求排入佇列（Phase 2 處理）。
	QueueSkill(req SkillRequest)
	// CancelAllBuffs 移除目標所有可取消的 buff（Cancellation 效果）。
	CancelAllBuffs(target *world.PlayerInfo)
	// TickPlayerBuffs 每 tick 遞減 buff 計時器並處理到期。
	TickPlayerBuffs(p *world.PlayerInfo)
	// RemoveBuffAndRevert 移除指定 buff 並還原屬性。
	RemoveBuffAndRevert(target *world.PlayerInfo, skillID int32)
}

// NpcKillResult is returned by ProcessMeleeAttack/ProcessRangedAttack when an NPC
// dies. CombatSystem uses it to emit EntityKilled events on the bus.
type NpcKillResult struct {
	KillerSessionID uint64
	KillerCharID    int32
	NpcID           int32 // world NPC object ID
	NpcTemplateID   int32 // NPC template ID from spawn data
	ExpGained       int32
	MapID           int16
	X, Y            int32
}

// TradeManager 處理交易邏輯。由 system.TradeSystem 實作。
type TradeManager interface {
	// InitiateTrade 向目標發送交易確認對話框。
	InitiateTrade(sess *net.Session, player, target *world.PlayerInfo)
	// HandleYesNo 處理交易確認回應。
	HandleYesNo(sess *net.Session, player *world.PlayerInfo, partnerID int32, accepted bool)
	// AddItem 將物品加入交易視窗。
	AddItem(sess *net.Session, player *world.PlayerInfo, objectID, count int32)
	// Accept 確認交易。
	Accept(sess *net.Session, player *world.PlayerInfo)
	// Cancel 取消交易（由主動取消方呼叫）。
	Cancel(player *world.PlayerInfo)
	// CancelIfActive 若正在交易則取消（傳送、移動、開商店等呼叫）。
	CancelIfActive(player *world.PlayerInfo)
}

// PartyManager 處理隊伍邏輯（一般隊伍 + 聊天隊伍）。由 system.PartySystem 實作。
type PartyManager interface {
	// Invite 發送一般隊伍邀請（type 0=普通, 1=自動分配）。
	Invite(sess *net.Session, player *world.PlayerInfo, targetID int32, partyType byte)
	// ChatInvite 發送聊天隊伍邀請（type 2）。
	ChatInvite(sess *net.Session, player *world.PlayerInfo, targetName string)
	// TransferLeader 轉移隊長（type 3）。
	TransferLeader(sess *net.Session, player *world.PlayerInfo, targetID int32)
	// ShowPartyInfo 顯示隊伍成員 HTML 對話框。
	ShowPartyInfo(sess *net.Session, player *world.PlayerInfo)
	// Leave 自願離開隊伍。
	Leave(player *world.PlayerInfo)
	// BanishMember 踢除隊員（隊長專用，依名稱）。
	BanishMember(sess *net.Session, player *world.PlayerInfo, targetName string)

	// ChatKick 踢除聊天隊伍成員。
	ChatKick(sess *net.Session, player *world.PlayerInfo, targetName string)
	// ChatLeave 離開聊天隊伍。
	ChatLeave(player *world.PlayerInfo)
	// ShowChatPartyInfo 顯示聊天隊伍成員 HTML 對話框。
	ShowChatPartyInfo(sess *net.Session, player *world.PlayerInfo)

	// InviteResponse 處理一般隊伍邀請的 Yes/No 回應（953/954）。
	InviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool)
	// ChatInviteResponse 處理聊天隊伍邀請的 Yes/No 回應（951）。
	ChatInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool)

	// UpdateMiniHP 廣播 HP 變化到隊伍成員。
	UpdateMiniHP(player *world.PlayerInfo)
	// RefreshPositions 發送位置更新到該玩家的隊伍。
	RefreshPositions(player *world.PlayerInfo)
}

// ClanManager 處理血盟邏輯。由 system.ClanSystem 實作。
type ClanManager interface {
	// Create 建立新血盟。
	Create(sess *net.Session, player *world.PlayerInfo, clanName string)
	// JoinRequest 發送加入血盟請求（面對面機制）。
	JoinRequest(sess *net.Session, player *world.PlayerInfo)
	// JoinResponse 處理加入血盟的 Yes/No 回應（97）。
	JoinResponse(sess *net.Session, responder *world.PlayerInfo, applicantCharID int32, accepted bool)
	// Leave 離開或解散血盟。
	Leave(sess *net.Session, player *world.PlayerInfo, clanNamePkt string)
	// BanMember 驅逐血盟成員。
	BanMember(sess *net.Session, player *world.PlayerInfo, targetName string)
	// ShowClanInfo 顯示血盟資訊。
	ShowClanInfo(sess *net.Session, player *world.PlayerInfo)
	// UpdateSettings 更新血盟公告或成員備註。
	UpdateSettings(sess *net.Session, player *world.PlayerInfo, dataType byte, content string)
	// ChangeRank 變更成員階級。
	ChangeRank(sess *net.Session, player *world.PlayerInfo, rank int16, targetName string)
	// SetTitle 設定稱號。
	SetTitle(sess *net.Session, player *world.PlayerInfo, charName, title string)
	// UploadEmblem 上傳盟徽。
	UploadEmblem(sess *net.Session, player *world.PlayerInfo, emblemData []byte)
	// DownloadEmblem 下載盟徽。
	DownloadEmblem(sess *net.Session, emblemID int32)
}

// EquipManager 處理裝備邏輯（穿脫武器/防具、套裝系統、屬性計算）。由 system.EquipSystem 實作。
type EquipManager interface {
	// EquipWeapon 裝備武器或脫下已裝備的武器。
	EquipWeapon(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, info *data.ItemInfo)
	// EquipArmor 裝備防具或脫下已裝備的防具。
	EquipArmor(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, info *data.ItemInfo)
	// UnequipSlot 脫下指定欄位的裝備。
	UnequipSlot(sess *net.Session, player *world.PlayerInfo, slot world.EquipSlot)
	// FindEquippedSlot 找到物品所在的裝備欄位。
	FindEquippedSlot(player *world.PlayerInfo, item *world.InvItem) world.EquipSlot
	// RecalcEquipStats 重新計算裝備屬性並發送更新封包。
	RecalcEquipStats(sess *net.Session, player *world.PlayerInfo)
	// InitEquipStats 進入世界時初始化裝備屬性（偵測套裝 + 設定基礎 AC + 計算裝備加成，不發送封包）。
	InitEquipStats(player *world.PlayerInfo)
	// SendEquipList 發送裝備欄位列表封包。
	SendEquipList(sess *net.Session, player *world.PlayerInfo)
}

// ItemUseManager 處理物品使用邏輯（消耗品、衝裝、鑑定、技能書、傳送卷軸、掉落）。由 system.ItemUseSystem 實作。
type ItemUseManager interface {
	// UseConsumable 處理消耗品使用（藥水、食物）。
	UseConsumable(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo)
	// EnchantItem 處理衝裝卷軸使用。
	EnchantItem(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, scroll *world.InvItem, scrollInfo *data.ItemInfo)
	// IdentifyItem 處理鑑定卷軸使用。
	IdentifyItem(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, scroll *world.InvItem)
	// UseSpellBook 處理技能書使用。
	UseSpellBook(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, itemInfo *data.ItemInfo)
	// UseTeleportScroll 處理傳送卷軸使用。
	UseTeleportScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, item *world.InvItem)
	// UseHomeScroll 處理回家卷軸使用。
	UseHomeScroll(sess *net.Session, player *world.PlayerInfo, item *world.InvItem)
	// UseFixedTeleportScroll 處理指定傳送卷軸使用。
	UseFixedTeleportScroll(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, itemInfo *data.ItemInfo)
	// GiveDrops 為擊殺的 NPC 擲骰掉落物品。
	GiveDrops(killer *world.PlayerInfo, npcID int32)
	// ApplyHaste 套用加速效果。
	ApplyHaste(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32)
	// BroadcastEffect 向自己和附近玩家廣播特效。
	BroadcastEffect(sess *net.Session, player *world.PlayerInfo, gfxID int32)
}

// Deps holds shared dependencies injected into all packet handlers.
type Deps struct {
	AccountRepo *persist.AccountRepo
	CharRepo    *persist.CharacterRepo
	ItemRepo    *persist.ItemRepo
	Config      *config.Config
	Log         *zap.Logger
	World       *world.State
	Scripting   *scripting.Engine
	NpcActions  *data.NpcActionTable
	Items       *data.ItemTable
	Shops       *data.ShopTable
	Drops       *data.DropTable
	Teleports     *data.TeleportTable
	TeleportHtml  *data.TeleportHtmlTable
	Portals       *data.PortalTable
	Skills        *data.SkillTable
	Npcs          *data.NpcTable
	MobSkills      *data.MobSkillTable
	MapData        *data.MapDataTable
	Polys          *data.PolymorphTable
	ArmorSets      *data.ArmorSetTable
	SprTable       *data.SprTable
	WarehouseRepo  *persist.WarehouseRepo
	WALRepo        *persist.WALRepo
	ClanRepo       *persist.ClanRepo
	BuffRepo       *persist.BuffRepo
	Doors          *data.DoorTable
	ItemMaking     *data.ItemMakingTable
	SpellbookReqs  *data.SpellbookReqTable
	BuffIcons      *data.BuffIconTable
	NpcServices    *data.NpcServiceTable
	BuddyRepo     *persist.BuddyRepo
	ExcludeRepo   *persist.ExcludeRepo
	BoardRepo     *persist.BoardRepo
	MailRepo      *persist.MailRepo
	PetRepo       *persist.PetRepo
	PetTypes      *data.PetTypeTable
	PetItems      *data.PetItemTable
	Dolls         *data.DollTable
	TeleportPages *data.TeleportPageTable
	Combat        CombatQueue  // filled after CombatSystem is created
	Skill         SkillManager // filled after SkillSystem is created
	Trade         TradeManager // filled after TradeSystem is created
	Party         PartyManager // filled after PartySystem is created
	Clan          ClanManager  // filled after ClanSystem is created
	Equip         EquipManager    // filled after EquipSystem is created
	ItemUse       ItemUseManager  // filled after ItemUseSystem is created
	Bus           *event.Bus  // event bus for emitting game events (EntityKilled, etc.)
	WeaponSkills  *data.WeaponSkillTable
}

// RegisterAll registers all packet handlers into the registry.
func RegisterAll(reg *packet.Registry, deps *Deps) {
	// Handshake phase
	reg.Register(packet.C_OPCODE_VERSION,
		[]packet.SessionState{packet.StateHandshake},
		func(sess any, r *packet.Reader) {
			HandleVersion(sess.(*net.Session), r, deps)
		},
	)

	// Login phase — BeanFun login (opcode 210) has action byte prefix
	reg.Register(packet.C_OPCODE_SHIFT_SERVER,
		[]packet.SessionState{packet.StateVersionOK},
		func(sess any, r *packet.Reader) {
			HandleAuthBeanFun(sess.(*net.Session), r, deps)
		},
	)
	// Direct login (opcode 119) — no action byte, just account\0 password\0
	reg.Register(packet.C_OPCODE_LOGIN,
		[]packet.SessionState{packet.StateVersionOK},
		func(sess any, r *packet.Reader) {
			HandleAuthDirect(sess.(*net.Session), r, deps)
		},
	)

	// Authenticated phase (character select screen)
	authStates := []packet.SessionState{packet.StateAuthenticated, packet.StateReturningToSelect}

	reg.Register(packet.C_OPCODE_CREATE_CUSTOM_CHARACTER, authStates,
		func(sess any, r *packet.Reader) {
			HandleCreateChar(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_DELETE_CHARACTER, authStates,
		func(sess any, r *packet.Reader) {
			HandleDeleteChar(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ENTER_WORLD,
		[]packet.SessionState{packet.StateAuthenticated},
		func(sess any, r *packet.Reader) {
			HandleEnterWorld(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_REQUEST_ROLL,
		[]packet.SessionState{packet.StateAuthenticated, packet.StateInWorld, packet.StateReturningToSelect},
		func(sess any, r *packet.Reader) {
			HandleChangeChar(sess.(*net.Session), r, deps)
		},
	)

	// In-world phase
	inWorldStates := []packet.SessionState{packet.StateInWorld}

	reg.Register(packet.C_OPCODE_MOVE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleMove(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_CHANGE_DIRECTION, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleChangeDirection(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ATTR, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleAttr(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_DUEL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleDuel(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ATTACK, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleAttack(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_FAR_ATTACK, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleFarAttack(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_CHECK_PK, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleCheckPK(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_DIALOG, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleNpcTalk(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_HACTION, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleNpcAction(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BUY_SELL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBuySell(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_CHAT, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleChat(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_SAY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleSay(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_TELL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleWhisper(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_USE_ITEM, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleUseItem(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_DESTROY_ITEM, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleDestroyItem(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_DROP, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleDropItem(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_GET, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandlePickupItem(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_FIXABLE_ITEM, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleFixWeaponList(sess.(*net.Session), r, deps)
		},
	)
	// C_FIX (118) = C_FixWeaponList in Java — same handler as C_FIXABLE_ITEM (254).
	// Both opcodes query the damaged weapon list; the client may send either one.
	reg.Register(packet.C_OPCODE_FIX, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleFixWeaponList(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_PERSONAL_SHOP, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleSelectList(sess.(*net.Session), r, deps)
		},
	)
	// Ship transport
	reg.Register(packet.C_OPCODE_ENTER_SHIP, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleEnterShip(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_RESTART, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleRestart(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ACTION, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleAction(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BOOKMARK, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBookmark(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_DELETE_BOOKMARK, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleDeleteBookmark(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_PLATE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBoardOrPlate(sess.(*net.Session), r, deps)
		},
	)
	// Board (bulletin board)
	reg.Register(packet.C_OPCODE_BOARD_LIST, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBoardBack(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BOARD_READ, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBoardRead(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BOARD_WRITE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBoardWrite(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BOARD_DELETE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBoardDelete(sess.(*net.Session), r, deps)
		},
	)

	// Mail
	reg.Register(packet.C_OPCODE_MAIL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleMail(sess.(*net.Session), r, deps)
		},
	)

	reg.Register(packet.C_OPCODE_TELEPORT, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleTeleport(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ENTER_PORTAL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleEnterPortal(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_USE_SPELL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleUseSpell(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BUY_SPELL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBuySpell(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BUYABLE_SPELL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBuyableSpell(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_SAVEIO, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleCharConfig(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_OPEN, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleOpen(sess.(*net.Session), r, deps)
		},
	)

	// Warehouse: all warehouse ops go through C_BUY_SELL (opcode 161) with resultType 2-9.
	// C_DEPOSIT(56) and C_WITHDRAW(44) are castle treasury opcodes, not warehouse.

	// Party
	// C_WHO_PARTY (230) = C_CreateParty in Java — party invite
	reg.Register(packet.C_OPCODE_WHO_PARTY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleInviteParty(sess.(*net.Session), r, deps)
		},
	)
	// C_INVITE_PARTY_TARGET (43) = C_Party in Java — query party info
	reg.Register(packet.C_OPCODE_INVITE_PARTY_TARGET, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleWhoParty(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_LEAVE_PARTY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleLeaveParty(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BANISH_PARTY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBanishParty(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_CHAT_PARTY_CONTROL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandlePartyControl(sess.(*net.Session), r, deps)
		},
	)

	// Clan
	reg.Register(packet.C_OPCODE_CREATE_PLEDGE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleCreateClan(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_JOIN_PLEDGE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleJoinClan(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_LEAVE_PLEDGE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleLeaveClan(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_BAN_MEMBER, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleBanMember(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_WHO_PLEDGE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleWhoPledge(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_PLEDGE_WATCH, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandlePledgeWatch(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_RANK_CONTROL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleRankControl(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_TITLE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleTitle(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_UPLOAD_EMBLEM, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleEmblemUpload(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ALT_ATTACK, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleEmblemDownload(sess.(*net.Session), r, deps)
		},
	)

	// Polymorph (monlist dialog input)
	reg.Register(packet.C_OPCODE_HYPERTEXT_INPUT_RESULT, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleHypertextInputResult(sess.(*net.Session), r, deps)
		},
	)

	// Trade
	reg.Register(packet.C_OPCODE_ASK_XCHG, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleAskTrade(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ADD_XCHG, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleAddTrade(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ACCEPT_XCHG, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleAcceptTrade(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_CANCEL_XCHG, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleCancelTrade(sess.(*net.Session), r, deps)
		},
	)

	// Buddy / Friend list
	reg.Register(packet.C_OPCODE_QUERY_BUDDY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleQueryBuddy(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_ADD_BUDDY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleAddBuddy(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_REMOVE_BUDDY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleRemoveBuddy(sess.(*net.Session), r, deps)
		},
	)

	// Exclude / Block list
	reg.Register(packet.C_OPCODE_EXCLUDE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleExclude(sess.(*net.Session), r, deps)
		},
	)

	// Who online
	reg.Register(packet.C_OPCODE_WHO, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleWho(sess.(*net.Session), r, deps)
		},
	)

	// Give item to NPC/Pet
	reg.Register(packet.C_OPCODE_GIVE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleGiveItem(sess.(*net.Session), r, deps)
		},
	)

	// Pet
	reg.Register(packet.C_OPCODE_CHECK_INVENTORY, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandlePetMenu(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_NPC_ITEM_CONTROL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleUsePetItem(sess.(*net.Session), r, deps)
		},
	)

	// Mercenary (stub)
	reg.Register(packet.C_OPCODE_MERCENARYARRANGE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleMercenaryArrange(sess.(*net.Session), r, deps)
		},
	)

	// Always allowed (any active state)
	aliveStates := []packet.SessionState{
		packet.StateVersionOK, packet.StateAuthenticated,
		packet.StateInWorld, packet.StateReturningToSelect,
	}
	reg.Register(packet.C_OPCODE_ALIVE, aliveStates,
		func(sess any, r *packet.Reader) {
			// Java: C_KeepALIVE sends S_GameTime to keep client time synced (day/night cycle).
			s := sess.(*net.Session)
			if s.State() == packet.StateInWorld {
				sendGameTime(s, world.GameTimeNow().Seconds())
			}
		},
	)
	reg.Register(packet.C_OPCODE_QUIT, aliveStates,
		func(sess any, r *packet.Reader) {
			HandleQuit(sess.(*net.Session), r, deps)
		},
	)
}
