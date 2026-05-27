package world

import (
	"math/rand"
	"time"

	"github.com/l1jgo/server/internal/net"
)

// PlayerInfo holds in-memory data for a player currently in-world.
// Accessed only from the game loop goroutine — no locks needed.
type PlayerInfo struct {
	SessionID                 uint64
	Session                   *net.Session
	CharID                    int32 // DB ID, used as object ID in packets
	Name                      string
	X                         int32
	Y                         int32
	MapID                     int16
	Heading                   int16
	ClassID                   int32 // GFX
	Sex                       int16 // 0=male, 1=female
	Level                     int16
	Lawful                    int32
	Title                     string
	ClanID                    int32
	ClanName                  string
	ClanRank                  int16
	ClassType                 int16 // 0=Prince, 1=Knight, 2=Elf, 3=Wizard, 4=DarkElf, 5=DragonKnight, 6=Illusionist
	ElfAttr                   int16 // Java getElfAttr(): 0=無, 1=地, 2=火, 4=水, 8=風
	HP                        int32
	MaxHP                     int32
	MP                        int32
	MaxMP                     int32
	Str                       int16
	Dex                       int16
	Con                       int16
	Wis                       int16
	Intel                     int16
	Cha                       int16
	Exp                       int32 // cumulative total exp
	BonusStats                int16 // number of bonus stat points already allocated (level 51+)
	ElixirStats               int16 // 萬能藥使用次數（洗點時用於計算可分配點數）
	Speed                     byte  // 0=normal, 1=fast, etc.
	MoveSpeed                 byte  // 0=normal, 1=hasted (green potion), 2=slowed
	BraveSpeed                byte  // 0=none, 1=brave (attack speed), 3=elf brave
	HasteItemEquipped         int   // Java getHasteItemEquipped(): equipped item-based haste counter
	HasteTicks                int   // remaining ticks for haste buff (0 = expired)
	BraveTicks                int   // remaining ticks for brave buff (0 = expired)
	WisdomTicks               int   // remaining ticks for wisdom buff (0 = expired)
	WisdomSP                  int16 // SP bonus from wisdom potion (removed when buff expires)
	AC                        int16 // current AC (base 10 - equipment bonus; lower = better)
	MR                        int16 // magic resistance
	HitMod                    int16 // melee hit bonus from buffs
	DmgMod                    int16 // melee damage bonus from buffs
	BowHitMod                 int16 // bow hit bonus from buffs
	BowDmgMod                 int16 // bow damage bonus from buffs
	DollDmgAdd                int16 // 魔法娃娃機率增傷量（Yiwei: dmgAdd）
	DollDmgAddChance          int   // 魔法娃娃機率增傷機率百分比
	SP                        int16 // spell power bonus from buffs
	HPR                       int16 // HP regen bonus from buffs (per regen tick)
	MPR                       int16 // MP regen bonus from buffs (per regen tick)
	FireRes                   int16 // fire resistance
	WaterRes                  int16 // water resistance
	WindRes                   int16 // wind resistance
	EarthRes                  int16 // earth resistance
	Dodge                     int16 // dodge bonus
	RegistSustain             int16 // 持續傷害抗性
	RegistFreeze              int16 // 凍結抗性
	RegistStun                int16 // 暈眩抗性
	RegistStone               int16 // 石化抗性
	RegistBlind               int16 // 失明抗性
	RegistSleep               int16 // 睡眠抗性
	StunLevel                 int16 // 昏迷等級（Java: getStunLevel，用於衝擊之暈成功率）
	MagicCritical             int16 // 魔法爆擊加成
	OriginalMagicHit          int16 // 額外魔法命中（Java: getOriginalMagicHit，用於有害魔法成功率）
	DollDmgReduceRandom       int16 // 魔法娃娃機率減傷量（Yiwei: dmgDowe）
	DollDmgReduceRandomChance int   // 魔法娃娃機率減傷機率百分比
	Food                      int16 // satiety 0-225 (225=full); sent in S_STATUS
	FoodFullTime              int64 // 飽食度達 225 的時刻（Unix 秒）；-1=未滿（Java: _h_time，生存吶喊用）
	CookingID                 int32 // 當前料理 buff 的 skill ID（0=無）；同時只能有一個
	AccessLevel               int16 // GM 等級（0=一般玩家, ≥200=GM）
	PKCount                   int32 // PK kill count
	KillCount                 int32 // PvP 擊殺累計（排名用）
	DeathCount                int32 // PvP 死亡累計（排名用）
	PartnerID                 int32 // 配偶角色 ID（0=未婚；結婚系統用）
	MarriageRingID            int32 // 結婚時使用的戒指物品 ID（Java: QUEST_MARRY step）
	TempID                    int32 // 暫存目標 ID（Java: pc.setTempID）— 寵物改名等用途

	// 武器吸血/吸魔累計值（Java: L1PcInstance dice_hp/sucking_hp/dice_mp/sucking_mp）
	DrainDiceHP       int   // 所有裝備累計的 HP 吸取機率
	DrainSuckingHP    int   // 所有裝備累計的 HP 吸取量
	DrainDiceMP       int   // 所有裝備累計的 MP 吸取機率
	DrainSuckingMP    int   // 所有裝備累計的 MP 吸取量
	WeaknessLevel     int16 // 龍騎士弱點曝光階段，0=無，1-3=階段
	WeaknessTargetID  int32 // 目前弱點曝光鎖定目標；切換目標會清除階段
	FoeSlayerBonusDmg int32 // 屠宰者額外傷害加成來源，例如娃娃效果

	// 釣魚系統
	Fishing       bool  // 是否正在釣魚
	FishX         int32 // 釣點 X 座標
	FishY         int32 // 釣點 Y 座標
	FishingPoleID int32 // 使用中的釣竿物品 ID
	FishingTick   int   // 釣魚計時器（tick 計數）

	Karma             int32 // 善惡值（Java: L1Karma）— 正=善, 負=惡
	PinkName          bool  // temporary red name (180 seconds after attacking blue player)
	PinkNameTicks     int   // remaining ticks for pink name timer
	WantedTicks       int   // >0 = wanted by guards (24h = 432000 ticks at 200ms/tick)
	FightId           int32 // 0=無決鬥, >0=決鬥對手角色 ID（Java: L1PcInstance.fightId）
	WarehousePassword int32 // 倉庫密碼（0=未設定, >0=6位數密碼）。從帳號載入。
	RegenHPAcc        int   // HP regen accumulator: counts 1-second ticks since last HP regen

	// 角色重置（洗點）暫存欄位（Java: tempMaxLevel, tempLevel, tempElixirstats 等）
	InCharReset      bool  // true=正在重置中（凍結操作）
	ResetTempLevel   int16 // 重置過程中的臨時等級（從 1 開始逐級升）
	ResetMaxLevel    int16 // 重置目標等級（當前等級）
	ResetElixirStats int16 // 萬能藥額外點數

	Dead             bool  // true when HP <= 0, waiting for restart
	PendingResSkill  int32 // 待同意的復活技能 ID（61/75），0=無待復活
	PendingResCaster int32 // 施法者 CharID
	TombEffectID     int32 // 死亡時生成的墓碑 GroundEffect ID（Java: pc.get_tomb）
	Invisible        bool  // true when under Invisibility
	Paralyzed        bool  // true when frozen/stunned/bound
	Sleeped          bool  // true when under sleep effect
	Silenced         bool  // 沉默狀態（沉默毒 / silence 技能）— 禁止施法
	AbsoluteBarrier  bool  // 絕對屏障（skill 78）— 免疫所有傷害，攻擊/施法/使用道具時解除
	AttackView       bool  // 浮動傷害數字開關（Java: is_attack_view，預設 true，聊天輸入 dmg 切換）
	WaterOff         bool  // GM 切換：true 時即使在 underwater 地圖也不送水的旗標給客戶端

	// ShowID 副本實例隔離標籤（對應 Java L1PcInstance.set_showId）。
	// 0 = 主世界（預設）；>=100 = 副本實例 ID（QuestWorldSystem 分配，從 100 起遞增）。
	// AOI 過濾用：相同 ShowID 才互相可見（同 mapID 多副本實例隔離）。
	// 注意 Java 用 -1 作 sentinel，Go 改用 0 因 Go 零初值=0、副本實例 ID 永遠 >= 100，0/-1 等價無歧義。
	ShowID int32

	// 火龍窟副本（MISS-P2-016）：威頓村 NPC 漢(46180) 發放「漢的袋子」(80001) 的下次可領 Unix 秒數。
	// 24 小時冷卻；0 = 從未領過。值對應 Java 角色資料的「下次任務道具可領時間」。
	NextHansBagAt int64

	// 動態 HTML 對話即時更新狀態（搭配客戶端 @dynamic patch）。
	// nil = 沒有 live dialog；非 nil = LiveDialogSystem 會定期 re-render 並重送對話。
	// 玩家任何 NPC 動作（包含關閉對話）都會清空此欄位。
	LiveDialog *LiveDialogState

	LastMoveTime int64 // time.Now().UnixNano() of last accepted move (0 = no throttle)

	TempCharGfx int32 // 0=use ClassID; >0=current polymorph GFX sprite
	PolyID      int32 // current polymorph poly_id (for equip/skill checks; 0=not polymorphed)
	ActiveSetID int   // armor set ID currently active (0=none); cleared when set is incomplete

	// Summon selection mode: true when "summonlist" dialog is open, waiting for player to pick a summon.
	// Set by executeSummonMonster when ring equipped; cleared by HandleNpcAction on numeric response.
	SummonSelectionMode bool
	PendingPolySkill    bool // true when skill 67 opened monlist and is waiting for player selection

	Inv          *Inventory // in-memory inventory
	Equip        Equipment  // equipped items (value type, zero-initialized = all slots empty)
	EquipBonuses EquipStats // cached equipment stat contributions (for diff on equip/unequip)

	// 額外負重加成（MISS-P1-006）— 來自魔法娃娃等系統，獨立於 buff 14/218。
	// 對應 Java Doll_Weight 系列。PlayerMaxWeight 會疊加此值。
	WeightBonus int32
	// 額外負重百分比（Java add_weightUP）。值 4 代表最大負重 +4%。
	WeightBonusPct int16

	// Cached current weapon visual byte (for S_PUT_OBJECT / S_CHANGE_DESC)
	CurrentWeapon byte

	// Pending teleport destination (set by teleport scroll/spell, executed by C_TELEPORT)
	TeleportX       int32
	TeleportY       int32
	TeleportMapID   int16
	TeleportHeading int16
	HasTeleport     bool // true when a teleport is prepared and waiting for C_TELEPORT confirmation

	// 卷軸延遲傳送：特效發出後延遲 1 tick 再執行（模擬 Java Thread.sleep(196ms)）
	ScrollTPTick int // >0 = 剩餘等待 tick 數；每 tick -1，到 0 時執行傳送
	ScrollTPX    int32
	ScrollTPY    int32
	ScrollTPMap  int16

	// Teleport bookmarks
	Bookmarks []Bookmark

	// Known spell IDs (skill_id values the player has learned)
	KnownSpells []int32

	// Global cast cooldown: cannot cast any spell before this time (Java: isSkillDelay)
	SkillDelayUntil time.Time

	// Active buffs: skillID → remaining ticks. Decremented each tick; removed at 0.
	ActiveBuffs map[int32]*ActiveBuff

	// Warehouse: temporary cache while warehouse UI is open
	WarehouseItems []*WarehouseCache // loaded from DB on open, nil when closed
	WarehouseType  int16             // 3=personal, 4=elf, 5=clan

	// Party
	PartyID     int32 // 0=not in party
	PartyLeader bool

	// Trade
	TradePartnerID  int32      // CharID of trade partner (0 = not trading)
	TradeWindowOpen bool       // true after target accepted trade (windows are open)
	TradeOk         bool       // true when this side has pressed confirm
	TradeItems      []*InvItem // items offered in trade
	TradeGold       int32      // gold offered in trade

	// 個人商店（擺攤）
	PrivateShop       bool               // true = 正在擺攤中
	ShopSellList      []*PrivateShopSell // 出售清單
	ShopBuyList       []*PrivateShopBuy  // 收購清單
	ShopChat          []byte             // 商店標語（Big5 原始位元組）
	ShopTradingLocked bool               // true = 有人正在購買中，防止併發
	ShopPartnerCount  int                // 對方商店的商品數量快取（Java: partnersPrivateShopItemCount）
	NoAskMassTeleport bool               // true=關閉集體傳送詢問；false=Java 預設會詢問

	// 光源（Java turnOnOffLight: 0=無光, 14=日光術, 最大值=角色周圍亮光圈半徑）
	LightSize byte

	// --- 中毒系統（Java L1Poison）---
	// PoisonType: 0=無, 1=傷害毒, 2=沉默毒, 3=麻痺毒延遲中, 4=麻痺毒已麻痺
	PoisonType             byte
	PoisonTicksLeft        int    // 毒剩餘 ticks（傷害毒:150, 麻痺延遲:100, 麻痺:80）
	PoisonDmgTimer         int    // 傷害毒：距下次扣血的 tick 計數（每 15 tick 扣一次）
	PoisonDmgIntervalTicks int    // 傷害毒扣血間隔；0 表示使用 Java 一般預設 15 ticks
	PoisonDmgAmount        int16  // 傷害毒每次扣血量（NPC攻擊:20, 毒咒:5）
	PoisonParalysisTicks   int    // 麻痺毒延遲後的麻痺持續 ticks；0 表示使用 Java 一般預設 80 ticks
	PoisonAttacker         uint64 // 施毒者 SessionID（傷害毒歸屬用）

	// 投石車沉默砲彈到期時間（Unix 秒，0=無效）
	CatapultSilenceEnd int64

	// --- 詛咒麻痺系統（Java L1CurseParalysis，與毒系統獨立）---
	// CurseType: 0=無, 1=詛咒延遲中, 2=詛咒已麻痺
	CurseType      byte
	CurseTicksLeft int // 詛咒剩餘 ticks（延遲:25, 麻痺:20）

	// AOI 可見性追蹤（VisibilitySystem 使用）
	Known *KnownEntities

	// Pending yes/no dialog (S_Message_YN response tracking)
	PendingYesNoType int16 // 0=none, 252=trade confirm, 953=party invite, etc.
	PendingYesNoData int32 // related charID (trade partner or party inviter)

	// Party invite context: what type of party the inviter wants to create
	// Java: pc.setPartyType(type) — 0=normal, 1=auto-share
	PartyInviteType byte

	// Party position refresh: Java L1PartyRefresh runs every 25 seconds.
	// Counter decrements each tick; at 0, sends position refresh and resets.
	PartyRefreshTicks int

	// Crafting: set when S_InputAmount is sent, cleared on C_Amount response.
	// Non-empty value means the next opcode 11 (C_HYPERTEXT_INPUT_RESULT) should be
	// interpreted as C_Amount (crafting batch response) instead of monlist (polymorph).
	PendingCraftAction string

	// 火神工匠系統：玩家選擇配方後，儲存當前選中的配方 action key 和 NPC 物件 ID。
	// "confirm craft" 時用這些資訊找到對應配方。
	PendingCraftKey   string // 當前選中的配方 action key（如 "A", "B"... "a1"...）
	PendingCraftNpcID int32  // 當前互動的 NPC ID（非物件 ID）
	PendingCraftIndex int    // ItemBlend 配方瀏覽的目前索引（cancel craft 循環用）

	// 交易視窗延遲發送：S_Trade 先開視窗，延遲 1 tick 再發 S_TradeAddItem。
	// 3.80C 客戶端在同一 tick 收到 S_Trade + S_TradeAddItem 時，交易視窗尚未初始化完成，
	// 導致物品不顯示。正常交易中物品是玩家手動拖入的，自然有延遲。
	CraftTradeTick int // >0 = 剩餘等待 tick 數；每 tick -1，到 0 時發送交易物品

	// 火神精煉：當玩家開啟精煉介面時，記住 NPC 物件 ID，以便 C_Result 攔截。
	// 0 = 未開啟精煉介面。
	FireSmithNpcObjID     int32
	CnShopNpcID           int32 // 最近瀏覽的寄賣商城 NPC ID（購買時用於查詢商品）
	PowerItemNpcID        int32 // 最近瀏覽的強化物品商店 NPC ID
	PendingAuctionHouseID int32 // 拍賣出價待處理的小屋 ID（0=無）

	// 出售小屋：S_SellHouse 發送後的待處理狀態（Java agsell 流程）
	PendingSellHouseID int32 // 售屋等待 C_Amount 回覆的小屋 ID（0=無）

	// 旅館租房：記錄 S_HowManyKey 發送後的待處理狀態
	PendingInnNpcObjID int32 // 旅館 NPC 物件 ID（0=無待處理）
	PendingInnRoomNum  int32 // 選中的房間號碼
	PendingInnHall     bool  // 是否租會議室

	// Paginated teleport (Npc_Teleport): current browsing state
	TelePage     int    // current page (0-based)
	TeleCategory string // current category key (e.g., "A", "B", "H01")
	TeleNpcObjID int32  // NPC object ID for the teleport dialog

	// Buddy/friend list (persisted to DB, loaded on enter world)
	Buddies []BuddyEntry

	// Exclude/block list (session-only, max 16 entries, NOT persisted)
	ExcludeList []string

	// 任務進度（登入時從 character_quests 載入）
	// key=quest_id, value=step（0=未開始, 1~254=進行中, 255=已完成）
	Quests map[int32]int32

	// 物品使用延遲（runtime-only，不持久化）
	// key=DelayID (如 502=道具共用), value=到期時間
	ItemDelays map[int]time.Time

	// 限時地圖定時器（Java: MapTimerThread / character_maps_time）
	MapTimeUsed       map[int]int // key=組別 OrderID, value=已使用秒數
	MapTimerGroupIdx  int         // 當前所在限時地圖組 OrderID（-1 或 0 = 不在限時地圖）
	MapTimerRemaining int         // 剩餘秒數（runtime 快取）
	MapTimerTickAcc   int         // tick 累加器（每 5 tick = 1 秒）

	// VIP 物品系統：已啟用的 VIP 物品 objectID，按 type 分組（同 type 只能一個）。
	// key=VIP type, value=InvItem.ObjectID
	ActiveVIP map[int]int32

	// Dirty flag for batch persistence. Set to true when any persisted state
	// changes (position, HP/MP, exp, inventory, buffs). PersistenceSystem only
	// saves dirty players and resets this flag after each successful save.
	Dirty bool
}

// BuddyEntry represents a single buddy in the player's friend list.
type BuddyEntry struct {
	CharID int32
	Name   string
}

// WarehouseCache maps a temporary objectID to a DB warehouse item.
type WarehouseCache struct {
	TempObjID        int32
	DbID             int32 // warehouse_items.id in DB
	ItemID           int32
	Count            int32
	EnchantLvl       int16
	Bless            int16
	Stackable        bool
	Identified       bool
	UseType          byte // 0=etcitem, 1=weapon, 2=armor
	ChargeCount      int16
	LastUsedAt       int64
	Durability       int8
	AttrEnchantKind  int8
	AttrEnchantLevel int8
	InnKeyID         int32
	InnNpcID         int32
	InnHall          bool
	InnDueTime       int64
	Name             string
	InvGfx           int32
	Weight           int32
}

// PrivateShopSell 個人商店出售清單項目。
type PrivateShopSell struct {
	ItemObjectID int32 // 出售物品的 ObjectID
	SellTotal    int32 // 預計出售的總數量
	SellPrice    int32 // 單價
	SoldCount    int32 // 已出售的累計數量
}

// PrivateShopBuy 個人商店收購清單項目。
type PrivateShopBuy struct {
	ItemObjectID int32 // 背包中作為「樣本」的物品 ObjectID
	ItemID       int32 // 物品模板 ID（驗證用）
	EnchantLvl   int8  // 要求的強化等級
	BuyTotal     int32 // 預計收購的總數量
	BuyPrice     int32 // 收購單價
	BoughtCount  int32 // 已收購的累計數量
}

// ActiveBuff tracks a single active buff/debuff on a player.
type ActiveBuff struct {
	SkillID   int32
	TicksLeft int // remaining ticks (0 = permanent until cancelled)
	// Stat deltas applied when buff started (reversed on removal)
	DeltaAC            int16
	DeltaStr           int16
	DeltaDex           int16
	DeltaCon           int16
	DeltaWis           int16
	DeltaIntel         int16
	DeltaCha           int16
	DeltaMaxHP         int32
	DeltaMaxMP         int32
	DeltaHitMod        int16
	DeltaDmgMod        int16
	DeltaSP            int16
	DeltaMR            int16
	DeltaHPR           int16
	DeltaMPR           int16
	DeltaBowHit        int16
	DeltaBowDmg        int16
	DeltaWeightPct     int16
	DeltaFireRes       int16
	DeltaWaterRes      int16
	DeltaWindRes       int16
	DeltaEarthRes      int16
	DeltaDodge         int16
	DeltaRegistSustain int16
	DeltaRegistFreeze  int16
	DeltaRegistStun    int16
	DeltaRegistStone   int16
	DeltaRegistBlind   int16
	DeltaRegistSleep   int16
	DeltaMagicCritical int16
	// Special flags for non-stat effects
	SetMoveSpeed       byte // if > 0, the buff set MoveSpeed to this value
	SetBraveSpeed      byte // if > 0, the buff set BraveSpeed to this value
	SetInvisible       bool // buff made player invisible
	SetParalyzed       bool // buff paralyzed/froze player
	SetSleeped         bool // buff put player to sleep
	SetSilenced        bool // buff made player unable to cast spells
	SetAbsoluteBarrier bool // buff 設定了絕對屏障（到期/移除時清 flag）
}

// HasBuff returns true if the player has the given skill effect active.
func (p *PlayerInfo) HasBuff(skillID int32) bool {
	if p.ActiveBuffs == nil {
		return false
	}
	_, ok := p.ActiveBuffs[skillID]
	return ok
}

// GetBuff returns the active buff for a skillID, or nil if not found.
func (p *PlayerInfo) GetBuff(skillID int32) *ActiveBuff {
	if p.ActiveBuffs == nil {
		return nil
	}
	return p.ActiveBuffs[skillID]
}

// AddBuff adds or replaces a buff. Returns the old buff if replaced, for stat reversal.
func (p *PlayerInfo) AddBuff(buff *ActiveBuff) *ActiveBuff {
	if p.ActiveBuffs == nil {
		p.ActiveBuffs = make(map[int32]*ActiveBuff)
	}
	old := p.ActiveBuffs[buff.SkillID]
	p.ActiveBuffs[buff.SkillID] = buff
	return old
}

// RemoveBuff removes a buff and returns it for stat reversal, or nil if not found.
func (p *PlayerInfo) RemoveBuff(skillID int32) *ActiveBuff {
	if p.ActiveBuffs == nil {
		return nil
	}
	old := p.ActiveBuffs[skillID]
	delete(p.ActiveBuffs, skillID)
	return old
}

// IsQuestDone 檢查指定任務是否已完成（step == 255）。
func (p *PlayerInfo) IsQuestDone(questID int32) bool {
	return p.Quests[questID] == 255
}

// QuestStep 取得指定任務的進度步驟（0=未開始）。
func (p *PlayerInfo) QuestStep(questID int32) int32 {
	return p.Quests[questID]
}

// SetQuestStep 設定任務進度步驟。
func (p *PlayerInfo) SetQuestStep(questID, step int32) {
	if p.Quests == nil {
		p.Quests = make(map[int32]int32)
	}
	if step == 0 {
		delete(p.Quests, questID)
	} else {
		p.Quests[questID] = step
	}
}

// KnownPos 記錄已知實體的最後位置（用於離開視野時解鎖格子）。
type KnownPos struct{ X, Y int32 }

// KnownEntities 追蹤玩家目前視野中的已知實體（類似 Java knownObjects）。
// VisibilitySystem 每 2 tick 掃描一次，與此集合做 diff。
type KnownEntities struct {
	Players       map[int32]KnownPos // CharID → 位置
	Npcs          map[int32]KnownPos // NPC 實例 ID → 位置
	Summons       map[int32]KnownPos // 召喚獸 ID → 位置
	Dolls         map[int32]KnownPos // 魔法娃娃 ID → 位置
	Hierarchs     map[int32]KnownPos // 隨身祭司 ID → 位置
	Followers     map[int32]KnownPos // 隨從 ID → 位置
	Pets          map[int32]KnownPos // 寵物 ID → 位置
	GroundItems   map[int32]KnownPos // 地面物品 ID → 位置
	GroundEffects map[int32]KnownPos // 地面技能效果 ID → 位置
	Doors         map[int32]KnownPos // 門 ID → 位置
}

// NewKnownEntities 建立空白的已知實體集合。
func NewKnownEntities() *KnownEntities {
	return &KnownEntities{
		Players:       make(map[int32]KnownPos),
		Npcs:          make(map[int32]KnownPos),
		Summons:       make(map[int32]KnownPos),
		Dolls:         make(map[int32]KnownPos),
		Hierarchs:     make(map[int32]KnownPos),
		Followers:     make(map[int32]KnownPos),
		Pets:          make(map[int32]KnownPos),
		GroundItems:   make(map[int32]KnownPos),
		GroundEffects: make(map[int32]KnownPos),
		Doors:         make(map[int32]KnownPos),
	}
}

// Reset 清空所有已知實體（用於傳送、rejectMove 等場景）。
func (k *KnownEntities) Reset() {
	clear(k.Players)
	clear(k.Npcs)
	clear(k.Summons)
	clear(k.Dolls)
	clear(k.Hierarchs)
	clear(k.Followers)
	clear(k.Pets)
	clear(k.GroundItems)
	clear(k.Doors)
}

// Bookmark is a saved teleport location for a player.
type Bookmark struct {
	ID    int32  // unique bookmark ID (auto-increment from DB)
	Name  string // display name
	X     int32
	Y     int32
	MapID int16
}

// tileKey uniquely identifies a tile in the world (map + coordinates).
type tileKey struct {
	MapID int16
	X, Y  int32
}

// EntityGrid is a tile occupancy map for O(1) collision checks.
// Supports multiple occupants per tile (for monster stuck-crossing scenarios).
// Player CharIDs < 100,000; NPC IDs start at 200,000,000 — no overlap.
type EntityGrid struct {
	tiles map[tileKey]map[int32]struct{}
}

func newEntityGrid() *EntityGrid {
	return &EntityGrid{tiles: make(map[tileKey]map[int32]struct{})}
}

// Occupy marks an entity as occupying a tile.
func (g *EntityGrid) Occupy(mapID int16, x, y int32, entityID int32) {
	k := tileKey{MapID: mapID, X: x, Y: y}
	cell := g.tiles[k]
	if cell == nil {
		cell = make(map[int32]struct{}, 1)
		g.tiles[k] = cell
	}
	cell[entityID] = struct{}{}
}

// Vacate removes an entity from a tile.
func (g *EntityGrid) Vacate(mapID int16, x, y int32, entityID int32) {
	k := tileKey{MapID: mapID, X: x, Y: y}
	cell := g.tiles[k]
	if cell != nil {
		delete(cell, entityID)
		if len(cell) == 0 {
			delete(g.tiles, k)
		}
	}
}

// Move atomically vacates old tile and occupies new tile.
func (g *EntityGrid) Move(mapID int16, oldX, oldY, newX, newY int32, entityID int32) {
	if oldX == newX && oldY == newY {
		return
	}
	g.Vacate(mapID, oldX, oldY, entityID)
	g.Occupy(mapID, newX, newY, entityID)
}

// IsOccupied returns true if any entity other than excludeID occupies the tile.
func (g *EntityGrid) IsOccupied(mapID int16, x, y int32, excludeID int32) bool {
	k := tileKey{MapID: mapID, X: x, Y: y}
	cell := g.tiles[k]
	if len(cell) == 0 {
		return false
	}
	for id := range cell {
		if id != excludeID {
			return true
		}
	}
	return false
}

// OccupantAt returns the first occupant ID at the tile, or 0 if empty.
func (g *EntityGrid) OccupantAt(mapID int16, x, y int32) int32 {
	k := tileKey{MapID: mapID, X: x, Y: y}
	for id := range g.tiles[k] {
		return id
	}
	return 0
}

// State tracks all players and NPCs currently in-world.
// Single-goroutine access only (game loop).
type State struct {
	bySession map[uint64]*PlayerInfo // SessionID → PlayerInfo
	byCharID  map[int32]*PlayerInfo  // CharID → PlayerInfo
	byName    map[string]*PlayerInfo // CharName → PlayerInfo
	aoi       *AOIGrid
	npcAoi    *NpcAOIGrid
	effectAoi *NpcAOIGrid
	entity    *EntityGrid

	npcs    map[int32]*NpcInfo // NPC object ID → NpcInfo
	npcList []*NpcInfo         // all NPCs (for tick iteration)

	doors    map[int32]*DoorInfo // door object ID → DoorInfo
	doorList []*DoorInfo         // all doors (for tick iteration)

	pets      map[int32]*PetInfo      // pet object ID → PetInfo
	summons   map[int32]*SummonInfo   // summon object ID → SummonInfo
	dolls     map[int32]*DollInfo     // doll object ID → DollInfo
	followers map[int32]*FollowerInfo // follower object ID → FollowerInfo
	hierarchs map[int32]*HierarchInfo // hierarch object ID → HierarchInfo

	groundItems   map[int32]*GroundItem   // ground item object ID → GroundItem
	groundEffects map[int32]*GroundEffect // ground skill effect object ID → GroundEffect
	furnitureNpcs map[int32]int32         // 道具 objectID → NPC objectID

	Parties     *PartyManager
	ChatParties *ChatPartyManager
	Clans       *ClanManager

	// Weather & game time (accessed from game loop only)
	Weather  byte // current weather type (0=clear, 1-3=snow, 17-19=rain)
	LastHour int  // last game hour for hour-change detection (-1 = uninitialized)

	// 可重用 AOI 查詢 buffer（遊戲迴圈單線程，無需鎖）
	aoiBuf       []uint64
	npcAoiBuf    []int32
	effectAoiBuf []int32
}

// RandomizeWeather picks a random weather with weighted distribution.
// Java defaults weather to 4 (clear) and never auto-changes. In Go, we add
// some variety but keep clear weather dominant (~60%) to avoid constant rain/snow.
// Valid values: 0=clear, 1-3=snow, 17-19=rain (Java confirms 17-19, not 16).
func (s *State) RandomizeWeather() {
	roll := rand.Intn(10) // 0-9
	switch {
	case roll < 6: // 60% clear
		s.Weather = 0
	case roll < 8: // 20% snow (light)
		s.Weather = byte(1 + rand.Intn(3)) // 1, 2, or 3
	default: // 20% rain (light)
		s.Weather = byte(17 + rand.Intn(3)) // 17, 18, or 19
	}
}

func NewState() *State {
	return &State{
		bySession:     make(map[uint64]*PlayerInfo),
		byCharID:      make(map[int32]*PlayerInfo),
		byName:        make(map[string]*PlayerInfo),
		aoi:           NewAOIGrid(),
		npcAoi:        NewNpcAOIGrid(),
		effectAoi:     NewNpcAOIGrid(),
		entity:        newEntityGrid(),
		Parties:       NewPartyManager(),
		ChatParties:   NewChatPartyManager(),
		Clans:         NewClanManager(),
		npcs:          make(map[int32]*NpcInfo),
		doors:         make(map[int32]*DoorInfo),
		pets:          make(map[int32]*PetInfo),
		summons:       make(map[int32]*SummonInfo),
		dolls:         make(map[int32]*DollInfo),
		followers:     make(map[int32]*FollowerInfo),
		groundItems:   make(map[int32]*GroundItem),
		groundEffects: make(map[int32]*GroundEffect),
		furnitureNpcs: make(map[int32]int32),
		LastHour:      -1,
	}
}

// AddPlayer registers a player in the world.
func (s *State) AddPlayer(p *PlayerInfo) {
	s.bySession[p.SessionID] = p
	s.byCharID[p.CharID] = p
	s.byName[p.Name] = p
	s.aoi.Add(p.SessionID, p.X, p.Y, p.MapID)
	s.entity.Occupy(p.MapID, p.X, p.Y, p.CharID)
}

// RemovePlayer removes a player from the world.
func (s *State) RemovePlayer(sessionID uint64) *PlayerInfo {
	p, ok := s.bySession[sessionID]
	if !ok {
		return nil
	}
	s.aoi.Remove(sessionID, p.X, p.Y, p.MapID)
	s.entity.Vacate(p.MapID, p.X, p.Y, p.CharID)
	delete(s.bySession, sessionID)
	delete(s.byCharID, p.CharID)
	delete(s.byName, p.Name)
	return p
}

// GetBySession returns a player by session ID.
func (s *State) GetBySession(sessionID uint64) *PlayerInfo {
	return s.bySession[sessionID]
}

// GetByCharID returns a player by character DB ID.
func (s *State) GetByCharID(charID int32) *PlayerInfo {
	return s.byCharID[charID]
}

// GetByName returns a player by character name.
func (s *State) GetByName(name string) *PlayerInfo {
	return s.byName[name]
}

// UpdatePosition moves a player and updates AOI grid + entity grid.
func (s *State) UpdatePosition(sessionID uint64, newX, newY int32, newMapID int16, heading int16) {
	p := s.bySession[sessionID]
	if p == nil {
		return
	}
	oldX, oldY, oldMap := p.X, p.Y, p.MapID
	p.X = newX
	p.Y = newY
	p.MapID = newMapID
	p.Heading = heading
	s.aoi.Move(sessionID, oldX, oldY, oldMap, newX, newY, newMapID)
	s.entity.Move(oldMap, oldX, oldY, newX, newY, p.CharID)
}

// GetNearbyPlayers returns all players visible to the given position.
// Uses Chebyshev distance <= 20 (matching Java PC_RECOGNIZE_RANGE).
func (s *State) GetNearbyPlayers(x, y int32, mapID int16, excludeSession uint64) []*PlayerInfo {
	s.aoiBuf = s.aoi.GetNearbyInto(x, y, mapID, s.aoiBuf)
	nearbyIDs := s.aoiBuf
	result := make([]*PlayerInfo, 0, len(nearbyIDs))
	for _, sid := range nearbyIDs {
		if sid == excludeSession {
			continue
		}
		p := s.bySession[sid]
		if p == nil {
			continue
		}
		// Chebyshev distance check
		dx := p.X - x
		dy := p.Y - y
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
		if dist <= 20 {
			result = append(result, p)
		}
	}
	return result
}

// GetNearbyPlayersInShow 同 GetNearbyPlayers，但額外用 ShowID 過濾。
// 用於副本系統（MISS-P0-003）：相同 ShowID 才互相可見。
// viewerShowID=0 代表主世界視角，只看得到 ShowID=0 的玩家；副本內視角同理只看自己副本。
func (s *State) GetNearbyPlayersInShow(x, y int32, mapID int16, excludeSession uint64, viewerShowID int32) []*PlayerInfo {
	s.aoiBuf = s.aoi.GetNearbyInto(x, y, mapID, s.aoiBuf)
	nearbyIDs := s.aoiBuf
	result := make([]*PlayerInfo, 0, len(nearbyIDs))
	for _, sid := range nearbyIDs {
		if sid == excludeSession {
			continue
		}
		p := s.bySession[sid]
		if p == nil {
			continue
		}
		if p.ShowID != viewerShowID {
			continue
		}
		dx := p.X - x
		dy := p.Y - y
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
		if dist <= 20 {
			result = append(result, p)
		}
	}
	return result
}

// GetNearbyPlayersInShowRange 同 GetNearbyPlayersInShow，但使用指定 Chebyshev 半徑。
func (s *State) GetNearbyPlayersInShowRange(x, y int32, mapID int16, excludeSession uint64, viewerShowID int32, radius int32) []*PlayerInfo {
	if radius < 0 {
		radius = 0
	}
	s.aoiBuf = s.aoi.GetNearbyIntoRange(x, y, mapID, radius, s.aoiBuf)
	nearbyIDs := s.aoiBuf
	result := make([]*PlayerInfo, 0, len(nearbyIDs))
	for _, sid := range nearbyIDs {
		if sid == excludeSession {
			continue
		}
		p := s.bySession[sid]
		if p == nil {
			continue
		}
		if p.ShowID != viewerShowID {
			continue
		}
		dx := p.X - x
		dy := p.Y - y
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
		if dist <= radius {
			result = append(result, p)
		}
	}
	return result
}

// ChangePlayerHeading 更新玩家朝向。
func (s *State) ChangePlayerHeading(player *PlayerInfo, heading int16) {
	player.Heading = heading
}

// PlayerCount returns the number of players in-world.
func (s *State) PlayerCount() int {
	return len(s.bySession)
}

// AllPlayers iterates all in-world players.
func (s *State) AllPlayers(fn func(*PlayerInfo)) {
	for _, p := range s.bySession {
		fn(p)
	}
}

// --- NPC methods ---

// AddNpc registers an NPC in the world.
func (s *State) AddNpc(npc *NpcInfo) {
	s.npcs[npc.ID] = npc
	s.npcList = append(s.npcList, npc)
	s.npcAoi.Add(npc.ID, npc.X, npc.Y, npc.MapID)
	s.entity.Occupy(npc.MapID, npc.X, npc.Y, npc.ID)
}

// GetNpc returns an NPC by its object ID.
func (s *State) GetNpc(id int32) *NpcInfo {
	return s.npcs[id]
}

// GetNearbyNpcs returns all alive NPCs visible from the given position (Chebyshev <= 20).
// Uses NPC AOI grid for O(cells) lookup instead of O(N) full scan.
func (s *State) GetNearbyNpcs(x, y int32, mapID int16) []*NpcInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	result := make([]*NpcInfo, 0, len(nearbyIDs))
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc == nil || npc.Dead {
			continue
		}
		dx := npc.X - x
		dy := npc.Y - y
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
		if dist <= 20 {
			result = append(result, npc)
		}
	}
	return result
}

// GetNearbyNpcsInShow 同 GetNearbyNpcs，但額外用 ShowID 過濾。
// 用於副本系統（MISS-P0-003）：相同 ShowID 才互相可見。
// viewerShowID=0 代表主世界視角，只看得到 ShowID=0 的 NPC；副本內視角同理只看自己副本的 NPC。
func (s *State) GetNearbyNpcsInShow(x, y int32, mapID int16, viewerShowID int32) []*NpcInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	result := make([]*NpcInfo, 0, len(nearbyIDs))
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc == nil || npc.Dead {
			continue
		}
		if npc.ShowID != viewerShowID {
			continue
		}
		dx := npc.X - x
		dy := npc.Y - y
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
		if dist <= 20 {
			result = append(result, npc)
		}
	}
	return result
}

// GetNpcsAlongLine 回傳從 (x1,y1) 到 (x2,y2) 直線上的所有存活 NPC（Bresenham 演算法）。
// 用於極光雷電（skill 17）的直線目標判定（Java: getVisibleLineObjects）。
func (s *State) GetNpcsAlongLine(x1, y1, x2, y2 int32, mapID int16) []*NpcInfo {
	// 收集直線經過的所有格子座標
	tiles := bresenhamLine(x1, y1, x2, y2)

	// 取附近所有 NPC 建立座標索引
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x1, y1, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf

	var result []*NpcInfo
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc == nil || npc.Dead || npc.MapID != mapID {
			continue
		}
		for _, t := range tiles {
			if npc.X == t[0] && npc.Y == t[1] {
				result = append(result, npc)
				break
			}
		}
	}
	return result
}

// bresenhamLine 回傳從 (x1,y1) 到 (x2,y2) 的 Bresenham 直線格子座標。
func bresenhamLine(x1, y1, x2, y2 int32) [][2]int32 {
	dx := x2 - x1
	dy := y2 - y1
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}

	sx := int32(1)
	if x1 > x2 {
		sx = -1
	}
	sy := int32(1)
	if y1 > y2 {
		sy = -1
	}

	var tiles [][2]int32
	if dx >= dy {
		err := dx / 2
		y := y1
		for x := x1; x != x2+sx; x += sx {
			tiles = append(tiles, [2]int32{x, y})
			err -= dy
			if err < 0 {
				y += sy
				err += dx
			}
		}
	} else {
		err := dy / 2
		x := x1
		for y := y1; y != y2+sy; y += sy {
			tiles = append(tiles, [2]int32{x, y})
			err -= dx
			if err < 0 {
				x += sx
				err += dy
			}
		}
	}
	return tiles
}

// GetNearbyNpcsForVis 回傳附近可見 NPC（含屍體）供 VisibilitySystem 使用。
// 活著的 NPC + 死亡但 DeleteTimer > 0 的 NPC（屍體仍需顯示在客戶端）。
func (s *State) GetNearbyNpcsForVis(x, y int32, mapID int16) []*NpcInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	result := make([]*NpcInfo, 0, len(nearbyIDs))
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc == nil {
			continue
		}
		// 跳過已完成刪除階段的死亡 NPC（DeleteTimer 已歸零）
		if npc.Dead && npc.DeleteTimer <= 0 {
			continue
		}
		dx := npc.X - x
		dy := npc.Y - y
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
		if dist <= 20 {
			result = append(result, npc)
		}
	}
	return result
}

func (s *State) GetNearbyNpcsForVisInShow(x, y int32, mapID int16, showID int32) []*NpcInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	result := make([]*NpcInfo, 0, len(nearbyIDs))
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc == nil || npc.ShowID != showID {
			continue
		}
		// 跳過已完成刪除階段的死亡 NPC（DeleteTimer 已歸零）
		if npc.Dead && npc.DeleteTimer <= 0 {
			continue
		}
		dx := npc.X - x
		dy := npc.Y - y
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
		if dist <= 20 {
			result = append(result, npc)
		}
	}
	return result
}

// UpdateNpcPosition moves an NPC and updates NPC AOI grid + entity grid.
// All NPC position changes MUST go through this method to keep indices consistent.
func (s *State) UpdateNpcPosition(npcID int32, newX, newY int32, heading int16) {
	npc := s.npcs[npcID]
	if npc == nil {
		return
	}
	oldX, oldY := npc.X, npc.Y
	npc.X = newX
	npc.Y = newY
	npc.Heading = heading
	s.npcAoi.Move(npcID, oldX, oldY, npc.MapID, newX, newY, npc.MapID)
	s.entity.Move(npc.MapID, oldX, oldY, newX, newY, npcID)
}

// RemoveNpc permanently removes an NPC from the world (used for taming, conversion).
// Unlike NpcDied, this also deletes the NPC from the internal maps.
func (s *State) RemoveNpc(npcID int32) *NpcInfo {
	npc, ok := s.npcs[npcID]
	if !ok {
		return nil
	}
	if !npc.Dead {
		s.npcAoi.Remove(npc.ID, npc.X, npc.Y, npc.MapID)
		s.entity.Vacate(npc.MapID, npc.X, npc.Y, npc.ID)
	}
	delete(s.npcs, npcID)
	// Remove from npcList (swap-delete for O(1))
	for i, n := range s.npcList {
		if n.ID == npcID {
			s.npcList[i] = s.npcList[len(s.npcList)-1]
			s.npcList = s.npcList[:len(s.npcList)-1]
			break
		}
	}
	return npc
}

// NpcDied 處理 NPC 死亡：釋放格子碰撞但保留 AOI（屍體需持續可見）。
// AOI 移除由 NpcCorpseCleanup 在 DeleteTimer 歸零後處理。
func (s *State) NpcDied(npc *NpcInfo) {
	s.entity.Vacate(npc.MapID, npc.X, npc.Y, npc.ID)
}

// NpcCorpseCleanup 從 NPC AOI 網格移除死亡 NPC（屍體消失階段）。
// 由 NpcRespawnSystem 在 DeleteTimer 歸零後呼叫。
func (s *State) NpcCorpseCleanup(npc *NpcInfo) {
	s.npcAoi.Remove(npc.ID, npc.X, npc.Y, npc.MapID)
}

// NpcRespawn re-adds a respawned NPC to the NPC AOI grid and entity grid.
// Call this after resetting the NPC's position and clearing Dead flag.
func (s *State) NpcRespawn(npc *NpcInfo) {
	s.npcAoi.Add(npc.ID, npc.X, npc.Y, npc.MapID)
	s.entity.Occupy(npc.MapID, npc.X, npc.Y, npc.ID)
}

// NpcList returns the full NPC list for tick iteration (spawn/respawn system).
func (s *State) NpcList() []*NpcInfo {
	return s.npcList
}

// NpcCount returns total NPC count.
func (s *State) NpcCount() int {
	return len(s.npcs)
}

// GetNearbyPlayersAt returns all players near a position (for NPC broadcasting).
func (s *State) GetNearbyPlayersAt(x, y int32, mapID int16) []*PlayerInfo {
	return s.GetNearbyPlayers(x, y, mapID, 0) // 0 = no exclude
}

// IsPlayerAt returns true if any alive player occupies the exact tile (excluding excludeSession).
func (s *State) IsPlayerAt(x, y int32, mapID int16, excludeSession uint64) bool {
	s.aoiBuf = s.aoi.GetNearbyInto(x, y, mapID, s.aoiBuf)
	nearbyIDs := s.aoiBuf
	for _, sid := range nearbyIDs {
		if sid == excludeSession {
			continue
		}
		p := s.bySession[sid]
		if p != nil && p.X == x && p.Y == y && p.MapID == mapID && !p.Dead {
			return true
		}
	}
	return false
}

// IsNpcAt returns true if any alive NPC occupies the exact tile.
// Uses NPC AOI grid for O(cells) lookup instead of O(N) full scan.
func (s *State) IsNpcAt(x, y int32, mapID int16) bool {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc != nil && npc.X == x && npc.Y == y && !npc.Dead {
			return true
		}
	}
	return false
}

// IsOccupied returns true if any alive entity (player or NPC) occupies the tile,
// excluding the given entity ID. O(1) lookup via EntityGrid.
func (s *State) IsOccupied(x, y int32, mapID int16, excludeID int32) bool {
	return s.entity.IsOccupied(mapID, x, y, excludeID)
}

// OccupantAt returns the first occupant entity ID at the tile, or 0 if empty.
func (s *State) OccupantAt(x, y int32, mapID int16) int32 {
	return s.entity.OccupantAt(mapID, x, y)
}

// VacateEntity removes an entity from the entity grid (for death, disconnect, etc.)
func (s *State) VacateEntity(mapID int16, x, y int32, entityID int32) {
	s.entity.Vacate(mapID, x, y, entityID)
}

// OccupyEntity adds an entity to the entity grid (for respawn, login, etc.)
func (s *State) OccupyEntity(mapID int16, x, y int32, entityID int32) {
	s.entity.Occupy(mapID, x, y, entityID)
}

// --- Door methods ---

// AddDoor registers a door in the world.
func (s *State) AddDoor(door *DoorInfo) {
	s.doors[door.ID] = door
	s.doorList = append(s.doorList, door)
}

// GetDoor returns a door by its object ID.
func (s *State) GetDoor(id int32) *DoorInfo {
	return s.doors[id]
}

// GetNearbyDoors returns all doors visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyDoors(x, y int32, mapID int16) []*DoorInfo {
	var result []*DoorInfo
	for _, door := range s.doors {
		if door.MapID != mapID {
			continue
		}
		dx := door.X - x
		dy := door.Y - y
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
		if dist <= 20 {
			result = append(result, door)
		}
	}
	return result
}

// GetDoorsByMap 回傳指定地圖上的所有門。
func (s *State) GetDoorsByMap(mapID int16) []*DoorInfo {
	var result []*DoorInfo
	for _, door := range s.doorList {
		if door.MapID == mapID {
			result = append(result, door)
		}
	}
	return result
}

// RemoveDoor removes a door by its object ID.
func (s *State) RemoveDoor(id int32) {
	if _, ok := s.doors[id]; !ok {
		return
	}
	delete(s.doors, id)
	for i, d := range s.doorList {
		if d.ID == id {
			s.doorList = append(s.doorList[:i], s.doorList[i+1:]...)
			break
		}
	}
}

// DoorCount returns total door count.
func (s *State) DoorCount() int {
	return len(s.doors)
}

// GetNearbyPets returns all alive pets visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyPets(x, y int32, mapID int16) []*PetInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	var result []*PetInfo
	for _, nid := range nearbyIDs {
		pet := s.pets[nid]
		if pet == nil || pet.Dead {
			continue
		}
		dx := pet.X - x
		dy := pet.Y - y
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
		if dist <= 20 {
			result = append(result, pet)
		}
	}
	return result
}

func (s *State) GetNearbyPetsInShow(x, y int32, mapID int16, showID int32) []*PetInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	var result []*PetInfo
	for _, nid := range nearbyIDs {
		pet := s.pets[nid]
		if pet == nil || pet.Dead || pet.ShowID != showID {
			continue
		}
		dx := pet.X - x
		dy := pet.Y - y
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
		if dist <= 20 {
			result = append(result, pet)
		}
	}
	return result
}

// --- Ground item methods ---

// AddGroundItem registers a ground item in the world.
func (s *State) AddGroundItem(item *GroundItem) {
	s.groundItems[item.ID] = item
}

// RemoveGroundItem removes a ground item from the world.
func (s *State) RemoveGroundItem(id int32) *GroundItem {
	item, ok := s.groundItems[id]
	if !ok {
		return nil
	}
	delete(s.groundItems, id)
	return item
}

// GetGroundItem returns a ground item by its object ID.
func (s *State) GetGroundItem(id int32) *GroundItem {
	return s.groundItems[id]
}

// GetNearbyGroundItems returns all ground items visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyGroundItems(x, y int32, mapID int16) []*GroundItem {
	var result []*GroundItem
	for _, item := range s.groundItems {
		if item.MapID != mapID {
			continue
		}
		dx := item.X - x
		dy := item.Y - y
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
		if dist <= 20 {
			result = append(result, item)
		}
	}
	return result
}

func (s *State) GetNearbyGroundItemsInShow(x, y int32, mapID int16, viewerShowID int32) []*GroundItem {
	var result []*GroundItem
	for _, item := range s.groundItems {
		if item.MapID != mapID || item.ShowID != viewerShowID {
			continue
		}
		dx := item.X - x
		dy := item.Y - y
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
		if dist <= 20 {
			result = append(result, item)
		}
	}
	return result
}

// TickGroundItems decrements TTL on ground items and returns expired ones.
func (s *State) TickGroundItems() []*GroundItem {
	var expired []*GroundItem
	for id, item := range s.groundItems {
		if item.NoExpire {
			continue
		}
		if item.TTL > 0 {
			item.TTL--
			if item.TTL <= 0 {
				expired = append(expired, item)
				delete(s.groundItems, id)
			}
		}
	}
	return expired
}

// --- Ground effect methods ---

func (s *State) AddGroundEffect(effect *GroundEffect) {
	s.groundEffects[effect.ID] = effect
	s.effectAoi.Add(effect.ID, effect.X, effect.Y, effect.MapID)
}

func (s *State) RemoveGroundEffect(id int32) *GroundEffect {
	effect, ok := s.groundEffects[id]
	if !ok {
		return nil
	}
	s.effectAoi.Remove(effect.ID, effect.X, effect.Y, effect.MapID)
	delete(s.groundEffects, id)
	return effect
}

func (s *State) GetGroundEffect(id int32) *GroundEffect {
	return s.groundEffects[id]
}

func (s *State) GroundEffectList() []*GroundEffect {
	result := make([]*GroundEffect, 0, len(s.groundEffects))
	for _, effect := range s.groundEffects {
		result = append(result, effect)
	}
	return result
}

func (s *State) GetNearbyGroundEffects(x, y int32, mapID int16) []*GroundEffect {
	s.effectAoiBuf = s.effectAoi.GetNearbyInto(x, y, mapID, s.effectAoiBuf)
	nearbyIDs := s.effectAoiBuf
	result := make([]*GroundEffect, 0, len(nearbyIDs))
	for _, id := range nearbyIDs {
		effect := s.groundEffects[id]
		if effect == nil || effect.MapID != mapID {
			continue
		}
		if chebyshevDistance32(effect.X, effect.Y, x, y) <= 20 {
			result = append(result, effect)
		}
	}
	return result
}

func (s *State) GetNearbyGroundEffectsInShow(x, y int32, mapID int16, showID int32) []*GroundEffect {
	s.effectAoiBuf = s.effectAoi.GetNearbyInto(x, y, mapID, s.effectAoiBuf)
	nearbyIDs := s.effectAoiBuf
	result := make([]*GroundEffect, 0, len(nearbyIDs))
	for _, id := range nearbyIDs {
		effect := s.groundEffects[id]
		if effect == nil || effect.MapID != mapID || effect.ShowID != showID {
			continue
		}
		if chebyshevDistance32(effect.X, effect.Y, x, y) <= 20 {
			result = append(result, effect)
		}
	}
	return result
}

func (s *State) HasGroundEffectAt(x, y int32, mapID int16, npcID int32) bool {
	s.effectAoiBuf = s.effectAoi.GetNearbyInto(x, y, mapID, s.effectAoiBuf)
	for _, id := range s.effectAoiBuf {
		effect := s.groundEffects[id]
		if effect != nil && effect.MapID == mapID && effect.X == x && effect.Y == y && effect.NpcID == npcID {
			return true
		}
	}
	return false
}

func (s *State) HasGroundEffectAtInShow(x, y int32, mapID int16, npcID int32, showID int32) bool {
	s.effectAoiBuf = s.effectAoi.GetNearbyInto(x, y, mapID, s.effectAoiBuf)
	for _, id := range s.effectAoiBuf {
		effect := s.groundEffects[id]
		if effect != nil && effect.MapID == mapID && effect.ShowID == showID && effect.X == x && effect.Y == y && effect.NpcID == npcID {
			return true
		}
	}
	return false
}

func (s *State) TickGroundEffects() []*GroundEffect {
	var expired []*GroundEffect
	for _, effect := range s.groundEffects {
		if effect.TicksLeft <= 0 {
			continue
		}
		effect.TicksLeft--
		if effect.TicksLeft <= 0 {
			expired = append(expired, effect)
		}
	}
	for _, effect := range expired {
		s.RemoveGroundEffect(effect.ID)
	}
	return expired
}

func chebyshevDistance32(x1, y1, x2, y2 int32) int32 {
	dx := x1 - x2
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y2
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return dy
	}
	return dx
}

// --- 家具 NPC 追蹤 ---

// GetFurnitureNpc 查詢道具 objectID 對應的家具 NPC objectID（0=不存在）。
func (s *State) GetFurnitureNpc(itemObjID int32) int32 {
	return s.furnitureNpcs[itemObjID]
}

// AddFurnitureNpc 註冊家具 NPC。
func (s *State) AddFurnitureNpc(itemObjID, npcObjID int32) {
	s.furnitureNpcs[itemObjID] = npcObjID
}

// RemoveFurnitureNpc 移除家具 NPC 追蹤。
func (s *State) RemoveFurnitureNpc(itemObjID int32) {
	delete(s.furnitureNpcs, itemObjID)
}
