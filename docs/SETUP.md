# L1JGO-Whale 伺服器架設指南

完整的從零開始架設教學。

---

## 目錄

1. [環境需求](#環境需求)
2. [安裝 PostgreSQL](#安裝-postgresql)
3. [建立資料庫](#建立資料庫)
4. [設定伺服器](#設定伺服器)
5. [準備地圖檔案](#準備地圖檔案)
6. [編譯與啟動](#編譯與啟動)
7. [客戶端連線設定](#客戶端連線設定)
8. [設定 GM 帳號](#設定-gm-帳號)
9. [設定檔完整說明](#設定檔完整說明)
10. [常見問題](#常見問題)

---

## 環境需求

| 軟體 | 最低版本 | 說明 |
|------|----------|------|
| Go | 1.23+ | [下載頁面](https://go.dev/dl/) |
| PostgreSQL | 14+ | [下載頁面](https://www.postgresql.org/download/) |
| 天堂 3.80C 客戶端 | 台版 3.80C | 需自行準備 |

### 作業系統

支援 Windows、Linux、macOS。本文以 Windows 為主要範例。

---

## 安裝 PostgreSQL

### Windows

1. 從 [PostgreSQL 官網](https://www.postgresql.org/download/windows/) 下載安裝程式
2. 安裝時記住你設定的超級使用者密碼（預設使用者名稱為 `postgres`）
3. 預設埠號為 `5432`，保持不變即可
4. 安裝完成後，確認 PostgreSQL 服務已啟動

### Linux (Ubuntu/Debian)

```bash
sudo apt update
sudo apt install postgresql postgresql-contrib
sudo systemctl start postgresql
sudo systemctl enable postgresql
```

---

## 建立資料庫

### 方法一：使用 psql 命令列

```bash
# Windows：開啟 "SQL Shell (psql)" 或命令提示字元
psql -U postgres

# 輸入密碼後，執行：
CREATE DATABASE l1jgo;
\q
```

### 方法二：使用 pgAdmin

1. 開啟 pgAdmin（隨 PostgreSQL 一同安裝）
2. 連線到本機 PostgreSQL 伺服器
3. 右鍵 Databases → Create → Database
4. 名稱輸入 `l1jgo`，按 Save

> **注意**：資料庫表格會在伺服器首次啟動時自動建立（透過 goose 遷移工具），不需要手動建表。

---

## 設定伺服器

編輯 `server/config/server.toml`：

### 必須修改的設定

```toml
[database]
# 將 postgres 和密碼改為你的 PostgreSQL 設定
dsn = "postgres://postgres:你的密碼@localhost:5432/l1jgo?sslmode=disable"
```

### 建議修改的設定

```toml
[network]
bind_address = "0.0.0.0:7001"  # 監聽埠號，需與客戶端一致

[rates]
exp_rate = 1      # 經驗值倍率（1=正常, 10=十倍, 5000=五千倍）
drop_rate = 1     # 掉寶倍率
gold_rate = 1     # 金幣倍率

[character]
auto_create_accounts = true  # true=輸入新帳號自動註冊, false=需手動建帳
```

---

## 準備地圖檔案

地圖檔案為 `.s32` 二進制格式（更名為 `.txt`），用於碰撞偵測和移動驗證。

1. 將地圖檔案放到專案根目錄的 `map/` 資料夾
2. 檔案命名格式：`<mapID>.txt`（例如 `2005.txt`）

> **注意**：沒有地圖檔案的地圖，NPC 移動和碰撞偵測將無法正常運作，但玩家仍可登入。

---

## 編譯與啟動

### 方法一：使用 Makefile

```bash
cd server
make run
```

### 方法二：手動編譯

```bash
cd server
go build -o l1jgo ./cmd/l1jgo
./l1jgo              # Linux/macOS
.\l1jgo.exe          # Windows
```

### 方法三：直接執行（開發用）

```bash
cd server
go run ./cmd/l1jgo
```

### 啟動成功畫面

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
  NPC 動作 ····························· 1111
  道具模板 ····························· 2992
  商店 ·································· 129
  掉寶表 ······························· 1015
  傳送點 ······························· 1135
  傳送選單 ······························ 199
  傳送門 ······························· 1641
  技能 ·································· 253
  怪物技能 ······························ 588
  ✓ Lua 腳本載入完成

  ── 伺服器就緒 ───────────────────────────────
  ▶ 監聽位址 0.0.0.0:7001
  ▶ 遊戲迴圈啟動 (tick: 200ms)
```

看到 `伺服器就緒` 即表示啟動成功。

---

## 客戶端連線設定

將天堂 3.80C 客戶端的伺服器連線位址設定為：

- **IP**：`127.0.0.1`（本機）或你的伺服器 IP
- **Port**：`7001`（與 `server.toml` 的 `bind_address` 一致）

---

## 設定 GM 帳號

首次登入後，使用 psql 或 pgAdmin 修改帳號權限：

```sql
-- 將帳號設為 GM（access_level = 200）
UPDATE accounts SET access_level = 200 WHERE login = '你的帳號名';
```

| access_level | 權限 |
|-------------|------|
| 0 | 一般玩家 |
| 200 | GM（可使用 GM 指令） |

設定完成後重新登入即可使用 GM 指令（以 `.` 開頭，例如 `.level 52`）。

---

## 設定檔完整說明

以下是 `server/config/server.toml` 各設定項的詳細說明：

### [server] 伺服器基本設定

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `name` | `"L1JGO-Whale"` | 伺服器名稱 |
| `id` | `1` | 伺服器編號 |
| `language` | `3` | 語系：0=美國, 3=台灣, 4=日本, 5=中國 |

### [database] 資料庫設定

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `dsn` | - | PostgreSQL 連線字串 |
| `max_open_conns` | `20` | 最大連線數 |
| `max_idle_conns` | `5` | 閒置連線數 |
| `conn_max_lifetime` | `"30m"` | 連線最大存活時間 |

### [network] 網路設定

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `bind_address` | `"0.0.0.0:7001"` | 監聽位址與埠號 |
| `tick_rate` | `"200ms"` | 遊戲迴圈間隔（每秒 5 tick） |
| `in_queue_size` | `128` | 每連線輸入佇列大小 |
| `out_queue_size` | `256` | 每連線輸出佇列大小 |
| `max_packets_per_tick` | `32` | 每 tick 每連線最大封包數 |
| `write_timeout` | `"10s"` | 寫入逾時 |
| `read_timeout` | `"60s"` | 讀取逾時 |

### [rates] 倍率設定

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `exp_rate` | `1` | 經驗值倍率 |
| `drop_rate` | `1` | 掉寶倍率 |
| `gold_rate` | `1` | 金幣倍率 |
| `lawful_rate` | `1.0` | 正義值倍率 |

### [enchant] 衝裝設定

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `weapon_chance` | `0.5` | 武器衝裝成功率（50%） |
| `armor_chance` | `0.333` | 防具衝裝成功率（33%） |

### [character] 角色設定

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `default_slots` | `6` | 預設角色欄位數 |
| `auto_create_accounts` | `true` | 自動建立帳號 |
| `delete_7_days` | `true` | 啟用 7 天刪除等待期 |
| `delete_7_days_min_level` | `5` | 等待刪除的最低等級 |
| `client_language_code` | `"MS950"` | 客戶端文字編碼 |

### [logging] 日誌設定

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `level` | `"info"` | 日誌等級：debug, info, warn, error |
| `format` | `"console"` | 輸出格式：console 或 json |

### [rate_limit] 流量限制

| 設定 | 預設值 | 說明 |
|------|--------|------|
| `enabled` | `true` | 啟用流量限制 |
| `login_attempts_per_minute` | `10` | 每分鐘最大登入嘗試次數 |
| `packets_per_second` | `60` | 每秒最大封包數 |

---

## 常見問題

### Q: 啟動時出現 `database: connect to db` 錯誤

**A**: 檢查 PostgreSQL 是否正在執行，以及 `server.toml` 中的 `dsn` 設定是否正確（帳號、密碼、埠號、資料庫名稱）。

### Q: 客戶端無法連線

**A**: 確認：
1. 伺服器已顯示 `▶ 監聽位址` 訊息
2. 客戶端的 IP 和埠號與 `bind_address` 一致
3. 防火牆沒有封鎖該埠號

### Q: 如何關閉伺服器？

**A**: 在終端按 `Ctrl+C`，伺服器會自動保存所有線上玩家資料後安全關閉。

### Q: 如何修改倍率？

**A**: 編輯 `server/config/server.toml` 中的 `[rates]` 區塊，修改後需重啟伺服器。

### Q: 資料庫表格需要手動建嗎？

**A**: 不需要。伺服器首次啟動時會自動執行資料庫遷移（goose），自動建立所有需要的表格。

### Q: 如何備份資料？

**A**: 備份 PostgreSQL 資料庫：
```bash
pg_dump -U postgres l1jgo > backup.sql
```

還原：
```bash
psql -U postgres l1jgo < backup.sql
```

### Q: 如何查看詳細日誌？

**A**: 修改 `server.toml` 中的日誌等級：
```toml
[logging]
level = "debug"  # 顯示所有封包收發等詳細資訊
```
