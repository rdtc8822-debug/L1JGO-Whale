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
// 也提供 HandleNpcDeath / AddExp 給 handler 內其他檔案呼叫（weapon_skill, gmcommand）。
type CombatQueue interface {
	QueueAttack(req AttackRequest)
	// HandleNpcDeath 處理 NPC 死亡（經驗、掉落、移除）。
	HandleNpcDeath(npc *world.NpcInfo, killer *world.PlayerInfo, nearby []*world.PlayerInfo) *NpcKillResult
	// AddExp 增加經驗值並檢查升級。
	AddExp(player *world.PlayerInfo, expGain int32)
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
	// ClearAllBuffsOnDeath 死亡時清除所有 buff（含不可取消的）。
	ClearAllBuffsOnDeath(target *world.PlayerInfo)
	// TickPlayerBuffs 每 tick 遞減 buff 計時器並處理到期。
	TickPlayerBuffs(p *world.PlayerInfo)
	// RemoveBuffAndRevert 移除指定 buff 並還原屬性。
	RemoveBuffAndRevert(target *world.PlayerInfo, skillID int32)
	// ApplyNpcDebuff NPC 對玩家施放 debuff 技能（麻痺/睡眠/減速等）。
	ApplyNpcDebuff(target *world.PlayerInfo, skill *data.SkillInfo)
	// CancelAbsoluteBarrier 解除絕對屏障（攻擊/施法/使用道具時）。
	CancelAbsoluteBarrier(player *world.PlayerInfo)
	// CancelInvisibility 解除隱身（攻擊/施法時自動觸發）。
	CancelInvisibility(player *world.PlayerInfo)
	// ApplyGMBuff GM 強制套用 buff（繞過已學/MP/材料驗證）。
	ApplyGMBuff(player *world.PlayerInfo, skillID int32) bool
}

// DeathManager 處理玩家死亡與重生。由 system.DeathSystem 實作。
type DeathManager interface {
	// KillPlayer 處理玩家死亡（動畫、經驗懲罰、清 buff）。
	KillPlayer(player *world.PlayerInfo)
	// ProcessRestart 處理死亡重生（回城、重建 Known）。
	ProcessRestart(sess *net.Session, player *world.PlayerInfo)
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

// SummonManager 處理召喚技能邏輯（召喚/馴服/殭屍/歸返自然）。由 system.SummonSystem 實作。
type SummonManager interface {
	// ExecuteSummonMonster 處理技能 51 召喚怪物。
	ExecuteSummonMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32)
	// ExecuteTamingMonster 處理技能 36 馴服怪物。
	ExecuteTamingMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32)
	// ExecuteCreateZombie 處理技能 41 創造殭屍。
	ExecuteCreateZombie(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32)
	// ExecuteReturnToNature 處理技能 145 歸返自然。
	ExecuteReturnToNature(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo)
	// DismissSummon 自願解散召喚獸。
	DismissSummon(sum *world.SummonInfo, player *world.PlayerInfo)
}

// PolymorphManager 處理變身邏輯（變身/解除、裝備相容檢查）。由 system.PolymorphSystem 實作。
type PolymorphManager interface {
	// DoPoly 將玩家變身為指定形態。cause: PolyCauseMagic(1)/PolyCauseGM(2)/PolyCauseNPC(4)。
	DoPoly(player *world.PlayerInfo, polyID int32, durationSec int, cause int)
	// UndoPoly 解除玩家變身，恢復原始外觀。
	UndoPoly(player *world.PlayerInfo)
	// UsePolyScroll 處理變身卷軸使用。monsterName="" 表示取消變身。
	UsePolyScroll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, monsterName string)
	// UsePolySkill 處理變形術技能選擇對話框結果。
	UsePolySkill(sess *net.Session, player *world.PlayerInfo, monsterName string)
}

// PvPManager 處理 PvP 戰鬥邏輯（PvP 攻擊、粉紅名、善惡值、PK 擊殺）。由 system.PvPSystem 實作。
type PvPManager interface {
	// HandlePvPAttack 處理近戰 PvP 攻擊。
	HandlePvPAttack(attacker, target *world.PlayerInfo)
	// HandlePvPFarAttack 處理遠程 PvP 攻擊。
	HandlePvPFarAttack(attacker, target *world.PlayerInfo)
	// AddLawfulFromNpc 根據 NPC 善惡值增加擊殺者善惡值。
	AddLawfulFromNpc(killer *world.PlayerInfo, npcLawful int32)
}

// MailManager 處理信件邏輯（讀取/寫入/刪除/搬移）。由 system.MailSystem 實作。
type MailManager interface {
	// OpenMailbox 載入並發送信件列表。
	OpenMailbox(sess *net.Session, player *world.PlayerInfo, mailType int16)
	// ReadMail 讀取信件內容並標記已讀。
	ReadMail(sess *net.Session, player *world.PlayerInfo, mailID int32, mailType int16)
	// SendMail 寄出一封一般信件。
	SendMail(sess *net.Session, player *world.PlayerInfo, receiverName string, rawText []byte)
	// DeleteMail 刪除單封信件。
	DeleteMail(sess *net.Session, player *world.PlayerInfo, mailID int32, subtype byte)
	// MoveToStorage 搬移信件至保管箱。
	MoveToStorage(sess *net.Session, player *world.PlayerInfo, mailID int32, subtype byte)
	// BulkDelete 批次刪除信件。
	BulkDelete(sess *net.Session, player *world.PlayerInfo, subtype byte, mailIDs []int32)
}

// ShopManager 處理 NPC 商店交易邏輯（購買/販賣）。由 system.ShopSystem 實作。
type ShopManager interface {
	// BuyFromNpc 處理玩家從 NPC 購買物品。
	BuyFromNpc(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, shop *data.Shop)
	// SellToNpc 處理玩家向 NPC 販賣物品。
	SellToNpc(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, shop *data.Shop)
}

// CraftManager 處理 NPC 製作邏輯（材料驗證、消耗、生產）。由 system.CraftSystem 實作。
type CraftManager interface {
	// HandleCraftEntry 製作入口：檢查材料、顯示批量對話或執行製作。
	HandleCraftEntry(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, recipe *data.CraftRecipe, action string)
	// ExecuteCraft 執行製作：驗證材料、消耗、生產物品。
	ExecuteCraft(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, recipe *data.CraftRecipe, amount int32)
}

// PetLifecycleManager 處理寵物生命週期邏輯（召喚/收回/解放/死亡/經驗/指令）。由 system.PetSystem 實作。
type PetLifecycleManager interface {
	// UsePetCollar 使用寵物項圈召喚寵物（或收回已召喚的寵物）。
	UsePetCollar(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem)
	// HandlePetAction 處理寵物控制指令（攻擊/防禦/待機/解放等）。
	HandlePetAction(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, action string)
	// HandlePetNameChange 處理寵物改名。
	HandlePetNameChange(sess *net.Session, player *world.PlayerInfo, petID int32, newName string)
	// DismissPet 解放寵物（轉為野生 NPC）。
	DismissPet(pet *world.PetInfo, player *world.PlayerInfo)
	// CollectPet 收回寵物至項圈（儲存 DB）。
	CollectPet(pet *world.PetInfo, player *world.PlayerInfo)
	// PetDie 處理寵物死亡（經驗懲罰、動畫）。
	PetDie(pet *world.PetInfo)
	// AddPetExp 增加寵物經驗值並處理升級。
	AddPetExp(pet *world.PetInfo, expGain int32)
	// PetExpPercent 計算寵物經驗百分比（0-100）。
	PetExpPercent(pet *world.PetInfo) int
	// CalcUsedPetCost 計算玩家已使用的寵物/召喚獸 CHA 消耗。
	CalcUsedPetCost(charID int32) int
	// GiveToPet 處理給予寵物物品（裝備/進化）。
	GiveToPet(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, invItem *world.InvItem)
	// TameNpc 處理馴服野生 NPC 為寵物。
	TameNpc(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo)
	// UsePetItem 處理寵物裝備穿脫。
	UsePetItem(sess *net.Session, pet *world.PetInfo, listNo int)
}

// DollManager 處理魔法娃娃召喚/解散/屬性加成。由 system.DollSystem 實作。
type DollManager interface {
	// UseDoll 處理使用魔法娃娃物品（召喚或收回）。
	UseDoll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, dollDef *data.DollDef)
	// DismissDoll 解散魔法娃娃（還原加成、移除、廣播）。
	DismissDoll(doll *world.DollInfo, player *world.PlayerInfo)
	// RemoveDollBonuses 僅還原娃娃屬性加成（不移除世界實體）。
	RemoveDollBonuses(player *world.PlayerInfo, doll *world.DollInfo)
}

// ItemGroundManager 處理物品地面操作（銷毀、掉落、撿取）。由 system.ItemGroundSystem 實作。
type ItemGroundManager interface {
	// DestroyItem 銷毀背包中的物品。
	DestroyItem(sess *net.Session, player *world.PlayerInfo, objectID, count int32)
	// DropItem 將物品掉落至地面。
	DropItem(sess *net.Session, player *world.PlayerInfo, objectID, count int32)
	// PickupItem 從地面撿取物品。
	PickupItem(sess *net.Session, player *world.PlayerInfo, objectID int32)
}

// WarehouseManager 處理倉庫邏輯（存入/領出、DB 操作、血盟鎖定）。由 system.WarehouseSystem 實作。
type WarehouseManager interface {
	// OpenWarehouse 載入倉庫並發送物品列表。
	OpenWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16)
	// OpenWarehouseDeposit 開啟倉庫存入介面（與 OpenWarehouse 相同，客戶端內建 tab）。
	OpenWarehouseDeposit(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16)
	// OpenClanWarehouse 開啟血盟倉庫（含權限驗證+單人鎖定）。
	OpenClanWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32)
	// HandleWarehouseOp 處理倉庫存入/領出操作。
	HandleWarehouseOp(sess *net.Session, r *packet.Reader, resultType byte, count int, player *world.PlayerInfo)
	// SendClanWarehouseHistory 發送血盟倉庫歷史記錄。
	SendClanWarehouseHistory(sess *net.Session, clanID int32)
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
	// UseConsumable 處理消耗品使用（藥水、食物）。回傳 true 表示已消耗。
	UseConsumable(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) bool
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

// RankingChecker 提供英雄排名查詢。由 system.RankingSystem 實作。
type RankingChecker interface {
	// IsHero 檢查玩家是否在英雄排名中（TOP10 或任一職業 TOP3）。
	IsHero(name string) bool
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
	Death         DeathManager // filled after DeathSystem is created
	Trade         TradeManager // filled after TradeSystem is created
	Party         PartyManager // filled after PartySystem is created
	Clan          ClanManager  // filled after ClanSystem is created
	Summon        SummonManager    // filled after SummonSystem is created
	Polymorph     PolymorphManager // filled after PolymorphSystem is created
	Equip         EquipManager      // filled after EquipSystem is created
	ItemUse       ItemUseManager    // filled after ItemUseSystem is created
	Mail          MailManager        // filled after MailSystem is created
	Warehouse     WarehouseManager  // filled after WarehouseSystem is created
	PvP           PvPManager        // filled after PvPSystem is created
	Shop          ShopManager       // filled after ShopSystem is created
	Craft         CraftManager      // filled after CraftSystem is created
	ItemGround    ItemGroundManager    // filled after ItemGroundSystem is created
	PetLife       PetLifecycleManager // filled after PetSystem is created
	DollMgr       DollManager         // filled after DollSystem is created
	Bus           *event.Bus  // event bus for emitting game events (EntityKilled, etc.)
	WeaponSkills  *data.WeaponSkillTable
	Ranking       RankingChecker // filled after RankingSystem is created
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
	// C_CommonClick (opcode 16) — 客戶端收到 LOGOUT 後自動發送，請求角色列表。
	// Java: C_CommonClick.java — 回應 S_CharAmount + S_CharPacks。
	reg.Register(packet.C_OPCODE_COMMON_CLICK, authStates,
		func(sess any, r *packet.Reader) {
			sendCharacterList(sess.(*net.Session), deps)
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
	reg.Register(packet.C_OPCODE_CHAR_RESET, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleCharReset(sess.(*net.Session), r, deps)
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
	// 倉庫密碼（Java: C_Password — 密碼設定/變更/驗證後開倉）
	reg.Register(packet.C_OPCODE_WAREHOUSE_CONTROL, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleWarehousePassword(sess.(*net.Session), r, deps)
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
	// C_FIX (118) = C_FixWeaponList in Java — 武器修理列表查詢。
	// 注意：opcode 254 在 Java 中是 C_Windows（書籤排序、地圖計時等），不是武器修理！
	reg.Register(packet.C_OPCODE_FIX, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleFixWeaponList(sess.(*net.Session), r, deps)
		},
	)
	// C_WINDOWS (254) = C_Windows in Java — 書籤排序、地圖計時器等客戶端初始化請求。
	// 客戶端登入後自動發送。目前忽略（未實作）。
	reg.Register(packet.C_OPCODE_FIXABLE_ITEM, inWorldStates,
		func(sess any, r *packet.Reader) {
			// TODO: 實作 C_Windows 處理器（書籤排序、地圖計時等）
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
