package handler

import (
	"time"

	"github.com/l1jgo/server/internal/config"
	"github.com/l1jgo/server/internal/core/event"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/dialog"
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
	// ExecuteRangedAttackOnNpc 同步執行一次完整的弓箭物理攻擊（不經佇列）。
	// 對齊 Java `cha.onAction(srcpc)` 走 L1AttackPc 弓箭流程：消耗箭矢、命中/傷害骰、武器效果觸發、廣播、扣血、仇恨。
	// 供三重矢（skill 132）等需要在當前 tick 內連發多次完整攻擊的技能呼叫。
	ExecuteRangedAttackOnNpc(player *world.PlayerInfo, npcID int32)
}

// SkillRequest is queued by the handler and processed by SkillSystem in Phase 2.
type SkillRequest struct {
	SessionID  uint64
	SkillID    int32
	TargetID   int32
	TargetX    int32
	TargetY    int32
	MapID      int32
	BookmarkID int32
	SummonID   int32
	TargetName string
	Text       string
}

// SkillManager 處理技能執行、buff 管理、buff 計時。由 system.SkillSystem 實作。
type SkillManager interface {
	// QueueSkill 將技能請求排入佇列（Phase 2 處理）。
	QueueSkill(req SkillRequest)
	// CancelAllBuffs 移除目標所有可取消的 buff（Cancellation 效果）。
	CancelAllBuffs(target *world.PlayerInfo)
	// ClearAllBuffsOnDeath 死亡時清除所有 buff（含不可取消的）。
	ClearAllBuffsOnDeath(target *world.PlayerInfo)
	// GMClearAllStatuses GM 強制清除所有狀態：全部 buff + 中毒 + 詛咒 + 控制狀態 + 客戶端通知。
	GMClearAllStatuses(target *world.PlayerInfo)
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
	// RevertBuffStats 還原 buff 的所有屬性修改。
	RevertBuffStats(target *world.PlayerInfo, buff *world.ActiveBuff)
	// ConsumeSkillResources 扣除 MP/HP/材料並設定冷卻。
	ConsumeSkillResources(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo)
	// ApplyBuffStats 套用 buff 的所有屬性加成（靜默，不發送封包）。供登入恢復 buff 使用。
	ApplyBuffStats(player *world.PlayerInfo, buff *world.ActiveBuff)
	// ApplyJoyOfPainBacklash 觸發疼痛的歡愉反傷（攻擊者已持有 buff 218 時對攻擊者扣 HP 並消耗 buff）。
	// Java: L1PcInstance.receiveDamage:2737-2773 對所有 PC→PC 傷害源觸發；Go 既有 skill 路徑已接，
	// 此介面方法供 pvp.go melee/ranged 等通用傷害路徑共用。
	ApplyJoyOfPainBacklash(attacker, target *world.PlayerInfo, nearby []*world.PlayerInfo)
}

// DeathManager 處理玩家死亡與重生。由 system.DeathSystem 實作。
type DeathManager interface {
	// KillPlayer 處理玩家死亡（動畫、經驗懲罰、清 buff）。
	KillPlayer(player *world.PlayerInfo)
	// ProcessRestart 處理死亡重生（回城、重建 Known）。
	ProcessRestart(sess *net.Session, player *world.PlayerInfo)
	// ClearPlayerTomb 清除玩家死亡時生成的墓碑。
	ClearPlayerTomb(player *world.PlayerInfo)
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
	// HealMember 處理血盟飽食度 HP 回復。
	HealMember(sess *net.Session, player *world.PlayerInfo, addHP int32)
	// UploadEmblem 上傳盟徽。
	UploadEmblem(sess *net.Session, player *world.PlayerInfo, emblemData []byte)
	// DownloadEmblem 下載盟徽。
	DownloadEmblem(sess *net.Session, emblemID int32)
}

// SummonManager 處理召喚技能邏輯（召喚/馴服/殭屍/歸返自然）。由 system.SummonSystem 實作。
type SummonManager interface {
	// ExecuteSummonMonster 處理技能 51 召喚怪物。
	ExecuteSummonMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32)
	// ExecuteElementalSummon 處理技能 154/162 召喚屬性精靈。
	ExecuteElementalSummon(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo)
	// ExecuteTamingMonster 處理技能 36 馴服怪物。
	ExecuteTamingMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32)
	// ExecuteCreateZombie 處理技能 41 創造殭屍。
	ExecuteCreateZombie(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32)
	// ExecuteReturnToNature 處理技能 145 歸返自然。
	ExecuteReturnToNature(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32)
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
	// UseDirectPolyScroll 處理使用後不開清單、直接變身的特殊卷軸。
	UseDirectPolyScroll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem)
	// UsePolySkill 處理變形術技能選擇對話框結果。
	UsePolySkill(sess *net.Session, player *world.PlayerInfo, monsterName string)
}

// PvPManager 處理 PvP 戰鬥邏輯（PvP 攻擊、粉紅名、善惡值、PK 擊殺）。由 system.PvPSystem 實作。
type PvPManager interface {
	// HandlePvPAttack 處理近戰 PvP 攻擊。
	HandlePvPAttack(attacker, target *world.PlayerInfo)
	// HandlePvPFarAttack 處理遠程 PvP 攻擊。
	HandlePvPFarAttack(attacker, target *world.PlayerInfo)
	// TriggerPinkName 依 Java L1PinkName.onAction 觸發粉紅名判定。
	TriggerPinkName(attacker, target *world.PlayerInfo)
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
	// BuyFromNpc 處理玩家從 NPC 購買物品。npc 用於稅金計算（城堡/城鎮歸屬）。
	BuyFromNpc(sess *net.Session, r *packet.Reader, count int, player *world.PlayerInfo, shop *data.Shop, npc *world.NpcInfo)
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

// HauntedHouseManager 鬼屋副本管理器。由 system.HauntedHouseSystem 實作。
type HauntedHouseManager interface {
	// AddMember 嘗試讓玩家加入鬼屋副本。
	AddMember(sess *net.Session, player *world.PlayerInfo)
	// OnGoalReached 處理玩家觸碰終點鬼火（NPC 81171）。
	OnGoalReached(sess *net.Session, player *world.PlayerInfo)
	// RemoveOnDisconnect 玩家斷線時移除。
	RemoveOnDisconnect(player *world.PlayerInfo)
	// GiveReward 給予鬼屋獎品。
	GiveReward(sess *net.Session, player *world.PlayerInfo)
}

// DragonDoorManager 龍門系統管理器。由 system.DragonDoorSystem 實作。
type DragonDoorManager interface {
	// GetAvailableCounts 取得各類型門衛可用名額（安塔瑞斯、法利昂、林德拜爾）。
	GetAvailableCounts() (a, b, c int)
	// SpawnKeeper 在玩家位置生成指定類型的門衛 NPC。
	SpawnKeeper(sess *net.Session, player *world.PlayerInfo, npcID int32)
}

// QuestWorldManager 任務副本世界管理器（MISS-P0-003）。由 system.QuestWorldSystem 實作。
// 對應 Java WorldQuest 單例。
type QuestWorldManager interface {
	// Enter 建立新副本實例並讓玩家進入；回傳新建的實例，找不到副本定義回 nil。
	Enter(player *world.PlayerInfo, dungeonID int32) *world.QuestInstance
	// Exit 玩家退出當前所在副本；回傳是否實際從副本移出。
	Exit(player *world.PlayerInfo) bool
	// RemoveOnDisconnect 玩家斷線時從副本移出（若該玩家正在副本內）。
	RemoveOnDisconnect(player *world.PlayerInfo)
	// OnNpcDeath 副本內 NPC 死亡時觸發；用於更新 round 進度與觸發 on_round_clear。
	OnNpcDeath(npc *world.NpcInfo)
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

// HierarchManager 處理隨身祭司召喚/解散。由 system.HierarchSystem 實作。
type HierarchManager interface {
	// UseHierarch 處理使用隨身祭司物品（召喚或收回）。
	UseHierarch(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, def *data.HierarchDef)
}

// PetMatchManager 處理寵物比賽系統（報名/比賽/獎勵）。由 system.PetMatchSystem 實作。
type PetMatchManager interface {
	// EnterPetMatch 報名寵物比賽：驗證 → 召喚寵物 → 傳送到競技場。
	EnterPetMatch(sess *net.Session, player *world.PlayerInfo, amuletObjID int32) bool
	// TickPetMatches 每 tick 呼叫：檢查就緒狀態、比賽進行、超時判定。
	TickPetMatches()
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
	// UseResurrectionScroll 處理復活卷軸使用。
	UseResurrectionScroll(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, targetObjID int32) bool
	// UseBlankMagicScroll 處理空白魔法卷軸寫入技能。
	UseBlankMagicScroll(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, selectedSkillIndex int) bool
	// UseMagicScroll 處理封入魔法的卷軸施放。
	UseMagicScroll(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, itemInfo *data.ItemInfo, targetObjID int32, targetX, targetY int16) bool
	// UseDissolution 處理溶解劑。
	UseDissolution(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, targetObjID int32) bool
	// UseWhetstone 處理磨刀石修復武器/防具耐久。
	UseWhetstone(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, targetObjID int32) bool
	// UseTeleportScroll 處理傳送卷軸使用。
	UseTeleportScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, item *world.InvItem)
	// UseHomeScroll 處理回家卷軸使用。
	UseHomeScroll(sess *net.Session, player *world.PlayerInfo, item *world.InvItem)
	// UseWand 處理魔杖使用（充能扣減、效果觸發）。
	// targetObjID/targetX/targetY 僅在 spell_long 類魔杖（烏木、楓木）時有值。
	UseWand(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem,
		targetObjID int32, targetX, targetY int16)
	// UseFixedTeleportScroll 處理指定傳送卷軸使用。
	UseFixedTeleportScroll(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, itemInfo *data.ItemInfo)
	// GiveDrops 為擊殺的 NPC 擲骰掉落物品（支援自動分配隊伍）。
	GiveDrops(killer *world.PlayerInfo, npc *world.NpcInfo)
	// ApplyHaste 套用加速效果。
	ApplyHaste(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32)
	// BroadcastEffect 向自己和附近玩家廣播特效。
	BroadcastEffect(sess *net.Session, player *world.PlayerInfo, gfxID int32)
	// ConsumeBoxItem 消耗 1 個寶箱物品。
	ConsumeBoxItem(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem)
	// GiveBoxReward 給予開箱獎勵物品。
	GiveBoxReward(sess *net.Session, player *world.PlayerInfo, getItemID int32, minCount, maxCount int32, bless, enchant int8, broadcast bool)
	// ActivateVIP 啟用 VIP 物品效果（同 type 互斥）。
	ActivateVIP(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, vip *data.ItemVIP)
}

// RankingEntry 排名項目。
type RankingEntry struct {
	Name  string
	Value int64
}

// RankingChecker 提供四大排名查詢。由 system.RankingSystem 實作。
type RankingChecker interface {
	// IsHero 檢查玩家是否在英雄排名中（TOP10 或任一職業 TOP3）。
	IsHero(name string) bool
	// GetHeroAll 全職業 TOP10。
	GetHeroAll() []RankingEntry
	// GetHeroClass 指定職業 TOP3（0=王族..7=戰士）。
	GetHeroClass(classType int) []RankingEntry
	// GetClanRanking 血盟 TOP10。
	GetClanRanking() []RankingEntry
	// GetKillRanking 擊殺 TOP10。
	GetKillRanking() []RankingEntry
	// GetDeathRanking 死亡 TOP10。
	GetDeathRanking() []RankingEntry
	// GetWealthRanking 財富 TOP10。
	GetWealthRanking() []RankingEntry
}

// NpcServiceManager 處理 NPC 服務邏輯（治療、附魔、變身、傳送、升級）。由 system.NpcServiceSystem 實作。
type NpcServiceManager interface {
	// NpcFullHeal 處理 NPC 完整治療。
	NpcFullHeal(sess *net.Session, player *world.PlayerInfo, npcID int32)
	// NpcWeaponEnchant 處理 NPC 武器附魔。
	NpcWeaponEnchant(sess *net.Session, player *world.PlayerInfo)
	// NpcArmorEnchant 處理 NPC 防具附魔。
	NpcArmorEnchant(sess *net.Session, player *world.PlayerInfo)
	// NpcPoly 處理 NPC 變身服務。
	NpcPoly(sess *net.Session, player *world.PlayerInfo, polyID int32)
	// NpcTeleportWithCost 處理 NPC 傳送（扣費 + 傳送）。
	NpcTeleportWithCost(sess *net.Session, player *world.PlayerInfo, dest *data.TeleportDest, objID int32)
	// NpcUpgrade 處理物品升級合成。
	NpcUpgrade(sess *net.Session, player *world.PlayerInfo, upg *data.ItemUpgrade)
	// ConsumeAdena 扣除玩家金幣並發送更新封包。
	ConsumeAdena(sess *net.Session, player *world.PlayerInfo, amount int32) bool
	// RepairWeapon 處理武器修理（扣費 + 修復耐久度）。
	RepairWeapon(sess *net.Session, player *world.PlayerInfo, weapon *world.InvItem, cost int32) bool
	// ConsumeItem 消耗背包物品（移除 + 發送更新 + 標記 dirty）。
	ConsumeItem(sess *net.Session, player *world.PlayerInfo, objectID int32, count int32) bool
	// Refine 火神精煉分解（移除裝備 + 給予結晶體）。
	Refine(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, crystalItemID int32, crystalCount int32)
	// FireSmithCraft 火神合成（消耗結晶體+契約+催化材料 → 給予成品）。
	// plusItemObjID/plusItemCount 為客戶端 SmithUI plus 槽拖入的火神之槌/淚 objID 與數量。
	FireSmithCraft(sess *net.Session, player *world.PlayerInfo, recipe *data.FireSmithRecipe, plusItemObjID int32, plusItemCount int32)
}

// ShopCnManager 處理天寶幣商城交易邏輯。由 system.ShopCnSystem 實作。
type ShopCnManager interface {
	// BuyCnItem 購買天寶幣商城物品（扣幣+給物品）。
	BuyCnItem(sess *net.Session, player *world.PlayerInfo, cnItem *data.ShopCnItem, buyCount, actualCount int32)
	// SellCnItem 回收物品換天寶幣（移除物品+給幣）。
	SellCnItem(sess *net.Session, player *world.PlayerInfo, item *world.InvItem, sellCount, recyclePrice int32)
}

// ItemCreateManager 處理統一給物品流程。由 system.ItemCreateSystem 實作。
type ItemCreateManager interface {
	// GiveItem 給玩家物品，處理堆疊、背包格數、負重與封包通知。
	GiveItem(sess *net.Session, player *world.PlayerInfo, itemID, count int32) (*world.InvItem, bool)
}

// PowerItemManager 處理強化物品購買邏輯。由 system.PowerItemSystem 實作。
type PowerItemManager interface {
	// BuyPowerItem 購買強化物品（扣金幣+給物品含屬性）。
	BuyPowerItem(sess *net.Session, player *world.PlayerInfo, pItem *data.PowerShopItem)
}

// SpellShopManager 處理魔法商店購買邏輯。由 system.SpellShopSystem 實作。
type SpellShopManager interface {
	// BuySpells 購買並學習魔法（扣金幣+學習技能+特效）。
	BuySpells(sess *net.Session, player *world.PlayerInfo, validSpells []*data.SkillInfo, totalCost int32)
}

// GMCommandManager 處理 GM 命令的角色狀態修改。由 system.GMCommandSystem 實作。
type GMCommandManager interface {
	// SetLevel 設定玩家等級（含經驗值、HP/MP 重算）。
	SetLevel(sess *net.Session, player *world.PlayerInfo, level int)
	// SetHP 設定玩家 HP（含死亡復活處理）。
	SetHP(sess *net.Session, player *world.PlayerInfo, hp int)
	// SetMP 設定玩家 MP。
	SetMP(sess *net.Session, player *world.PlayerInfo, mp int)
	// FullHeal 補滿 HP/MP（含死亡復活處理）。
	FullHeal(sess *net.Session, player *world.PlayerInfo)
	// SetStat 設定指定屬性值。
	SetStat(sess *net.Session, player *world.PlayerInfo, stat string, value int16)
	// GiveItem 給予物品。
	GiveItem(sess *net.Session, player *world.PlayerInfo, itemID, count int32, enchant int8)
	// GiveGold 給予金幣。
	GiveGold(sess *net.Session, player *world.PlayerInfo, amount int32)
	// AdjustLawful 以 signed delta 調整玩家正義值，並同步 S_Lawful。
	AdjustLawful(sess *net.Session, player *world.PlayerInfo, delta int32)
	// ApplyPoison GM 強制施加中毒（1=傷害毒、2=沉默毒/卡司特毒、3=麻痺毒延遲）。回傳是否成功（已中毒或未知類型回傳 false）。
	ApplyPoison(player *world.PlayerInfo, ptype byte) bool
	// BreakWeapon GM 強制將玩家當前裝備武器的耐久損壞值設為 amount（1-127）。回傳武器名稱與是否成功（無裝備武器回傳 false）。
	BreakWeapon(player *world.PlayerInfo, amount int8) (string, bool)
}

// PrivateShopManager 處理個人商店交易邏輯。由 system.PrivateShopSystem 實作。
type PrivateShopManager interface {
	// TransferItem 從來源玩家背包移動物品到目標玩家背包。
	TransferItem(from, to *world.PlayerInfo, item *world.InvItem, count int32)
	// TransferGold 轉移金幣。
	TransferGold(from, to *world.PlayerInfo, amount int32)
	// SetupShop 開設個人商店（設定出售/收購清單 + 廣播擺攤動作）。
	SetupShop(player *world.PlayerInfo, sellList []*world.PrivateShopSell, buyList []*world.PrivateShopBuy, shopChat []byte)
	// CloseShop 關閉個人商店（清除狀態 + 廣播取消動作）。
	CloseShop(player *world.PlayerInfo)
	// CancelShopNotTradable 因不可交易物品取消商店設置。
	CancelShopNotTradable(player *world.PlayerInfo)
	// ExecuteBuy 執行從個人商店購買物品（業務驗證 + 物品/金幣轉移 + 售完清理）。
	ExecuteBuy(buyer *world.PlayerInfo, shopPlayer *world.PlayerInfo, orders []ShopBuyOrder)
	// ExecuteSell 執行向個人商店出售物品（業務驗證 + 物品/金幣轉移 + 收購完成清理）。
	ExecuteSell(seller *world.PlayerInfo, shopPlayer *world.PlayerInfo, orders []ShopSellOrder)
}

// ShopBuyOrder 從個人商店購買的單筆訂單（封包解析結果）。
type ShopBuyOrder struct {
	Order int   // 商品索引
	Count int32 // 購買數量
}

// ShopSellOrder 向個人商店出售的單筆訂單（封包解析結果）。
type ShopSellOrder struct {
	ItemObjID int32 // 賣方物品 ObjectID
	Count     int32 // 出售數量
	Order     int   // 收購清單索引
}

// FishingManager 處理釣魚邏輯（開始/結束/tick）。由 system.FishingSystem 實作。
type FishingManager interface {
	// StartFishing 開始釣魚（設定狀態 + 廣播動作）。
	StartFishing(player *world.PlayerInfo, item *world.InvItem)
	// StopFishing 結束釣魚狀態。
	StopFishing(player *world.PlayerInfo, sendMsg bool)
	// Tick 每 tick 更新釣魚計時器。
	Tick(player *world.PlayerInfo)
}

// MapTimerManager 處理限時地圖計時邏輯。由 system.MapTimerSystem 實作。
type MapTimerManager interface {
	// OnEnterTimedMap 玩家進入限時地圖時呼叫。
	OnEnterTimedMap(player *world.PlayerInfo, mapID int16)
	// TickMapTimer 每秒遞減計時，回傳 true 表示時間到。
	TickMapTimer(player *world.PlayerInfo) bool
	// ResetAllMapTimers 重置玩家所有限時地圖時間。
	ResetAllMapTimers(player *world.PlayerInfo)
}

// InnManager 處理旅館租房/退租邏輯。由 system.InnSystem 實作。
type InnManager interface {
	// ReturnRoom 處理退租。
	ReturnRoom(sess *net.Session, player *world.PlayerInfo, npcObjID, npcID int32)
	// RentRoom 處理租房。
	RentRoom(sess *net.Session, player *world.PlayerInfo, npcObjID, npcID int32, amount int32)
}

// MarriageManager 處理結婚/離婚邏輯。由 system.MarriageSystem 實作。
type MarriageManager interface {
	// AcceptProposal 處理求婚接受。
	AcceptProposal(sess *net.Session, player *world.PlayerInfo, proposerID int32, accepted bool)
	// ConfirmDivorce 處理離婚確認。
	ConfirmDivorce(sess *net.Session, player *world.PlayerInfo, accepted bool)
}

// StatAllocManager 處理角色屬性配點邏輯。由 system.StatAllocSystem 實作。
type StatAllocManager interface {
	// AllocStat 分配一個屬性點。
	AllocStat(sess *net.Session, player *world.PlayerInfo, statName string)
}

// CharResetManager 處理角色重置（洗點）邏輯。由 system.CharResetSystem 實作。
type CharResetManager interface {
	// Start 啟動角色重置流程。
	Start(sess *net.Session, player *world.PlayerInfo)
	// ResetStage1 處理初始屬性選擇。
	ResetStage1(sess *net.Session, player *world.PlayerInfo, str, intel, wis, dex, con, cha int16)
	// ResetStage2 處理逐級升級。
	ResetStage2(sess *net.Session, player *world.PlayerInfo, type2 byte)
	// ResetStage2Finish 處理 stage2 完成時的最後屬性加點。
	ResetStage2Finish(sess *net.Session, player *world.PlayerInfo, lastAttr byte)
	// ResetStage3 處理萬能藥屬性覆寫。
	ResetStage3(sess *net.Session, player *world.PlayerInfo, str, intel, wis, dex, con, cha int16)
}

// QuestActionHandler 處理任務 NPC 動作邏輯（驗證/消耗/獎勵/步驟推進）。由 system.QuestSystem 實作。
type QuestActionHandler interface {
	// ExecuteQuestAction 執行任務動作（驗證條件 → 消耗道具 → 給予獎勵 → 推進步驟）。
	ExecuteQuestAction(sess *net.Session, player *world.PlayerInfo, objID int32, npcID int32, action string) bool
}

// CastleManager 處理城堡管理邏輯（稅率/寶庫/攻城戰排程）。由 system.CastleSystem 實作。
type CastleManager interface {
	// GetCastle 取得城堡運行時狀態。
	GetCastle(castleID int32) *CastleInfo
	// GetCastleByOwnerClan 依城主公會 ID 取得城堡（0=無城堡）。
	GetCastleByOwnerClan(clanID int32) *CastleInfo
	// SetTaxRate 設定城堡稅率（10-50）。
	SetTaxRate(sess *net.Session, player *world.PlayerInfo, castleID int32, rate int32)
	// Deposit 存入金幣到城堡寶庫。
	Deposit(sess *net.Session, player *world.PlayerInfo, castleID int32, amount int32)
	// Withdraw 從城堡寶庫領出金幣。
	Withdraw(sess *net.Session, player *world.PlayerInfo, castleID int32, amount int32)
	// GetTaxRate 取得城堡當前稅率（緩存值）。
	GetTaxRate(castleID int32) int32
	// GetCastleIDByNpcLocation 依 NPC 座標查詢所屬城堡 ID。
	GetCastleIDByNpcLocation(x, y int32, mapID int16) int32
	// IsWarNow 指定城堡是否在攻城戰中。
	IsWarNow(castleID int32) bool
	// IsAnyWarNow 是否有任何城堡在攻城戰中。
	IsAnyWarNow() bool
	// CheckInWarArea 檢查座標是否在攻城戰區域中。
	CheckInWarArea(castleID int32, x, y int32, mapID int16) bool
	// TickWar 攻城戰排程 tick（由主迴圈每 tick 呼叫）。
	TickWar()
	// AddPublicMoney 增加城堡寶庫金額（用於稅收自動存入）。
	AddPublicMoney(castleID int32, amount int64)
	// TransferCastle 轉移城堡主權（攻城勝利時呼叫）。
	TransferCastle(castleID int32, newClanID int32)
	// OnTowerDeath 守護塔被摧毀時呼叫：在塔座標生成王冠。
	OnTowerDeath(npc *world.NpcInfo)
	// HandleCrownClick 玩家點擊王冠：城堡主權轉移。
	HandleCrownClick(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo)
	// CanDamageTower 檢查是否可以攻擊守護塔（必須在攻城戰中且宣戰方）。
	CanDamageTower(player *world.PlayerInfo, npc *world.NpcInfo) bool
	// CanDamageCatapult 檢查是否可以攻擊投石車（必須在攻城戰中且宣戰方）。
	CanDamageCatapult(player *world.PlayerInfo, npc *world.NpcInfo) bool
	// SpawnWarFlags 攻城戰開始時生成戰爭旗。
	SpawnWarFlags(castleID int32)
	// ClearWarFlags 攻城戰結束時清除戰爭旗。
	ClearWarFlags(castleID int32)
	// SpawnCatapults 生成城堡投石車。
	SpawnCatapults(castleID int32)
	// ClearCatapults 清除城堡投石車。
	ClearCatapults(castleID int32)
	// HandleCatapultAction 投石車砲彈發射。
	HandleCatapultAction(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, action string)
	// IsCatapultAttacker 判斷投石車是否為攻擊方。
	IsCatapultAttacker(npcID int32) bool
}

// CastleInfo 城堡運行時狀態。
type CastleInfo struct {
	CastleID    int32
	Name        string
	TaxRate     int32
	PublicMoney int64
	WarTime     time.Time
	OwnerClanID int32
	IsWar       bool
}

// WarManager 處理戰爭邏輯（宣戰/投降/休戰/攻城勝利）。由 system.WarSystem 實作。
type WarManager interface {
	// DeclareWar 宣戰。
	DeclareWar(sess *net.Session, player *world.PlayerInfo, targetClanName string)
	// SurrenderWar 投降。
	SurrenderWar(sess *net.Session, player *world.PlayerInfo, targetClanName string)
	// CeaseWar 休戰。
	CeaseWar(sess *net.Session, player *world.PlayerInfo, targetClanName string)
	// WinCastleWar 攻城勝利（王冠被取得時呼叫）。
	WinCastleWar(winnerClanName string, castleID int32)
	// CeaseCastleWar 攻城戰時間到（防禦方勝利）。
	CeaseCastleWar(castleID int32)
	// IsWar 兩個公會是否在戰爭中。
	IsWar(clan1, clan2 string) bool
	// IsClanInWar 公會是否在任何戰爭中。
	IsClanInWar(clanName string) bool
	// GetActiveWars 取得所有進行中的戰爭。
	GetActiveWars() []*ActiveWar
	// CheckCastleWar 登入時通知攻城戰狀態。
	CheckCastleWar(sess *net.Session)
}

// ActiveWar 進行中的戰爭狀態。
type ActiveWar struct {
	WarType     int // 1=攻城, 2=模擬戰, 3=血盟決鬥
	DefenceClan string
	AttackClans map[string]bool
	CastleID    int32
	StartTime   time.Time
}

// TrapTriggerer 處理陷阱觸發邏輯（傷害/治療/中毒/技能/傳送）。由 system.TrapSystem 實作。
type TrapTriggerer interface {
	// TriggerTraps 處理玩家踩到陷阱的所有遊戲邏輯。
	TriggerTraps(sess *net.Session, player *world.PlayerInfo, traps []*world.TrapInstance)
	// DetectTraps 處理 DETECTION / COUNTER_DETECTION 的畫面內陷阱偵測。
	DetectTraps(sess *net.Session, player *world.PlayerInfo)
}

// Deps holds shared dependencies injected into all packet handlers.
type Deps struct {
	AccountRepo      *persist.AccountRepo
	CharRepo         *persist.CharacterRepo
	ItemRepo         *persist.ItemRepo
	Config           *config.Config
	Log              *zap.Logger
	World            *world.State
	Scripting        *scripting.Engine
	NpcActions       *data.NpcActionTable
	Items            *data.ItemTable
	Shops            *data.ShopTable
	Drops            *data.DropTable
	Teleports        *data.TeleportTable
	TeleportHtml     *data.TeleportHtmlTable
	Portals          *data.PortalTable
	RandomPortals    *data.RandomPortalTable
	Skills           *data.SkillTable
	Npcs             *data.NpcTable
	MobSkills        *data.MobSkillTable
	MapData          *data.MapDataTable
	Polys            *data.PolymorphTable
	ArmorSets        *data.ArmorSetTable
	ItemPowers       *data.ItemPowerTable // 物品強化加成（MISS-P1-005，L1ItemPower）
	SprTable         *data.SprTable
	WarehouseRepo    *persist.WarehouseRepo
	WALRepo          *persist.WALRepo
	ClanRepo         *persist.ClanRepo
	BuffRepo         *persist.BuffRepo
	Doors            *data.DoorTable
	ItemMaking       *data.ItemMakingTable
	SpellbookReqs    *data.SpellbookReqTable
	BuffIcons        *data.BuffIconTable
	NpcServices      *data.NpcServiceTable
	QuestRepo        *persist.QuestRepo
	BuddyRepo        *persist.BuddyRepo
	ExcludeRepo      *persist.ExcludeRepo
	BoardRepo        *persist.BoardRepo
	MailRepo         *persist.MailRepo
	PetRepo          *persist.PetRepo
	PetTypes         *data.PetTypeTable
	PetItems         *data.PetItemTable
	Dolls            *data.DollTable
	TeleportPages    *data.TeleportPageTable
	Combat           CombatQueue         // filled after CombatSystem is created
	Skill            SkillManager        // filled after SkillSystem is created
	Death            DeathManager        // filled after DeathSystem is created
	Trade            TradeManager        // filled after TradeSystem is created
	Party            PartyManager        // filled after PartySystem is created
	Clan             ClanManager         // filled after ClanSystem is created
	Summon           SummonManager       // filled after SummonSystem is created
	Polymorph        PolymorphManager    // filled after PolymorphSystem is created
	Equip            EquipManager        // filled after EquipSystem is created
	ItemUse          ItemUseManager      // filled after ItemUseSystem is created
	Mail             MailManager         // filled after MailSystem is created
	Warehouse        WarehouseManager    // filled after WarehouseSystem is created
	PvP              PvPManager          // filled after PvPSystem is created
	Shop             ShopManager         // filled after ShopSystem is created
	Craft            CraftManager        // filled after CraftSystem is created
	ItemGround       ItemGroundManager   // filled after ItemGroundSystem is created
	PetLife          PetLifecycleManager // filled after PetSystem is created
	DollMgr          DollManager         // filled after DollSystem is created
	HierarchMgr      HierarchManager     // filled after HierarchSystem is created
	HauntedHouse     HauntedHouseManager // filled after HauntedHouseSystem is created
	DragonDoor       DragonDoorManager   // filled after DragonDoorSystem is created
	QuestWorld       QuestWorldManager   // filled after QuestWorldSystem is created (MISS-P0-003)
	PetMatch         PetMatchManager     // filled after PetMatchSystem is created
	Bus              *event.Bus          // event bus for emitting game events (EntityKilled, etc.)
	WeaponSkills     *data.WeaponSkillTable
	FireCrystals     *data.FireCrystalTable
	FireSmithRecipes *data.FireSmithRecipeTable
	Resolvents       *data.ResolventTable
	Ranking          RankingChecker // filled after RankingSystem is created
	ItemBoxes        *data.ItemBoxTable
	ItemUpgrades     *data.ItemUpgradeTable
	ItemVIPs         *data.ItemVIPTable
	NpcChats         *data.NpcChatTable
	MobGroups        *data.MobGroupTable
	ShopCn           *data.ShopCnTable
	PowerItems       *data.PowerItemTable
	ItemCreate       ItemCreateManager // filled after ItemCreateSystem is created
	Hierarchs        *data.HierarchTable
	Auction          AuctionManager                       // filled after AuctionSystem is created
	Houses           *data.HouseTable                     // 住宅靜態座標資料
	HouseRepo        *persist.HouseRepo                   // 住宅動態狀態持久化
	InnRepo          *persist.InnRepo                     // 旅館房間持久化
	InnRooms         map[int32]map[int32]*persist.InnRoom // 旅館房間運行時狀態（npcID → roomNum → room）
	Alliances        *AllianceManager                     // 聯盟管理器（啟動時從 DB 載入）
	ClanMatching     *ClanMatchingManager                 // 血盟配對管理器
	QuestData        *data.QuestTable                     // 任務範本 + NPC 對話定義（YAML 載入）
	TrapMgr          *world.TrapManager                   // 陷阱管理器（座標觸發 + 重生）
	Trap             TrapTriggerer                        // 陷阱觸發邏輯（filled after TrapSystem is created）
	Quest            QuestActionHandler                   // 任務動作邏輯（filled after QuestSystem is created）
	NpcSvc           NpcServiceManager                    // NPC 服務邏輯（filled after NpcServiceSystem is created）
	CharReset        CharResetManager                     // 角色重置邏輯（filled after CharResetSystem is created）
	StatAlloc        StatAllocManager                     // 屬性配點邏輯（filled after StatAllocSystem is created）
	Marriage         MarriageManager                      // 結婚/離婚邏輯（filled after MarriageSystem is created）
	Inn              InnManager                           // 旅館租房/退租邏輯（filled after InnSystem is created）
	ShopCnMgr        ShopCnManager                        // 天寶幣商城（filled after ShopCnSystem is created）
	PowerItemMgr     PowerItemManager                     // 強化物品購買（filled after PowerItemSystem is created）
	SpellShopMgr     SpellShopManager                     // 魔法商店（filled after SpellShopSystem is created）
	GMCmd            GMCommandManager                     // GM 命令（filled after GMCommandSystem is created）
	PrivShop         PrivateShopManager                   // 個人商店交易（filled after PrivateShopSystem is created）
	Fishing          FishingManager                       // 釣魚邏輯（filled after FishingSystem is created）
	MapTimer         MapTimerManager                      // 限時地圖計時（filled after MapTimerSystem is created）
	Castles          *data.CastleTable                    // 城堡靜態地理資料
	WarGifts         *data.WarGiftTable                   // 攻城戰禮物資料
	CastleRepo       *persist.CastleRepo                  // 城堡動態狀態持久化
	Castle           CastleManager                        // 城堡管理邏輯（filled after CastleSystem is created）
	War              WarManager                           // 戰爭管理邏輯（filled after WarSystem is created）
	Dialogs          *dialog.Manager                      // 動態 HTML 對話註冊表（YAML+HTM 載入）
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
	// C_WINDOWS (254) = C_Windows in Java — 書籤排序、地圖計時器、龍門等。
	reg.Register(packet.C_OPCODE_WINDOWS, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleWindows(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_PERSONAL_SHOP, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleSelectList(sess.(*net.Session), r, deps)
		},
	)
	// 個人商店（擺攤）
	reg.Register(packet.C_OPCODE_SHOP, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleShop(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_QUERY_PERSONAL_SHOP, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleQueryPrivateShop(sess.(*net.Session), r, deps)
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

	// Castle treasury
	reg.Register(packet.C_OPCODE_TAX, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleTaxRate(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_DEPOSIT, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleCastleDeposit(sess.(*net.Session), r, deps)
		},
	)
	reg.Register(packet.C_OPCODE_WITHDRAW, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleCastleWithdraw(sess.(*net.Session), r, deps)
		},
	)

	// War（宣戰/投降/休戰）
	reg.Register(packet.C_OPCODE_WAR, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleWar(sess.(*net.Session), r, deps)
		},
	)

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

	// 結婚系統（Java: C_Propose, opcode 50）
	reg.Register(packet.C_OPCODE_MARRIAGE, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleMarriage(sess.(*net.Session), r, deps)
		},
	)

	// 血盟配對系統（Java: C_ClanMatching, opcode 76）
	reg.Register(packet.C_OPCODE_CLAN_MATCHING, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleClanMatching(sess.(*net.Session), r, deps)
		},
	)

	// 釣魚點擊（Java: C_FishClick, opcode 62 — 與 C_OPCODE_THROW 共用）
	reg.Register(packet.C_OPCODE_THROW, inWorldStates,
		func(sess any, r *packet.Reader) {
			HandleFishClick(sess.(*net.Session), r, deps)
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
