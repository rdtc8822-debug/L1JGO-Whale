# L1JGO-Whale Development Guide

L1J 3.80C game server rewrite in Go. Reference Java codebase at `l1j_java/` for feature behavior only — do NOT copy its architecture.

## Development Workflow (MANDATORY)

Every feature MUST follow this order. No exceptions.

1. **Read Java first** — Find and fully read ALL related Java files (clientpackets, serverpackets, model classes). Understand packet format, every field, all switch/case branches, edge cases, and error handling.
2. **Document protocol constraints** — List what the 3.80C client dictates: packet structure, opcodes, type values, response formats, field order. These are immutable — the client binary cannot be changed.
3. **Design Go implementation** — Only NOW design using ECS/Lua/Event Bus architecture. The protocol layer must exactly match Java; the architecture layer is where Go improves.
4. **Write code** — Implement based on the above analysis.

**Why**: The 3.80C client is a fixed binary. Its packet formats, opcodes, and behavioral expectations are non-negotiable. Designing without reading Java first guarantees missed protocol details and wasted rework. The Java codebase is the ground truth for "what the client expects."

**Completeness over speed (MANDATORY)**: 寧願花更多時間去設計系統、徹底研究 Java 參考實現，也絕不交出半成品。每個功能必須先完整理解 Java 是怎麼實現的，再轉換成 Go。禁止只實現三分之一的功能然後留下三分之二的缺口等後續補齊。如果一個功能（例如負重系統）在 Java 中有 10 個面向，Go 實現就必須覆蓋全部 10 個面向。交付不完整的實現 = 浪費更多時間反查補齊，這是不可接受的。

## 開發後更新規則

每次完成一個功能或系統的開發後，必須將以下資訊更新到本文件的「系統介面參考」區塊：

1. Struct 名稱與所有 exported 欄位及型別
2. 所有 exported 方法/函式簽名（receiver, 參數, 回傳值）
3. Interface 定義
4. 該系統依賴的其他 package / struct
5. 重要的常數 / iota 定義

格式範例：

### [系統名稱] - package [package名]
```go
type Character struct {
    HP        int
    MP        int
    Level     int
    Inventory *Inventory
}

func (c *Character) TakeDamage(damage int, attacker *Character) error
func (c *Character) GetAttackPower() int

type Attacker interface {
    GetAttackPower() int
    TakeDamage(damage int, attacker *Character) error
}

const (
    MaxLevel = 99
    MaxHP    = 32767
)
```

依賴：`inventory`, `world`

⚠️ 禁止省略此步驟。


## Architecture Overview

```
Go Engine (networking, ECS, world, persistence)
  + Lua Scripts (ALL game logic: combat, skills, AI, quests)
  + YAML (static data: items, NPCs, skills, maps, shops)
  + TOML (runtime config: rates, server settings)
  + PostgreSQL (persistence, WAL for economic safety)
```

## Project Layout

```
server/
├── cmd/l1jgo/main.go                 # Entry point
├── internal/
│   ├── config/config.go              # TOML config struct + loader
│   ├── core/
│   │   ├── ecs/                      # Entity system (generics, no reflect)
│   │   │   ├── entity.go             # EntityID = index(32b) + generation(32b), EntityPool, free list
│   │   │   ├── component.go          # PtrComponentStore[T] generic typed map storage
│   │   │   ├── registry.go           # Removable interface, Registry (bulk cleanup on destroy)
│   │   │   ├── world.go              # World container, deferred destroy queue
│   │   │   └── query.go              # Each2[A,B], Each3[A,B,C] generic iterators
│   │   ├── event/                    # Double-buffered event bus (next-tick processing)
│   │   │   ├── bus.go                # Emit / SwapBuffers / ReadEvents
│   │   │   └── types.go              # EventType enum definitions
│   │   └── system/                   # System interface + Phase ordering
│   │       ├── system.go             # System interface, Phase enum
│   │       └── runner.go             # Ordered system execution
│   ├── net/                          # Network layer (goroutine per connection)
│   │   ├── server.go                 # TCP listener, session management
│   │   ├── session.go                # Session state machine (6 states), InQueue/OutQueue
│   │   ├── codec.go                  # Frame decoder: [2B LE length][payload], handles sticky/partial
│   │   ├── cipher.go                 # L1J 3.80C XOR rolling cipher (exact Java port)
│   │   └── packet/
│   │       ├── reader.go             # ReadC(byte) ReadH(uint16) ReadD(int32) ReadS(string)
│   │       ├── writer.go             # WriteC WriteH WriteD WriteS → Bytes()
│   │       ├── opcodes.go            # 94 client + 168 server opcode constants
│   │       └── registry.go           # Opcode → handler dispatch
│   ├── handler/                      # Packet handlers (grouped by feature)
│   ├── component/                    # ECS components (PURE DATA, NO METHODS)
│   ├── system/                       # ECS systems (game logic processors)
│   ├── world/                        # AOI grid, map data, pathfinding
│   ├── scripting/                    # Lua engine (Context-In / Commands-Out)
│   ├── persist/                      # PostgreSQL (WAL + batch), bcrypt, migrations
│   └── data/                         # YAML/TOML data loaders
├── config/server.toml
├── data/yaml/                        # Static game data
├── scripts/                          # Lua game logic
├── seed/                             # Dev test data + map files
├── go.mod
└── Makefile
```

## Critical Rules

### 1. Components = Pure Data, Zero Methods
```go
// CORRECT
type Inventory struct {
    Items  []ItemSlot
    Gold   int64
    Weight int32
}

// FORBIDDEN — components must NOT have methods
func (i *Inventory) AddItem(...) { ... }
```
All mutations happen in System functions only.

### 2. Single Game Loop Goroutine, Zero Locks
All game state lives in the game loop goroutine. No mutexes on ECS data.
Network IO runs in separate goroutines, communicating via bounded channels only.

### 3. Entity Lifecycle
- `EntityID` = lower 32 bits (index) + upper 32 bits (generation)
- Free list recycles indices; generation increments on destroy to invalidate stale refs
- `World.MarkForDestruction(id)` queues; `CleanupSystem` destroys at tick end
- `Registry.RemoveAll(id)` clears ALL component stores for that entity

### 4. Lua Integration: Context-In / Commands-Out
```
Go System → build ScriptContext → 1 crossing → Lua function
Lua function → compute → return commands table → 1 crossing → Go
Go System → execute []Command → modify ECS
```
- **Never** let Lua directly mutate Go state
- Every Lua call wrapped in `pcall` (Protect: true). Errors → log + skip, never crash
- Execution timeout via `context.WithDeadline` + `LState.SetContext`
- Single VM (not a pool). Hot-reload = build new Engine → atomic swap → old GC'd
- **Context types are scene-specific** — use `CombatContext`, `AIContext`, `SkillContext` etc., NOT one monolithic struct. Each only carries the fields that scene needs. Keeps allocation small and intent clear.
- **Lua tick budget monitoring** — track cumulative Lua execution time per tick. Log warning if > 50% of tick budget (100ms of 200ms). If AI becomes bottleneck, split AISystem to a second goroutine + second VM (planned escape hatch, not premature).

### 5. Event Bus: Next-Tick Only
- Events emitted in tick N are readable in tick N+1
- Double-buffered: `SwapBuffers()` at tick start
- **Never** immediate dispatch
- Use system execution ORDER for synchronous dependencies
- Use Event Bus ONLY for deferred cross-system side effects
- **Typed subscriptions** — use generic `Subscribe[T EventPayload](bus, func(T))` so each System only receives the event types it cares about. Compile-time type safety, no runtime type switch sprawl.

### 6. Persistence: Two-Tier Safety
| Tier | Use Case | Strategy |
|------|----------|----------|
| **WAL** | Trade, shop, auction, gold transfer | Sync write DB BEFORE modifying memory |
| **Batch** | Position, HP/MP, exp, buffs | Dirty flag + periodic flush (async) |

WAL flow: build entry → `WriteWAL()` → success? → modify memory. Fail? → cancel operation.

**WAL ↔ Batch coordination**: When batch flush commits a transaction, it atomically marks all corresponding WAL entries as `processed = TRUE` within the same DB transaction. On crash recovery, only `WHERE processed = FALSE` entries are replayed. This prevents double-execution of economic operations after restart.

### 7. Network Layer
- **Cipher**: `nil` until seed exchange. Handshake packets are plaintext.
- **Framing**: `[2 bytes LE length][encrypted payload]`. Use `io.ReadFull` for partial reads.
- **OutQueue backpressure**: Non-blocking send. Channel full → disconnect slow client. **Never** block game loop.
- **InQueue budget**: Max 32 packets per session per tick. Excess stays in channel for next tick.

### 8. Session States
```
Handshake → VersionOK → Authenticated → InWorld ⇄ ReturningToSelect
Any state → Disconnecting
```

## System Execution Order (Per Tick)

```
Phase 0 Input:       InputSystem           — drain packet queues
Phase 1 PreUpdate:   EventDispatchSystem   — process last tick's events
Phase 2 Update:      MovementSystem        — movement + speed validation
                     CombatSystem          — attacks (calls Lua)
                     SkillSystem           — skills (calls Lua)
                     AISystem              — NPC AI (calls Lua)
                     TradeSystem           — trade state machine
                     ShopSystem            — shop transactions
                     QuestSystem           — quest checks
                     ChatSystem            — message routing
                     BuffSystem            — buff tick/expire
Phase 3 PostUpdate:  RegenSystem           — HP/MP regen
                     SpawnSystem           — respawn timers
                     VisibilitySystem      — AOI grid updates
                     WeatherSystem         — game clock
Phase 4 Output:      OutputSystem          — build + send packets
Phase 5 Persist:     PersistenceSystem     — WAL flush + batch save
Phase 6 Cleanup:     CleanupSystem         — destroy queued entities
```

## Package Dependencies (for parallel AI agent development)

```
core/ecs/       → (none)
core/event/     → (none)
core/system/    → core/ecs
component/      → core/ecs (EntityID only)
net/            → (none, standalone)
handler/        → net/packet, core/ecs, component/
scripting/      → core/ecs (EntityID only)
persist/        → component/ (type definitions for mapping)
data/           → (none, standalone)
system/         → core/ecs, component/, scripting/, core/event/
scripts/*.lua   → Lua API contract only
data/yaml/*     → YAML schema contract only
```

Any AI agent can independently develop any package as long as it respects these import boundaries.

## Database Schema Key Points

- `accounts.password_hash` = bcrypt (VARCHAR(72), no separate salt column)
- `items.owner_id` = NULLABLE (ground items have no owner; ground items NOT persisted to DB)
- `character_quests` = separate table (NOT JSONB — needs querying and migration)
- `bookmarks` + `config` = JSONB in characters table (simple, rarely queried)
- `characters.deleted_at` = soft delete, hard delete after 7 days for level 30+
- **Character deletion preconditions**: Must reject if character is clan leader (transfer first), has active auction bids, or is in an active trade. Handler checks these BEFORE setting `deleted_at`.
- `economic_wal` = write-ahead log for crash recovery of economic transactions
- `economic_wal.processed` = marked TRUE inside batch flush transaction to prevent double-replay on crash
- Partial indexes used where possible (e.g., `WHERE clan_id IS NOT NULL`)

## Lua Script Conventions

- Combat formulas in `scripts/combat/` — return `[]Command`
- AI scripts in `scripts/ai/` — return movement/attack commands
- Lookup tables (STR/DEX hit/dmg, exp table) in `scripts/core/tables.lua` — avoids cross-boundary lookups
- Shop tax calculation in `scripts/npc/shop.lua` — multi-layer: castle + national + town + war + diad
- All scripts receive a `ctx` table with pre-packed data, return a commands array
- `API_VERSION` global set by Go engine — scripts can check compatibility

## Map Data (.s32 files)

L1J map files are binary `.s32` format containing tile flags (walkable, water, safe zone, etc.) and height data. Parsing is non-trivial — build an independent `cmd/mapparser/` tool early in Phase 2 to validate parsing correctness before integrating into `world/mapdata.go`. Test against known walkability in starter area (map 2005). Incorrect parsing = broken movement validation.

## TOML Config Sections

`config/server.toml` contains: `[server]`, `[database]`, `[persistence]`, `[network]`, `[rates]`, `[enchant]`, `[world]`, `[character]`, `[lua]`, `[anti_cheat]`, `[rate_limit]`, `[logging]`

## Development Phases

| Phase | Goal | Verification |
|-------|------|-------------|
| 1 | Login + character creation | Client connects → handshake → login → create char → char list |
| 2 | Enter world + movement + AOI | Move, see other players appear/disappear |
| 3 | NPC spawn + Lua + combat | Attack NPC, Lua damage formula, drops, exp, level up |
| 4 | Items + inventory + shops | Equip, buy/sell with tax, consumables |
| 5 | Skills + buffs + NPC AI | Cast spells, buff timing, mob fights back |
| 6 | Social + economic safety | Trade (WAL), chat, clans, parties, mail |
| 7 | Production quality | Anti-cheat, rate limit, graceful shutdown, metrics |

## Java Reference Files

Key files in `l1j_java/src/l1j/server/` for feature reference:
- `server/Cipher.java` — XOR cipher (port exactly)
- `server/ClientThread.java` — init packet format, seed exchange, frame reading
- `server/model/L1Attack.java` — combat formulas, STR/DEX lookup tables
- `server/model/skill/L1SkillUse.java` — skill system
- `server/model/L1World.java` — world storage, visibility (brute force)
- `server/model/Instance/L1MonsterInstance.java` — mob AI, aggro, hate list
- `server/model/shop/L1Shop.java` — shop system
- `server/model/L1TaxCalculator.java` — multi-layer tax
- `server/clientpackets/C_CreateChar.java` — character creation, initial stats arrays

## Opcode Naming Convention (V381)

All packet opcode constants MUST use the OpCodeKey names below. Do NOT use L1JTW/Java-style names.

### Server Opcodes

| Byte | Constant | OpCodeKey | Notes |
|------|----------|-----------|-------|
| 5 | S_OPCODE_ADD_INVENTORY_BATCH | S_ADD_INVENTORY_BATCH | S_InvList |
| 6 | S_OPCODE_DELETE_CHAR_OK | S_DELETE_CHAR_OK | |
| 8 | S_OPCODE_STATUS | S_STATUS | S_OwnCharStatus |
| 10 | S_OPCODE_MOVE_OBJECT | S_MOVE_OBJECT | |
| 22 | S_OPCODE_SOUND_EFFECT | S_SOUND_EFFECT | |
| 24 | S_OPCODE_CHANGE_ITEM_USE | S_CHANGE_ITEM_USE | |
| 30 | S_OPCODE_ATTACK | S_ATTACK | |
| 33 | S_OPCODE_MANA_POINT | S_MANA_POINT | S_MPUpdate |
| 37 | S_OPCODE_MAGIC_STATUS | S_MAGIC_STATUS | S_SPMR |
| 39 | S_OPCODE_HYPERTEXT | S_HYPERTEXT | |
| 40 | S_OPCODE_CHANGE_LIGHT | S_CHANGE_LIGHT | |
| 55 | S_OPCODE_EFFECT | S_EFFECT | S_SkillSoundGFX |
| 57 | S_OPCODE_REMOVE_INVENTORY | S_REMOVE_INVENTORY | |
| 64 | S_OPCODE_VOICE_CHAT | S_VOICE_CHAT | Also CharSynAck |
| 67 | S_OPCODE_TELL | S_TELL | |
| 70 | S_OPCODE_SELL_LIST | S_SELL_LIST | |
| 71 | S_OPCODE_MESSAGE_CODE | S_MESSAGE_CODE | S_ServerMsg |
| 81 | S_OPCODE_SAY | S_SAY | |
| 87 | S_OPCODE_PUT_OBJECT | S_381_PUT_OBJECT | S_CharPack |
| 92 | S_OPCODE_ADD_BOOKMARK | S_ADD_BOOKMARK | |
| 93 | S_OPCODE_CHARACTER_INFO | S_CHARACTER_INFO | S_CharPacks |
| 98 | S_OPCODE_CREATE_CHARACTER_CHECK | S_CREATE_CHARACTER_CHECK | |
| 100 | S_OPCODE_CHANGE_ITEM_DESC | S_CHANGE_ITEM_DESC | S_ItemName |
| 103 | S_OPCODE_DRUNKEN | S_DRUNKEN | |
| 113 | S_OPCODE_EXP | S_EXP | |
| 115 | S_OPCODE_WEATHER | S_WEATHER | |
| 119 | S_OPCODE_CHANGE_DESC | S_CHANGE_DESC | S_CharVisualUpdate |
| 120 | S_OPCODE_REMOVE_OBJECT | S_REMOVE_OBJECT | |
| 123 | S_OPCODE_TIME | S_TIME | S_GameTime |
| 127 | S_OPCODE_NEW_CHAR_INFO | S_NEW_CHAR_INFO | |
| 139 | S_OPCODE_VERSION_CHECK | S_VERSION_CHECK | S_ServerVersion |
| 150 | S_OPCODE_INITPACKET | (internal) | Init handshake |
| 158 | S_OPCODE_ACTION | S_ACTION | S_DoActionGFX |
| 164 | S_OPCODE_ADD_SPELL | S_ADD_SPELL | |
| 171 | S_OPCODE_INVISIBLE | S_INVISIBLE | |
| 174 | S_OPCODE_ABILITY_SCORES | S_ABILITY_SCORES | S_OwnCharAttrDef |
| 178 | S_OPCODE_NUM_CHARACTER | S_NUM_CHARACTER | S_CharAmount |
| 206 | S_OPCODE_WORLD | S_WORLD | S_MapID |
| 209 | S_OPCODE_CHANGE_ATTR | S_CHANGE_ATTR | |
| 223 | S_OPCODE_ENTER_WORLD_CHECK | S_ENTER_WORLD_CHECK | S_LoginToGame |
| 225 | S_OPCODE_HIT_POINT | S_HIT_POINT | S_HPUpdate |
| 227 | S_OPCODE_KICK | S_KICK | S_Disconnect |
| 233 | S_OPCODE_LOGIN_CHECK | S_LOGIN_CHECK | S_LoginResult |
| 243 | S_OPCODE_MESSAGE | S_MESSAGE | S_GlobalChat |
| 250 | S_OPCODE_EVENT | S_EVENT | S_PacketBox |
| 255 | S_OPCODE_SPEED | S_SPEED | S_SkillHaste |

### Client Opcodes

| Byte | Constant | OpCodeKey | Notes |
|------|----------|-----------|-------|
| 2 | C_OPCODE_ASK_XCHG | C_ASK_XCHG | C_Trade |
| 3 | C_OPCODE_DELETE_BOOKMARK | C_DELETE_BOOKMARK | |
| 4 | C_OPCODE_QUERY_BUDDY | C_QUERY_BUDDY | |
| 5 | C_OPCODE_DUEL | C_DUEL | C_Fight |
| 6 | C_OPCODE_USE_SPELL | C_USE_SPELL | |
| 7 | C_OPCODE_REQUEST_ROLL | C_REQUEST_ROLL | C_ChangeChar |
| 10 | C_OPCODE_PLATE | C_PLATE | |
| 11 | C_OPCODE_HYPERTEXT_INPUT_RESULT | C_HYPERTEXT_INPUT_RESULT | |
| 13 | C_OPCODE_WAREHOUSE_CONTROL | C_WAREHOUSE_CONTROL | |
| 14 | C_OPCODE_VERSION | C_VERSION | |
| 18 | C_OPCODE_UPLOAD_EMBLEM | C_UPLOAD_EMBLEM | |
| 19 | C_OPCODE_TAX | C_TAX | |
| 20 | C_OPCODE_PERSONAL_SHOP | C_PERSONAL_SHOP | |
| 23 | C_OPCODE_BOARD_LIST | C_BOARD_LIST | |
| 25 | C_OPCODE_DROP | C_DROP | |
| 26 | C_OPCODE_RETURN_SUMMON | C_RETURN_SUMMON | |
| 29 | C_OPCODE_MOVE | C_MOVE | C_MoveChar |
| 33 | C_OPCODE_LEAVE_PARTY | C_LEAVE_PARTY | |
| 34 | C_OPCODE_DIALOG | C_DIALOG | C_NpcTalk |
| 37 | C_OPCODE_ADD_XCHG | C_ADD_XCHG | |
| 39 | C_OPCODE_BUY_SPELL | C_BUY_SPELL | |
| 40 | C_OPCODE_CHAT | C_CHAT | C_ChatGlobal |
| 41 | C_OPCODE_OPEN | C_OPEN | C_Door |
| 43 | C_OPCODE_INVITE_PARTY_TARGET | C_INVITE_PARTY_TARGET | |
| 44 | C_OPCODE_WITHDRAW | C_WITHDRAW | |
| 45 | C_OPCODE_GIVE | C_GIVE | |
| 47 | C_OPCODE_QUERY_PERSONAL_SHOP | C_QUERY_PERSONAL_SHOP | |
| 50 | C_OPCODE_MARRIAGE | C_MARRIAGE | |
| 51 | C_OPCODE_CHECK_PK | C_CHECK_PK | |
| 52 | C_OPCODE_TELEPORT | C_TELEPORT | |
| 56 | C_OPCODE_DEPOSIT | C_DEPOSIT | |
| 61 | C_OPCODE_LEAVE_PLEDGE | C_LEAVE_PLEDGE | |
| 62 | C_OPCODE_THROW | C_THROW | |
| 63 | C_OPCODE_RANK_CONTROL | C_RANK_CONTROL | |
| 68 | C_OPCODE_WHO_PLEDGE | C_WHO_PLEDGE | |
| 69 | C_OPCODE_BAN_MEMBER | C_BAN_MEMBER | |
| 71 | C_OPCODE_ACCEPT_XCHG | C_ACCEPT_XCHG | |
| 72 | C_OPCODE_ALT_ATTACK | C_ALT_ATTACK | |
| 78 | C_OPCODE_PLEDGE_WATCH | C_PLEDGE_WATCH | |
| 84 | C_OPCODE_CREATE_CUSTOM_CHARACTER | C_CREATE_CUSTOM_CHARACTER | |
| 86 | C_OPCODE_CANCEL_XCHG | C_CANCEL_XCHG | |
| 87 | C_OPCODE_MAIL | C_MAIL | |
| 90 | C_OPCODE_TITLE | C_TITLE | |
| 95 | C_OPCODE_ALIVE | C_ALIVE | C_KeepAlive |
| 98 | C_OPCODE_VOICE_CHAT | C_VOICE_CHAT | |
| 103 | C_OPCODE_CHECK_INVENTORY | C_CHECK_INVENTORY | |
| 104 | C_OPCODE_NPC_ITEM_CONTROL | C_NPC_ITEM_CONTROL | |
| 112 | C_OPCODE_GET | C_GET | C_PickUpItem |
| 114 | C_OPCODE_BOARD_READ | C_BOARD_READ | |
| 118 | C_OPCODE_FIX | C_FIX | |
| 119 | C_OPCODE_LOGIN | C_LOGIN | |
| 120 | C_OPCODE_ACTION | C_ACTION | C_ExtCommand |
| 122 | C_OPCODE_QUIT | C_QUIT | |
| 123 | C_OPCODE_FAR_ATTACK | C_FAR_ATTACK | C_ArrowAttack |
| 125 | C_OPCODE_HACTION | C_HACTION | C_NpcAction |
| 129 | C_OPCODE_MERCENARYARRANGE | C_MERCENARYARRANGE | |
| 136 | C_OPCODE_SAY | C_SAY | C_Chat |
| 137 | C_OPCODE_ENTER_WORLD | C_ENTER_WORLD | C_LoginToServer |
| 138 | C_OPCODE_DESTROY_ITEM | C_DESTROY_ITEM | |
| 141 | C_OPCODE_BOARD_WRITE | C_BOARD_WRITE | |
| 145 | C_OPCODE_BUYABLE_SPELL | C_BUYABLE_SPELL | |
| 153 | C_OPCODE_BOARD_DELETE | C_BOARD_DELETE | |
| 161 | C_OPCODE_BUY_SELL | C_BUY_SELL | |
| 162 | C_OPCODE_DELETE_CHARACTER | C_DELETE_CHARACTER | |
| 164 | C_OPCODE_USE_ITEM | C_USE_ITEM | |
| 165 | C_OPCODE_BOOKMARK | C_BOOKMARK | |
| 171 | C_OPCODE_EXCLUDE | C_EXCLUDE | |
| 173 | C_OPCODE_EXIT_GHOST | C_EXIT_GHOST | |
| 177 | C_OPCODE_RESTART | C_RESTART | |
| 184 | C_OPCODE_TELL | C_TELL | C_ChatWhisper |
| 185 | C_OPCODE_SUMMON | C_SUMMON | |
| 194 | C_OPCODE_JOIN_PLEDGE | C_JOIN_PLEDGE | |
| 199 | C_OPCODE_CHAT_PARTY_CONTROL | C_CHAT_PARTY_CONTROL | |
| 202 | C_OPCODE_REMOVE_BUDDY | C_REMOVE_BUDDY | |
| 206 | C_OPCODE_WHO | C_WHO | |
| 207 | C_OPCODE_ADD_BUDDY | C_ADD_BUDDY | |
| 210 | C_OPCODE_SHIFT_SERVER | C_SHIFT_SERVER | C_BeanFunLogin |
| 219 | C_OPCODE_ENTER_PORTAL | C_ENTER_PORTAL | |
| 222 | C_OPCODE_CREATE_PLEDGE | C_CREATE_PLEDGE | |
| 223 | C_OPCODE_SLAVE_CONTROL | C_SLAVE_CONTROL | |
| 225 | C_OPCODE_CHANGE_DIRECTION | C_CHANGE_DIRECTION | |
| 227 | C_OPCODE_WAR | C_WAR | |
| 229 | C_OPCODE_ATTACK | C_ATTACK | |
| 230 | C_OPCODE_WHO_PARTY | C_WHO_PARTY | |
| 231 | C_OPCODE_ENTER_SHIP | C_ENTER_SHIP | |
| 244 | C_OPCODE_SAVEIO | C_SAVEIO | |
| 245 | C_OPCODE_EXCHANGEABLE_SPELL | C_EXCHANGEABLE_SPELL | |
| 253 | C_OPCODE_SHUTDOWN | C_SHUTDOWN | |
| 254 | C_OPCODE_FIXABLE_ITEM | C_FIXABLE_ITEM | C_SendLocation |
| 255 | C_OPCODE_BANISH_PARTY | C_BANISH_PARTY | |

## Tech Stack

| Purpose | Package |
|---------|---------|
| Lua VM | `github.com/yuin/gopher-lua` |
| PostgreSQL | `github.com/jackc/pgx/v5` |
| YAML | `gopkg.in/yaml.v3` |
| TOML | `github.com/BurntSushi/toml` |
| Logging | `go.uber.org/zap` |
| DB Migration | `github.com/pressly/goose/v3` |
| File Watch | `github.com/fsnotify/fsnotify` |
| Password | `golang.org/x/crypto/bcrypt` |
| Metrics | `github.com/prometheus/client_golang` |

## 系統介面參考

### 血盟系統 (Clan/Pledge) - package world, persist, handler

```go
// world/clan.go

const (
    ClanRankPublic    int16 = 7  // 一般成員
    ClanRankProbation int16 = 8  // 見習成員
    ClanRankGuardian  int16 = 9  // 守護騎士
    ClanRankPrince    int16 = 10 // 君主（盟主）
)

type ClanMember struct {
    CharID   int32
    CharName string
    Rank     int16
    Notes    []byte // up to 62 bytes Big5
}

type ClanInfo struct {
    ClanID       int32
    ClanName     string
    LeaderID     int32
    LeaderName   string
    FoundDate    int32
    HasCastle    int32
    HasHouse     int32
    Announcement []byte // up to 478 bytes Big5
    EmblemID     int32
    EmblemStatus int16
    Members      map[int32]*ClanMember
}

func (c *ClanInfo) MemberCount() int

type ClanManager struct { /* unexported maps */ }

func NewClanManager() *ClanManager
func (m *ClanManager) GetClan(clanID int32) *ClanInfo
func (m *ClanManager) GetClanByName(name string) *ClanInfo
func (m *ClanManager) GetPlayerClanID(charID int32) int32
func (m *ClanManager) ClanNameExists(name string) bool
func (m *ClanManager) ClanCount() int
func (m *ClanManager) IsLeader(charID int32) bool
func (m *ClanManager) AddClan(clan *ClanInfo)
func (m *ClanManager) RemoveClan(clanID int32)
func (m *ClanManager) AddMember(clanID int32, member *ClanMember)
func (m *ClanManager) RemoveMember(clanID, charID int32)
```

```go
// persist/clan_repo.go

type ClanRow struct {
    ClanID, LeaderID, FoundDate, HasCastle, HasHouse, EmblemID int32
    ClanName, LeaderName string
    Announcement []byte
    EmblemStatus int16
}

type ClanMemberRow struct {
    ClanID, CharID int32
    CharName string
    Rank int16
    Notes []byte
}

type ClanRepo struct { /* unexported */ }

func NewClanRepo(db *DB) *ClanRepo
func (r *ClanRepo) LoadAll(ctx context.Context) ([]ClanRow, []ClanMemberRow, error)
func (r *ClanRepo) CreateClan(ctx context.Context, leaderCharID int32, leaderName, clanName string, foundDate, adenaCost int32) (clanID int32, err error)
func (r *ClanRepo) AddMember(ctx context.Context, clanID int32, clanName string, charID int32, charName string, rank int16) error
func (r *ClanRepo) RemoveMember(ctx context.Context, clanID, charID int32) error
func (r *ClanRepo) DissolveClan(ctx context.Context, clanID int32) error
func (r *ClanRepo) UpdateAnnouncement(ctx context.Context, clanID int32, announcement []byte) error
func (r *ClanRepo) UpdateMemberNotes(ctx context.Context, clanID, charID int32, notes []byte) error
func (r *ClanRepo) UpdateMemberRank(ctx context.Context, clanID, charID int32, rank int16) error
func (r *ClanRepo) LoadOfflineCharClan(ctx context.Context, charName string) (charID, clanID int32, clanName string, clanRank int16, err error)

var ErrInsufficientGold error
```

```go
// handler/clan.go — 封包處理器

func HandleCreateClan(sess *net.Session, r *packet.Reader, deps *Deps)  // C_CREATE_PLEDGE (222)
func HandleJoinClan(sess *net.Session, r *packet.Reader, deps *Deps)    // C_JOIN_PLEDGE (194)
func HandleLeaveClan(sess *net.Session, r *packet.Reader, deps *Deps)   // C_LEAVE_PLEDGE (61)
func HandleBanMember(sess *net.Session, r *packet.Reader, deps *Deps)   // C_BAN_MEMBER (69)
func HandleWhoPledge(sess *net.Session, r *packet.Reader, deps *Deps)   // C_WHO_PLEDGE (68)
func HandlePledgeWatch(sess *net.Session, r *packet.Reader, deps *Deps) // C_PLEDGE_WATCH (78)
func HandleRankControl(sess *net.Session, r *packet.Reader, deps *Deps) // C_RANK_CONTROL (63)
func HandleClanJoinResponse(sess *net.Session, responder *world.PlayerInfo, applicantCharID int32, accepted bool, deps *Deps) // from C_ATTR case 97

// 封包建構函式
func sendClanName(sess *net.Session, objID int32, clanName string, clanID int32, join bool)      // S_ClanName (72)
func sendCharTitle(sess *net.Session, objID int32, title string)                                   // S_CharTitle (183)
func sendClanAttention(sess *net.Session)                                                          // S_ClanAttention (200)
func sendPledgeAnnounce(sess *net.Session, clan *world.ClanInfo)                                   // S_PacketBox(167)
func sendPledgeMembers(sess *net.Session, clan *world.ClanInfo, deps *Deps, onlineOnly bool)       // S_PacketBox(170/171)
```

依賴：`world`, `persist`, `net/packet`, `handler/context.Deps`

### 新增的伺服器操作碼

| Byte | Constant | 用途 |
|------|----------|------|
| 72 | S_OPCODE_CLANNAME | 血盟名稱顯示更新 |
| 183 | S_OPCODE_CHARTITLE | 角色稱號更新 |
| 200 | S_OPCODE_CLANATTENTION | 血盟狀態變更通知 |

### DB 遷移：007_clans.sql

- `clans` 表：clan_id (PK), clan_name (UNIQUE), leader_id, leader_name, found_date, announcement (BYTEA), emblem_id
- `clan_members` 表：(clan_id, char_id) PK, char_name, rank, notes (BYTEA)

### 變身系統 (Polymorph) - package data, handler, world

```go
// data/polymorph.go

type PolymorphInfo struct {
    PolyID      int32  // GFX sprite ID (also used as lookup key)
    Name        string // monster name for monlist lookup
    MinLevel    int    // minimum player level required
    WeaponEquip int    // weapon bitmask (0 = all weapons forbidden)
    ArmorEquip  int    // armor bitmask (0 = all armor forbidden)
    CanUseSkill bool   // false = cannot cast spells while polymorphed
    Cause       int    // trigger bitmask: 1=magic, 2=GM, 4=NPC, 8=keplisha
}

func (p *PolymorphInfo) IsWeaponEquipable(weaponType string) bool
func (p *PolymorphInfo) IsArmorEquipable(armorType string) bool
func (p *PolymorphInfo) IsMatchCause(cause int) bool // cause=0 bypasses

type PolymorphTable struct { /* byID map[int32]*, byName map[string]* */ }

func LoadPolymorphTable(path string) (*PolymorphTable, error)
func (t *PolymorphTable) GetByID(polyID int32) *PolymorphInfo
func (t *PolymorphTable) GetByName(name string) *PolymorphInfo  // case-insensitive
func (t *PolymorphTable) Count() int

// Weapon equip bitmask constants
const (
    PolyWeaponDagger      = 1
    PolyWeaponSword       = 2
    PolyWeaponTwoHandSword = 4
    PolyWeaponAxe         = 8
    PolyWeaponSpear       = 16
    PolyWeaponStaff       = 32
    PolyWeaponEdoryu      = 64
    PolyWeaponClaw        = 128
    PolyWeaponBow         = 256   // also gauntlet
    PolyWeaponKiringku    = 512
    PolyWeaponChainSword  = 1024
)

// Armor equip bitmask constants
const (
    PolyArmorHelm    = 1
    PolyArmorArmor   = 2
    PolyArmorTShirt  = 4
    PolyArmorCloak   = 8
    PolyArmorGlove   = 16
    PolyArmorBoots   = 32
    PolyArmorShield  = 64
    PolyArmorAmulet  = 128
    PolyArmorRingL   = 256
    PolyArmorRingR   = 512
    PolyArmorBelt    = 1024
    PolyArmorGuarder = 2048
)

// Cause bitmask constants
const (
    PolyCauseMagic = 1
    PolyCauseGM    = 2
    PolyCauseNPC   = 4
)
```

```go
// handler/polymorph.go

const SkillShapeChange int32 = 67

// Polymorph scroll item IDs
const (
    ItemPolyScroll        int32 = 40088  // 30 min
    ItemIvoryTowerPoly    int32 = 40096  // 30 min
    ItemWelfarePolyPotion int32 = 49308  // random 40-80 min
    ItemBlessedPolyScroll int32 = 140088 // 35 min
)

func IsPolyScroll(itemID int32) bool
func PlayerGfx(p *world.PlayerInfo) int32  // TempCharGfx > 0 ? TempCharGfx : ClassID
func DoPoly(player *world.PlayerInfo, polyID int32, durationSec int, cause int, deps *Deps)
func UndoPoly(player *world.PlayerInfo, deps *Deps)
func HandleHypertextInputResult(sess *net.Session, r *packet.Reader, deps *Deps)  // C_HYPERTEXT_INPUT_RESULT (11)

// 封包建構函式 (unexported)
// handlePolyScroll(sess, r, player, invItem, deps)    // C_USE_ITEM 變身卷軸處理 (讀 [S monsterName])
// polyScrollDuration(itemID) int                       // 依卷軸類型回傳持續秒數
// sendChangeShape(viewer, objID, polyGfx, weapon)      // S_ChangeShape (76): [D objID][H polyGfx][C weapon][C 0xff][C 0xff]
// sendPolyIcon(sess, durationSec)                      // S_PacketBox sub 35 (polymorph timer icon)
// sendShowPolyList(sess, charID)                       // S_HYPERTEXT "monlist" (polymorph selection dialog)
// forceUnequipIncompat(player, poly, deps)             // force unequip incompatible items
```

```go
// world/state.go — PlayerInfo 新增欄位

TempCharGfx int32 // 0=use ClassID; >0=current polymorph GFX sprite
PolyID      int32 // current polymorph poly_id (for equip/skill checks; 0=not polymorphed)
```

依賴：`data`, `world`, `net/packet`, `handler/context.Deps`

### 新增的伺服器操作碼（變身）

| Byte | Constant | 用途 |
|------|----------|------|
| 76 | S_OPCODE_POLY | S_ChangeShape 變身外觀變更 |

### 變身系統影響的現有處理器

| 檔案 | 修改內容 |
|------|---------|
| handler/broadcast.go | sendPutObject 使用 PlayerGfx() 取代 ClassID |
| handler/skill.go | skill 67 特殊路由開啟 monlist；buff 過期/取消呼叫 UndoPoly；施法限制檢查 |
| handler/item.go | 裝備武器/防具時檢查變身相容性；HandleUseItem 路由變身卷軸 |
| handler/gmcommand.go | .poly / .undopoly GM 指令 |
| handler/context.go | Deps.Polys *data.PolymorphTable；註冊 C_HYPERTEXT_INPUT_RESULT |

### YAML 資料：data/yaml/polymorph_list.yaml

297 筆變身形態，格式：
```yaml
polymorphs:
  - poly_id: 29
    name: "floating eye"
    min_level: 1
    weapon_equip: 0
    armor_equip: 0
    can_use_skill: true
    cause: 7
```

### 組隊系統 (Party) - package world, handler

```go
// world/party.go

const MaxPartySize     = 8
const MaxChatPartySize = 8

type PartyType byte
const (
    PartyTypeNormal    PartyType = 0
    PartyTypeAutoShare PartyType = 1
)

type PartyInfo struct {
    LeaderID  int32
    Members   []int32
    PartyType PartyType
}

type PartyManager struct { /* unexported */ }

func NewPartyManager() *PartyManager
func (m *PartyManager) GetParty(charID int32) *PartyInfo
func (m *PartyManager) IsInParty(charID int32) bool
func (m *PartyManager) IsLeader(charID int32) bool
func (m *PartyManager) CreateParty(leaderID, memberID int32, pType PartyType) *PartyInfo
func (m *PartyManager) AddMember(partyID, charID int32) bool
func (m *PartyManager) RemoveMember(charID int32) *PartyInfo
func (m *PartyManager) Dissolve(partyID int32)
func (m *PartyManager) SetLeader(oldLeaderID, newLeaderID int32)
func (m *PartyManager) SetInvite(targetID, inviterID int32)
func (m *PartyManager) GetInvite(targetID int32) int32
func (m *PartyManager) ClearInvite(targetID int32)

type ChatPartyInfo struct {
    LeaderID int32
    Members  []int32
}

type ChatPartyManager struct { /* unexported */ }

func NewChatPartyManager() *ChatPartyManager
func (m *ChatPartyManager) GetParty(charID int32) *ChatPartyInfo
func (m *ChatPartyManager) IsInParty(charID int32) bool
func (m *ChatPartyManager) IsLeader(charID int32) bool
func (m *ChatPartyManager) CreateParty(leaderID, memberID int32) *ChatPartyInfo
func (m *ChatPartyManager) AddMember(partyID, charID int32) bool
func (m *ChatPartyManager) RemoveMember(charID int32) *ChatPartyInfo
func (m *ChatPartyManager) Dissolve(partyID int32)
func (m *ChatPartyManager) MembersNameList(partyID int32, getPlayerName func(int32) string) string

func CalcPartyHP(hp, maxHP int16) byte    // 0-10 proportional, 0xFF = not in party
func CalcHPPercent(hp, maxHP int16) byte   // 0-100 percentage
```

```go
// world/state.go — PlayerInfo 組隊相關欄位

PartyID           int32  // 0=not in party
PartyLeader       bool
PendingYesNoType  int16  // 0=none, 951=chat party, 953=normal, 954=auto-share, 252=trade
PendingYesNoData  int32  // inviter CharID
PartyInviteType   byte   // 0=normal, 1=auto-share
PartyRefreshTicks int    // 25-second refresh cycle counter
```

```go
// handler/party.go

func HandleInviteParty(sess *net.Session, r *packet.Reader, deps *Deps)            // C_WHO_PARTY (230)
func HandleWhoParty(sess *net.Session, _ *packet.Reader, deps *Deps)               // C_INVITE_PARTY_TARGET (43)
func HandleLeaveParty(sess *net.Session, _ *packet.Reader, deps *Deps)             // C_LEAVE_PARTY (33)
func HandleBanishParty(sess *net.Session, r *packet.Reader, deps *Deps)            // C_BANISH_PARTY (255)
func HandlePartyControl(sess *net.Session, r *packet.Reader, deps *Deps)           // C_CHAT_PARTY_CONTROL (199)
func HandlePartyInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool, deps *Deps)     // from C_Attr case 953/954
func HandleChatPartyInviteResponse(player *world.PlayerInfo, inviterID int32, accepted bool, deps *Deps) // from C_Attr case 951

func UpdatePartyMiniHP(player *world.PlayerInfo, deps *Deps)    // 更新隊伍 HP 條
func RefreshPartyPositions(player *world.PlayerInfo, deps *Deps) // 25 秒位置刷新

// 封包建構函式 (unexported)
// sendYesNoDialog(sess, msgType, args...)          // S_Message_YN (219)
// sendHpMeter(viewer, objectID, hpRatio)           // S_HP_METER (237)
// sendPacketBoxFullPartyList(sess, party, deps)    // S_PacketBox sub 104
// sendPacketBoxNewMember(sess, newMember)           // S_PacketBox sub 105
// sendPacketBoxSetMaster(sess, newLeaderID)         // S_PacketBox sub 106
// sendPacketBoxPartyRefresh(sess, party, deps)      // S_PacketBox sub 110
```

依賴：`world`, `net/packet`, `handler/context.Deps`

### NPC AI 系統 - package world, data, scripting, main

```go
// world/npc.go — NpcInfo AI 相關欄位

type NpcInfo struct {
    // ... identity, position, stats 略 ...
    Agro         bool   // true = 主動攻擊
    AtkDmg       int32  // Level + STR/3
    Ranged       int16  // 1=melee, >1=ranged
    AtkSpeed     int16  // ms
    MoveSpeed    int16  // ms

    Dead         bool
    DeleteTimer  int    // ticks until S_RemoveObject (default 50 = 10s)
    RespawnTimer int

    // AI State
    AggroTarget  uint64 // SessionID of hate target (0 = no target)
    AttackTimer  int    // attack cooldown ticks
    MoveTimer    int    // move cooldown ticks
    WanderDist   int
    WanderDir    int16
    WanderTimer  int
}

func NextNpcID() int32  // 起始 200,000,000，避免與角色 ID 衝突
```

```go
// data/mobskill.go

type MobSkill struct {
    ActNo         int `yaml:"act_no"`
    Type          int `yaml:"type"`
    MpConsume     int `yaml:"mp_consume"`
    TriggerRandom int `yaml:"trigger_random"` // 0-100
    TriggerHP     int `yaml:"trigger_hp"`     // HP% threshold
    TriggerRange  int `yaml:"trigger_range"`
    SkillID       int `yaml:"skill_id"`
    ActID         int `yaml:"act_id"`
    Leverage      int `yaml:"leverage"`
    GfxID         int `yaml:"gfx_id"`
    SkillArea     int `yaml:"skill_area"`
}

type MobSkillTable struct { /* skills map[int32][]MobSkill */ }

func LoadMobSkillTable(path string) (*MobSkillTable, error)
func (t *MobSkillTable) Get(npcID int32) []MobSkill
func (t *MobSkillTable) Count() int
```

```go
// scripting/engine.go — AI 橋接

type AIContext struct {
    NpcID, X, Y, MapID              int
    HP, MaxHP, MP, MaxMP            int
    Level, AtkDmg, AtkSpeed, MoveSpeed, Ranged int
    Agro                            bool
    TargetID, TargetX, TargetY, TargetDist, TargetAC, TargetLevel int
    CanAttack, CanMove              bool
    Skills                          []MobSkillEntry
    WanderDist, SpawnDist           int
}

type MobSkillEntry struct {
    SkillID, MpConsume, TriggerRandom, TriggerHP, TriggerRange, ActID, GfxID int
}

type AICommand struct {
    Type    string // "attack","ranged_attack","skill","move_toward","wander","lose_aggro","idle"
    SkillID int
    ActID   int
    GfxID   int
    Dir     int // heading 0-7, -1=continue, -2=toward spawn
}

func (e *Engine) RunNpcAI(ctx AIContext) []AICommand
func (e *Engine) CalcNpcMelee(ctx CombatContext) CombatResult
func (e *Engine) CalcNpcRanged(ctx CombatContext) CombatResult
```

```go
// cmd/l1jgo/main.go — AI tick 函式

func tickNpcAI(ws *world.State, deps *handler.Deps)
func tickNpcRespawn(ws *world.State, maps *data.MapDataTable)

// NPC 動作函式 (unexported)
// npcMeleeAttack(ws, npc, target, deps)
// npcRangedAttack(ws, npc, target, deps)
// executeNpcSkill(ws, npc, target, skillID, actID, gfxID, deps)
// npcMoveToward(ws, npc, tx, ty, maps)
// npcWander(ws, npc, dir, maps)
// setNpcAtkCooldown(npc)       // max(3, AtkSpeed/200)
// chebyshev32(x1,y1,x2,y2)    // Chebyshev distance
// calcNpcHeading(sx,sy,tx,ty)  // heading 0-7
```

Lua AI 腳本：`scripts/ai/default.lua`
- `npc_ai(ctx)` → 主決策函式，回傳 AICommand 陣列
- 仇恨距離 > 15 tiles → lose_aggro
- 主動怪偵測範圍 = 8 tiles (Chebyshev)
- 技能觸發條件：HP 門檻、距離、MP、機率

依賴：`world`, `data`, `scripting`, `handler/context.Deps`

### 網路/Session 層 - package net, net/packet

```go
// net/session.go

type Session struct {
    ID          uint64
    InQueue     chan []byte
    OutQueue    chan []byte
    IP          string
    AccountName string
    CharName    string
}

func NewSession(conn net.Conn, id uint64, inSize, outSize int, log *zap.Logger) *Session
func (s *Session) State() packet.SessionState
func (s *Session) SetState(st packet.SessionState)
func (s *Session) Start()
func (s *Session) Send(data []byte)
func (s *Session) Close()
func (s *Session) IsClosed() bool
```

```go
// net/server.go

type Server struct { /* unexported */ }

func NewServer(bindAddr string, inSize, outSize int, log *zap.Logger) (*Server, error)
func (s *Server) AcceptLoop()
func (s *Server) NewSessions() <-chan *Session
func (s *Server) NotifyDead(sessionID uint64)
func (s *Server) DeadSessions() <-chan uint64
func (s *Server) Shutdown()
func (s *Server) Addr() net.Addr
```

```go
// net/cipher.go

type Cipher struct { /* unexported: eb, db [8]byte, tb [4]byte */ }

func NewCipher(seed int32) *Cipher
func (c *Cipher) Encrypt(data []byte) []byte
func (c *Cipher) Decrypt(data []byte) []byte
```

```go
// net/codec.go

func ReadFrame(r io.Reader) ([]byte, error)   // [2B LE len][payload]
func WriteFrame(w io.Writer, data []byte) error
```

```go
// net/packet/reader.go

type Reader struct { /* unexported */ }

func NewReader(data []byte) *Reader
func (r *Reader) Opcode() byte
func (r *Reader) ReadC() byte
func (r *Reader) ReadH() uint16
func (r *Reader) ReadD() int32
func (r *Reader) ReadS() string          // null-terminated MS950 → UTF-8
func (r *Reader) ReadBytes(n int) []byte
func (r *Reader) Remaining() int
```

```go
// net/packet/writer.go

type Writer struct { /* unexported */ }

func NewWriter() *Writer
func NewWriterWithOpcode(opcode byte) *Writer
func (w *Writer) WriteC(v byte)
func (w *Writer) WriteH(v uint16)
func (w *Writer) WriteD(v int32)
func (w *Writer) WriteDU(v uint32)
func (w *Writer) WriteS(s string)          // UTF-8 → MS950 null-terminated
func (w *Writer) WriteBytes(b []byte)
func (w *Writer) Bytes() []byte            // padded to 4-byte boundary
func (w *Writer) RawBytes() []byte
func (w *Writer) Len() int
```

```go
// net/packet/registry.go

type SessionState int
const (
    StateHandshake         SessionState = iota // 0
    StateVersionOK                             // 1
    StateAuthenticated                         // 2
    StateInWorld                               // 3
    StateReturningToSelect                     // 4
    StateDisconnecting                         // 5
)

type HandlerFunc func(sess any, r *Reader)
type Registry struct { /* unexported */ }

func NewRegistry(log *zap.Logger) *Registry
func (reg *Registry) Register(opcode byte, states []SessionState, fn HandlerFunc)
func (reg *Registry) Dispatch(sess any, state SessionState, data []byte) error
```

依賴：無（standalone package）

### 持久化層 - package persist

```go
// persist/db.go

type DB struct {
    Pool *pgxpool.Pool
}

func NewDB(ctx context.Context, cfg config.DatabaseConfig, log *zap.Logger) (*DB, error)
func (db *DB) Close()
```

```go
// persist/account_repo.go

type AccountRow struct {
    Name, PasswordHash, IP, Host string
    AccessLevel, CharacterSlot   int16
    Banned, Online               bool
    CreatedAt                    time.Time
    LastActive                   *time.Time
}

type AccountRepo struct { /* unexported */ }

func NewAccountRepo(db *DB) *AccountRepo
func (r *AccountRepo) Load(ctx context.Context, name string) (*AccountRow, error)
func (r *AccountRepo) Create(ctx context.Context, name, rawPassword, ip, host string) (*AccountRow, error)
func (r *AccountRepo) ValidatePassword(hash, rawPassword string) bool
func (r *AccountRepo) UpdateLastActive(ctx context.Context, name, ip string) error
func (r *AccountRepo) SetOnline(ctx context.Context, name string, online bool) error
```

```go
// persist/character_repo.go

type CharacterRow struct {
    ID                int32
    AccountName, Name string
    ClassType, Sex    int16
    ClassID           int32
    Str, Dex, Con, Wis, Cha, Intel int16
    Level             int16
    Exp               int64
    HP, MP, MaxHP, MaxMP int16
    AC                int16
    X, Y              int32
    MapID, Heading    int16
    Lawful            int32
    Title, ClanName   string
    ClanID            int32
    ClanRank          int16
    PKCount, Karma    int32
    BonusStats, ElixirStats int16
    PartnerID         int32
    Food, HighLevel, AccessLevel int16
    Birthday          int32
    DeletedAt         *time.Time
}

type BookmarkRow struct {
    ID    int32  `json:"id"`
    Name  string `json:"name"`
    X, Y  int32  `json:"x,y"`
    MapID int16  `json:"map_id"`
}

type CharacterRepo struct { /* unexported */ }

func NewCharacterRepo(db *DB) *CharacterRepo
func (r *CharacterRepo) LoadByAccount(ctx context.Context, accountName string) ([]CharacterRow, error)
func (r *CharacterRepo) Create(ctx context.Context, c *CharacterRow) error
func (r *CharacterRepo) NameExists(ctx context.Context, name string) (bool, error)
func (r *CharacterRepo) CountByAccount(ctx context.Context, accountName string) (int, error)
func (r *CharacterRepo) SoftDelete(ctx context.Context, name string) error
func (r *CharacterRepo) HardDelete(ctx context.Context, name string) error
func (r *CharacterRepo) CleanExpiredDeletions(ctx context.Context, accountName string) (int64, error)
func (r *CharacterRepo) SavePosition(ctx context.Context, name string, x, y int32, mapID, heading int16) error
func (r *CharacterRepo) SaveCharacter(ctx context.Context, c *CharacterRow) error
func (r *CharacterRepo) LoadBookmarks(ctx context.Context, name string) ([]BookmarkRow, error)
func (r *CharacterRepo) SaveBookmarks(ctx context.Context, name string, bookmarks []BookmarkRow) error
func (r *CharacterRepo) LoadKnownSpells(ctx context.Context, name string) ([]int32, error)
func (r *CharacterRepo) SaveKnownSpells(ctx context.Context, name string, spells []int32) error
func (r *CharacterRepo) LoadByName(ctx context.Context, name string) (*CharacterRow, error)
```

```go
// persist/item_repo.go

type ItemRow struct {
    ID, CharID, ItemID, ObjID int32
    Count                     int32
    EnchantLvl, Bless         int16
    Equipped, Identified      bool
    EquipSlot                 int16
}

type ItemRepo struct { /* unexported */ }

func NewItemRepo(db *DB) *ItemRepo
func (r *ItemRepo) LoadByCharID(ctx context.Context, charID int32) ([]ItemRow, error)
func (r *ItemRepo) MaxObjID(ctx context.Context) (int32, error)
func (r *ItemRepo) SaveInventory(ctx context.Context, charID int32, inv *world.Inventory, equip *world.Equipment) error
```

```go
// persist/wal.go

type WALEntry struct {
    TxType              string // "trade", "shop", "auction"
    FromChar, ToChar    int32
    ItemID, Count       int32
    EnchantLvl          int16
    GoldAmount          int64
}

type WALRepo struct { /* unexported */ }

func NewWALRepo(db *DB) *WALRepo
func (r *WALRepo) WriteWAL(ctx context.Context, entries []WALEntry) error
func (r *WALRepo) MarkProcessed(ctx context.Context) error
```

```go
// persist/warehouse_repo.go

type WarehouseItem struct {
    ID          int32
    AccountName, CharName string
    WhType      int16 // 3=personal, 4=elf, 5=clan
    ItemID, Count int32
    EnchantLvl, Bless int16
    Identified  bool
}

type WarehouseRepo struct { /* unexported */ }

func NewWarehouseRepo(db *DB) *WarehouseRepo
func (r *WarehouseRepo) Load(ctx context.Context, accountName string, whType int16) ([]WarehouseItem, error)
func (r *WarehouseRepo) Deposit(ctx context.Context, item WarehouseItem) (int32, error)
func (r *WarehouseRepo) AddToStack(ctx context.Context, whItemID, addCount int32) error
func (r *WarehouseRepo) Withdraw(ctx context.Context, whItemID, count int32) (bool, error)
```

```go
// persist/migrations.go

func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error
```

依賴：`config`, `world` (type definitions for mapping)

### AOI 系統 - package world

```go
// world/aoi.go

const cellSize = 20

type AOIGrid struct { /* unexported: cells map[cellKey]map[uint64]struct{} */ }

func NewAOIGrid() *AOIGrid
func (g *AOIGrid) Add(sessionID uint64, x, y int32, mapID int16)
func (g *AOIGrid) Remove(sessionID uint64, x, y int32, mapID int16)
func (g *AOIGrid) Move(sessionID uint64, oldX, oldY int32, oldMap int16, newX, newY int32, newMap int16)
func (g *AOIGrid) GetNearby(x, y int32, mapID int16) []uint64  // 3x3 cells
```

```go
// world/ground.go

func NextGroundItemID() int32  // 起始 700,000,000

type GroundItem struct {
    ID, ItemID, Count int32
    EnchantLvl        byte
    Name              string
    GrdGfx            int32
    X, Y              int32
    MapID             int16
    OwnerID           int32 // 0=anyone can pick
    TTL               int   // ticks until auto-delete
}
```

依賴：無

### 物品/背包/裝備系統 - package world, data

```go
// world/inventory.go

const (
    MaxInventorySize = 180
    AdenaItemID      = 40308
)

func NextItemObjID() int32
func SetItemObjIDStart(v int32)
func MaxWeight(str, con int16) int32
func EffectiveBless(item *InvItem) byte

type InvItem struct {
    ObjectID, ItemID int32
    Name             string
    InvGfx           int32
    Count            int32
    Identified       bool
    EnchantLvl       byte
    Bless            byte
    Stackable        bool
    Weight           int32
    UseType          byte
    Equipped         bool
}

type Inventory struct { Items []*InvItem }

func NewInventory() *Inventory
func (inv *Inventory) FindByItemID(itemID int32) *InvItem
func (inv *Inventory) FindByObjectID(objectID int32) *InvItem
func (inv *Inventory) Size() int
func (inv *Inventory) IsFull() bool
func (inv *Inventory) AddItem(itemID, count int32, name string, invGfx, weight int32, stackable bool, bless byte) *InvItem
func (inv *Inventory) AddItemWithID(objID, itemID, count int32, name string, invGfx, weight int32, stackable bool, bless byte) *InvItem
func (inv *Inventory) RemoveItem(objectID, count int32) (removed bool)
func (inv *Inventory) GetAdena() int32
func (inv *Inventory) TotalWeight() int32
func (inv *Inventory) Weight242(maxWeight int32) byte
func (inv *Inventory) IsOverWeight(addWeight, maxWeight int32) bool
```

```go
// world/equipment.go

type EquipSlot int
const (
    SlotNone EquipSlot = 0
    SlotHelm    = 1;  SlotArmor   = 2;  SlotGlove   = 3;  SlotBoots   = 4
    SlotShield  = 5;  SlotCloak   = 6;  SlotRing1   = 7;  SlotRing2   = 8
    SlotAmulet  = 9;  SlotBelt    = 10; SlotWeapon  = 11; SlotEarring = 12
    SlotGuarder = 13; SlotTShirt  = 14; SlotMax     = 15
)

type Equipment struct { Slots [SlotMax]*InvItem }
type EquipStats struct {
    AC, HitMod, DmgMod, BowHitMod, BowDmgMod int
    AddStr, AddDex, AddCon, AddInt, AddWis, AddCha int
    AddHP, AddMP, AddHPR, AddMPR, AddSP, MDef int
}

func ArmorSlotFromType(armorType string) EquipSlot
func IsTwoHanded(weaponType string) bool
func WeaponVisualID(weaponType string) byte
func IsAccessorySlot(slot EquipSlot) bool
func EquipClientIndex(slot EquipSlot) byte
func (e *Equipment) Get(slot EquipSlot) *InvItem
func (e *Equipment) Set(slot EquipSlot, item *InvItem)
func (e *Equipment) Weapon() *InvItem
```

```go
// data/item.go

type ItemCategory int
const (
    CategoryEtcItem ItemCategory = 0
    CategoryWeapon  ItemCategory = 1
    CategoryArmor   ItemCategory = 2
)

type ItemInfo struct {
    ItemID       int32
    Name         string
    InvGfx, GrdGfx int32
    Weight       int32
    Category     ItemCategory
    Type, Material string
    DmgSmall, DmgLarge, Range, HitMod, DmgMod int           // weapon
    AC, BowHitMod, BowDmgMod int                             // armor
    AddStr, AddCon, AddDex, AddInt, AddWis, AddCha int        // stat bonuses
    AddHP, AddMP, AddHPR, AddMPR, AddSP, MDef int
    SafeEnchant, Bless int
    Tradeable    bool
    MinLevel, MaxLevel int
    UseRoyal, UseKnight, UseMage, UseElf, UseDarkElf bool    // class restrictions
    UseDragonKnight, UseIllusionist bool
    Stackable    bool
    UseType, ItemType string
    MaxChargeCount, FoodVolume, DelayID, DelayTime int
    UseTypeID    byte
    ItemDescID   int
}

type ItemTable struct { /* unexported */ }

func UseTypeToID(s string) byte
func MaterialToID(s string) byte
func LoadItemTable(weaponPath, armorPath, etcitemPath string) (*ItemTable, error)
func (t *ItemTable) Get(itemID int32) *ItemInfo
func (t *ItemTable) Count() int
```

依賴：`data` (ItemInfo for stat calculations)

### 技能/Buff 系統 - package data, handler, world, scripting

```go
// data/skill.go

type SkillInfo struct {
    SkillID          int32
    Name             string
    SkillLevel, SkillNumber int
    MpConsume, HpConsume int
    ItemConsumeID, ItemConsumeCount int
    ReuseDelay, BuffDuration int
    Target           string // "attack", "buff", "none"
    TargetTo         int
    DamageValue, DamageDice, DamageDiceCount int
    ProbabilityValue, ProbabilityDice int
    Attr, Type, Lawful, Ranged, Area int
    Through          bool
    ActionID         int
    CastGfx, CastGfx2 int32
    SysMsgHappen, SysMsgStop, SysMsgFail int
    IDBitmask        int
}

type SkillTable struct { /* unexported */ }

func LoadSkillTable(path string) (*SkillTable, error)
func (t *SkillTable) Get(skillID int32) *SkillInfo
func (t *SkillTable) GetByName(name string) *SkillInfo
func (t *SkillTable) Count() int
func (t *SkillTable) All() []*SkillInfo
```

```go
// world/state.go — ActiveBuff

type ActiveBuff struct {
    SkillID    int32
    TicksLeft  int
    DeltaAC, DeltaStr, DeltaDex, DeltaCon, DeltaWis, DeltaIntel, DeltaCha int16
    DeltaMaxHP, DeltaMaxMP, DeltaHitMod, DeltaDmgMod int16
    DeltaSP, DeltaMR, DeltaHPR, DeltaMPR int16
    DeltaBowHit, DeltaBowDmg int16
    DeltaFireRes, DeltaWaterRes, DeltaWindRes, DeltaEarthRes int16
    DeltaDodge int16
    SetMoveSpeed, SetBraveSpeed byte
    SetInvisible, SetParalyzed, SetSleeped bool
}

func (p *PlayerInfo) HasBuff(skillID int32) bool
func (p *PlayerInfo) AddBuff(buff *ActiveBuff) *ActiveBuff  // returns old if replaced
func (p *PlayerInfo) RemoveBuff(skillID int32) *ActiveBuff
```

```go
// handler/item.go — Virtual SkillIDs for potion-based buffs
// These match Java L1SkillId.java STATUS_* constants.
// Potions create ActiveBuff entries with these IDs so they persist across logout/login.

const (
    SkillStatusBrave        int32 = 1000 // 勇敢藥水 (brave type 1)
    SkillStatusHaste        int32 = 1001 // 自我加速藥水 (move speed)
    SkillStatusBluePotion   int32 = 1002 // 藍色藥水 (MP regen boost)
    SkillStatusWisdomPotion int32 = 1004 // 慎重藥水 (SP +2)
    SkillStatusElfBrave     int32 = 1016 // 精靈餅乾 (brave type 3)
    SkillStatusThirdSpeed   int32 = 1027 // 三段加速
)

// handler/skill.go

func HandleUseSpell(sess *net.Session, r *packet.Reader, deps *Deps)
func TickPlayerBuffs(p *world.PlayerInfo, deps *Deps)

// key unexported:
// executeAttackSkill, executeBuffSkill, executeSelfSkill
// applyBuffEffect, revertBuffStats, cancelAllBuffs
// applyHaste, applyBrave, applyWisdom — all create ActiveBuff entries for persistence
// sendWisdomPotionIcon, sendBluePotionIcon — potion buff icon packets
```

依賴：`data`, `world`, `scripting`, `net/packet`

### 掉落系統 - package data

```go
// data/drop.go

type DropItem struct {
    ItemID, EnchantLevel int32
    Min, Max, Chance     int // Chance out of 1,000,000
}

type DropTable struct { /* unexported */ }

func LoadDropTable(path string) (*DropTable, error)
func (t *DropTable) Get(mobID int32) []DropItem
func (t *DropTable) Count() int
```

依賴：無

### 死亡/復活系統 - package handler

```go
// handler/death.go

func HandleRestart(sess *net.Session, _ *packet.Reader, deps *Deps) // C_RESTART (177)
func KillPlayer(player *world.PlayerInfo, deps *Deps)

// unexported:
// applyDeathExpPenalty(player, deps) — via Lua calc_death_exp_penalty
// getBackLocation(mapID, deps) — via Lua get_respawn_location
```

依賴：`world`, `scripting`, `net/packet`

### 商店系統 - package data, handler

```go
// data/shop.go

type ShopItem struct {
    ItemID, Order, SellingPrice, PackCount, PurchasingPrice int32
}

type Shop struct {
    NpcID           int32
    SellingItems    []*ShopItem
    PurchasingItems []*ShopItem
}

type ShopTable struct { /* unexported */ }

func LoadShopTable(path string) (*ShopTable, error)
func (t *ShopTable) Get(npcID int32) *Shop
func (t *ShopTable) Count() int
```

```go
// handler/shop.go

func HandleBuySell(sess *net.Session, r *packet.Reader, deps *Deps) // C_BUY_SELL (161)
// unexported: handleBuyFromNpc, handleSellToNpc
```

依賴：`data`, `world`, `persist/wal`, `net/packet`

### 倉庫系統 - package handler, persist

```go
// handler/warehouse.go

const (
    WhTypePersonal int16 = 3
    WhTypeElf      int16 = 4
    WhTypeClan     int16 = 5
)

func OpenWarehouse(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16, deps *Deps)
func OpenWarehouseDeposit(sess *net.Session, player *world.PlayerInfo, npcObjID int32, whType int16, deps *Deps)
func HandleWarehouseResult(sess *net.Session, r *packet.Reader, resultType byte, count int, deps *Deps)
```

依賴：`world`, `persist/warehouse_repo`, `net/packet`

### 聊天系統 - package handler

```go
// handler/chat.go

const (
    ChatNormal = 0; ChatShout = 2; ChatWorld = 3
    ChatClan = 4; ChatParty = 11; ChatTrade = 12
)

func HandleChat(sess *net.Session, r *packet.Reader, deps *Deps)    // C_CHAT (40)
func HandleSay(sess *net.Session, r *packet.Reader, deps *Deps)     // C_SAY (136)
func HandleWhisper(sess *net.Session, r *packet.Reader, deps *Deps)  // C_TELL (184)
```

依賴：`world`, `net/packet`

### PK/PvP 系統 - package handler

```go
// handler/pk.go

func HandleDuel(sess *net.Session, _ *packet.Reader, deps *Deps)     // C_DUEL (5) — toggle PKMode
func HandleCheckPK(sess *net.Session, _ *packet.Reader, deps *Deps)  // C_CHECK_PK (51)

// PK 邏輯 (unexported):
// triggerPinkName(attacker, target, deps)      — 180 秒粉名
// processPKKill(attacker, victim, deps)        — PK 殺人懲罰
// handlePvPAttack(attacker, target, deps)      — 近戰 PvP
// handlePvPFarAttack(attacker, target, deps)   — 遠程 PvP
// inSafetyZone(player, deps) bool              — 安全區檢查
// dropItemsOnPKDeath(attacker, victim, deps)   — 紅名掉落
```

依賴：`world`, `data/mapdata`, `net/packet`

### 傳送/Portal 系統 - package data, handler

```go
// data/portal.go

type PortalEntry struct {
    SrcX, SrcY int32; SrcMapID int16
    DstX, DstY int32; DstMapID int16
    DstHeading int16; Note string
}

type PortalTable struct { /* unexported */ }

func LoadPortalTable(path string) (*PortalTable, error)
func (t *PortalTable) Get(x, y int32, mapID int16) *PortalEntry
func (t *PortalTable) Count() int
```

```go
// handler/portal.go

func HandleEnterPortal(sess *net.Session, r *packet.Reader, deps *Deps) // C_ENTER_PORTAL (219)
```

```go
// handler/teleport.go

func HandleTeleport(sess *net.Session, r *packet.Reader, deps *Deps) // C_TELEPORT (52)
```

依賴：`data`, `world`, `net/packet`

### 交易系統 - package handler

```go
// handler/trade.go

func HandleAskTrade(sess *net.Session, r *packet.Reader, deps *Deps)    // C_ASK_XCHG (2)
func HandleAddTrade(sess *net.Session, r *packet.Reader, deps *Deps)    // C_ADD_XCHG (37)
func HandleAcceptTrade(sess *net.Session, r *packet.Reader, deps *Deps) // C_ACCEPT_XCHG (71)
func HandleCancelTrade(sess *net.Session, r *packet.Reader, deps *Deps) // C_CANCEL_XCHG (86)

// 交易狀態機 (unexported):
// findFaceToFace → 面對面檢測
// handleTradeYesNo → S_Message_YN(252) 回應
// executeTrade → WAL 寫入 → 物品交換
// cancelTrade → 還原物品
// cancelTradeIfActive → 傳送/NPC互動時自動取消
```

依賴：`world`, `persist/wal`, `net/packet`

### NPC 對話/動作系統 - package handler

```go
// handler/npctalk.go

func HandleNpcTalk(sess *net.Session, r *packet.Reader, deps *Deps) // C_DIALOG (34)
```

```go
// handler/npcaction.go

func HandleNpcAction(sess *net.Session, r *packet.Reader, deps *Deps) // C_HACTION (125)

// 路由功能 (unexported):
// handleShopBuy, handleShopSell — 商店買賣介面
// handleTeleportURLGeneric — 傳送頁面
// handleTeleport — 扣金幣+傳送
// teleportPlayer — 完整 AOI 傳送邏輯
// handleYesNoResponse — S_Message_YN 回應路由（交易/組隊/血盟）
```

依賴：`data`, `world`, `net/packet`

### 魔法商店系統 - package handler

```go
// handler/spellshop.go

func HandleBuySpell(sess *net.Session, r *packet.Reader, deps *Deps)     // C_BUY_SPELL (39)
func HandleBuyableSpell(sess *net.Session, r *packet.Reader, deps *Deps) // C_BUYABLE_SPELL (145)
```

依賴：`data`, `world`, `scripting`, `net/packet`

### Lua 腳本引擎 - package scripting

```go
// scripting/engine.go

type Engine struct { /* vm *lua.LState, log *zap.Logger */ }

func NewEngine(scriptsDir string, log *zap.Logger) (*Engine, error)
func (e *Engine) Close()

// 戰鬥
func (e *Engine) CalcMeleeAttack(ctx CombatContext) CombatResult
func (e *Engine) CalcRangedAttack(ctx RangedCombatContext) CombatResult
func (e *Engine) CalcSkillDamage(ctx SkillDamageContext) SkillDamageResult
func (e *Engine) CalcHeal(damageValue, damageDice, damageDiceCount, intel, sp int) int
func (e *Engine) CalcNpcMelee(ctx CombatContext) CombatResult
func (e *Engine) CalcNpcRanged(ctx CombatContext) CombatResult

// Buff
func (e *Engine) GetBuffEffect(skillID, targetLevel int) *BuffEffect
func (e *Engine) IsNonCancellable(skillID int) bool

// 等級/經驗
func (e *Engine) LevelFromExp(exp int) int
func (e *Engine) ExpForLevel(level int) int
func (e *Engine) CalcLevelUp(classType, con, wis int) LevelUpResult
func (e *Engine) CalcInitHP(classType, con int) int
func (e *Engine) CalcInitMP(classType, wis int) int

// 藥水 / 建角 / 復活
func (e *Engine) GetPotionEffect(itemID int) *PotionEffect
func (e *Engine) GetCharCreateData(classType int) *CharCreateData
func (e *Engine) GetResurrectEffect(skillID int) *ResurrectResult
func (e *Engine) GetSpellTiers(classType int) []SpellTierInfo

// 死亡 / 附魔
func (e *Engine) GetRespawnLocation(mapID int) *RespawnLocation
func (e *Engine) CalcDeathExpPenalty(level, exp int) int
func (e *Engine) CalcEnchant(ctx EnchantContext) EnchantResult

// NPC AI
func (e *Engine) RunNpcAI(ctx AIContext) []AICommand
```

依賴：`github.com/yuin/gopher-lua`

### Handler 依賴注入 - package handler

```go
// handler/context.go

type Deps struct {
    AccountRepo    *persist.AccountRepo
    CharRepo       *persist.CharacterRepo
    ItemRepo       *persist.ItemRepo
    Config         *config.Config
    Log            *zap.Logger
    World          *world.State
    Scripting      *scripting.Engine
    NpcActions     *data.NpcActionTable
    Items          *data.ItemTable
    Shops          *data.ShopTable
    Drops          *data.DropTable
    Teleports      *data.TeleportTable
    TeleportHtml   *data.TeleportHtmlTable
    Portals        *data.PortalTable
    Skills         *data.SkillTable
    Npcs           *data.NpcTable
    MobSkills      *data.MobSkillTable
    MapData        *data.MapDataTable
    Polys          *data.PolymorphTable
    WarehouseRepo  *persist.WarehouseRepo
    WALRepo        *persist.WALRepo
    ClanRepo       *persist.ClanRepo
}

func RegisterAll(reg *packet.Registry, deps *Deps)
```
