# L1JGO-Whale

天堂 (Lineage 1) 3.80C 遊戲伺服器 — 使用 Go 語言從零重寫。

以 L1J 3.80C Java 伺服器為行為參考，但採用全新的 Go 架構：ECS 實體系統、Lua 腳本驅動遊戲邏輯、YAML 靜態資料、PostgreSQL 持久化。

> **開發階段**：目前已完成至 Phase 5（技能 / Buff / NPC AI），功能持續開發中。

## 已實現功能

| 類別 | 功能 |
|------|------|
| **帳號系統** | 登入驗證、自動建帳、封鎖帳號、bcrypt 密碼 |
| **角色系統** | 建立 / 刪除 / 切換角色、配點、角色列表 |
| **世界系統** | 進入世界、AOI 視野管理、地圖傳送門 |
| **移動系統** | 8 方向移動、地圖碰撞檢測、速度驗證 |
| **戰鬥系統** | 近戰 / 遠程攻擊、Lua 傷害公式、PK 系統 |
| **NPC 系統** | NPC 生成 / 重生、對話系統、商店 NPC |
| **NPC AI** | 仇恨目標偵測、追擊 / 攻擊 / 遊走（Lua 驅動） |
| **道具系統** | 背包管理、裝備穿脫、消耗品、地面物品 |
| **商店系統** | 購買 / 販賣、多層稅率計算 |
| **倉庫系統** | 存入 / 取出、堆疊道具 |
| **交易系統** | 玩家間交易、WAL 經濟安全 |
| **衝裝系統** | 武器 / 防具衝裝、祝福保護、碎裂機制 |
| **技能系統** | 技能施放、魔法商店學習、技能書學習 |
| **Buff 系統** | 加速藥水、技能 Buff、定時到期 |
| **怪物技能** | 怪物魔法攻擊、遠程攻擊、技能觸發 |
| **組隊系統** | 建立 / 加入 / 離開隊伍、隊長轉移、小地圖位置同步 |
| **聊天系統** | 一般 / 廣播 / 密語 / 隊伍頻道 |
| **傳送系統** | NPC 傳送、記憶座標（書籤）、傳送門 |
| **死亡系統** | 玩家死亡、經驗懲罰、重新開始 |
| **GM 指令** | 等級 / 屬性 / 傳送 / 生成道具 / 踢人等 |
| **自動存檔** | 定時存檔角色資料、斷線自動保存 |

## 技術架構

```
Go Engine（網路層、ECS 實體系統、世界管理、持久化）
  + Lua Scripts（所有遊戲邏輯：戰鬥公式、AI、技能）
  + YAML（靜態遊戲資料：道具、NPC、技能、地圖、商店）
  + TOML（運行時設定：倍率、伺服器參數）
  + PostgreSQL（資料持久化、WAL 經濟安全機制）
```

| 技術 | 用途 |
|------|------|
| Go 1.23+ | 伺服器核心 |
| [gopher-lua](https://github.com/yuin/gopher-lua) | Lua 腳本引擎 |
| [pgx](https://github.com/jackc/pgx) | PostgreSQL 驅動 |
| [goose](https://github.com/pressly/goose) | 資料庫遷移 |
| [zap](https://go.uber.org/zap) | 結構化日誌 |
| [yaml.v3](https://gopkg.in/yaml.v3) | YAML 解析 |
| [toml](https://github.com/BurntSushi/toml) | TOML 設定 |
| [bcrypt](https://golang.org/x/crypto/bcrypt) | 密碼雜湊 |

## 快速開始

### 環境需求

- **Go** 1.23 以上
- **PostgreSQL** 14 以上
- **天堂 3.80C 客戶端**（台版）
- **地圖檔案**（`.s32` 格式，放置於 `map/` 目錄）

### 1. 建立資料庫

```sql
CREATE DATABASE l1jgo;
```

### 2. 修改設定檔

複製並編輯設定檔 `server/config/server.toml`：

```toml
[database]
dsn = "postgres://你的帳號:你的密碼@localhost:5432/l1jgo?sslmode=disable"

[network]
bind_address = "0.0.0.0:7001"  # 客戶端連線埠號

[rates]
exp_rate = 1      # 經驗值倍率（1 = 正常）
drop_rate = 1     # 掉寶倍率
gold_rate = 1     # 金幣倍率
```

完整設定說明請參考 [server/config/server.toml](server/config/server.toml)，每個欄位都有中文註解。

### 3. 編譯並啟動

```bash
cd server
go build -o l1jgo ./cmd/l1jgo
./l1jgo
```

或使用 Makefile：

```bash
cd server
make run
```

### 4. 連線遊戲

將客戶端的伺服器位址指向 `127.0.0.1:7001`，啟動客戶端即可登入。

首次登入時輸入任意帳號密碼，伺服器會自動建立帳號（可在設定檔關閉此功能）。

## 啟動畫面

```
  ┌───────────────────────────────────────────┐
  │           L1JGO-Whale  v0.1.0             │
  │      天堂 3.80C · Go 遊戲伺服器           │
  └───────────────────────────────────────────┘

  伺服器: L1JGO-Whale (編號: 1)

  ── 資料庫 ───────────────────────────────────
  ✓ PostgreSQL 連線成功
  ✓ 資料庫遷移完成

  ── 資料載入 ─────────────────────────────────
  NPC 模板 ····························· 3428
  地圖資料 ······························ 599
  NPC 生成 ···························· 29407
  ...
  ✓ Lua 腳本載入完成

  ── 伺服器就緒 ───────────────────────────────
  ▶ 監聽位址 0.0.0.0:7001
  ▶ 遊戲迴圈啟動 (tick: 200ms)
```

## 專案結構

```
L1JGO-Whale/
├── server/
│   ├── cmd/l1jgo/main.go          # 程式進入點
│   ├── config/server.toml         # 伺服器設定檔
│   ├── data/yaml/                 # 靜態遊戲資料（YAML）
│   ├── scripts/                   # Lua 遊戲邏輯腳本
│   │   ├── ai/                    #   NPC AI
│   │   ├── combat/                #   戰鬥公式
│   │   ├── core/                  #   核心查表（STR/DEX/經驗）
│   │   ├── character/             #   角色相關
│   │   ├── item/                  #   道具使用
│   │   ├── skill/                 #   技能處理
│   │   └── world/                 #   世界事件
│   ├── internal/
│   │   ├── config/                # TOML 設定載入
│   │   ├── core/ecs/              # ECS 實體組件系統
│   │   ├── core/event/            # 事件匯流排
│   │   ├── core/system/           # 系統執行器
│   │   ├── net/                   # 網路層（TCP、加密、封包）
│   │   ├── handler/               # 封包處理器
│   │   ├── world/                 # 世界狀態、AOI
│   │   ├── scripting/             # Lua 引擎封裝
│   │   ├── persist/               # PostgreSQL 持久化
│   │   └── data/                  # YAML 資料載入器
│   ├── seed/                      # 開發測試資料
│   ├── Makefile
│   └── go.mod
├── map/                           # 地圖檔案（.s32）
└── l1j_java/                      # Java 參考原始碼（僅供參考）
```

## 設定檔說明

設定檔位於 `server/config/server.toml`，主要區塊：

| 區塊 | 說明 |
|------|------|
| `[server]` | 伺服器名稱、編號、語系 |
| `[database]` | PostgreSQL 連線字串、連線池 |
| `[network]` | 監聽位址、tick rate、佇列大小 |
| `[rates]` | 經驗 / 掉寶 / 金幣倍率 |
| `[enchant]` | 衝裝成功率 |
| `[character]` | 角色欄位數、自動建帳、刪除等待期 |
| `[logging]` | 日誌等級與格式 |
| `[rate_limit]` | 流量限制 |

## GM 指令

在遊戲內聊天輸入（需 GM 權限）：

| 指令 | 說明 |
|------|------|
| `.level <n>` | 設定等級 |
| `.str/dex/con/wis/cha/int <n>` | 設定屬性 |
| `.hp/mp <n>` | 設定 HP/MP |
| `.move <x> <y> <map>` | 傳送到座標 |
| `.item <id> [count] [enchant]` | 生成道具 |
| `.kick <name>` | 踢出玩家 |
| `.kill` | 擊殺目標 |
| `.heal` | 回滿 HP/MP |
| `.speed <n>` | 設定移動速度 |
| `.spawn <npc_id>` | 生成 NPC |
| `.weather <type> <intensity>` | 設定天氣 |
| `.who` | 列出線上玩家 |

## 開發備註

- **協議層**：完全遵循 3.80C 客戶端的封包格式、操作碼、欄位順序，不可更改
- **架構層**：使用 Go 的 ECS + Lua + Event Bus 架構，與 Java 原始碼架構完全不同
- **Lua 腳本**：所有遊戲數值邏輯（傷害公式、AI 決策、技能效果）都在 Lua 中實現，方便熱更新
- **WAL 安全機制**：交易、商店等經濟操作使用 Write-Ahead Log，先寫 DB 再改記憶體，防止當機造成的經濟漏洞

## License

本專案僅供學習與研究用途。
