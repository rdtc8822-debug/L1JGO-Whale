package system

// 龍門系統（DragonDoor）— 門衛 NPC 生命週期管理 + 腳本式 AI。
// Java 參考：DragonKey.java、FireDragonDoorKeeper1/2/3.java、L1SpawnUtil.spawn
//
// 三種門衛行為：
//   70932 雷歐      → 走到 (32654,33000) → heading 2 → 死亡動畫 → 開橋 10040 → 自刪
//   70937 紅雷歐    → 走到 (32694,33052) → heading 2 → 死亡動畫 → 開橋 10041 → 自刪
//   70934 火燒雷歐  → 被玩家擊殺 → 死亡時開橋 10042（普通怪物 AI）
//
// 門衛生命週期：7200 秒（2 小時）後自動移除。
// 計時器以 tick 計數驅動（5 ticks ≈ 1 秒），Phase 3（PostUpdate）。

import (
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// 門衛目的地和橋門 ID 對應表
type keeperProfile struct {
	targetX  int32
	targetY  int32
	heading  int16
	bridgeID int32 // 0 = 無橋（戰鬥型門衛由死亡觸發）
	walking  bool  // true = 走路型, false = 戰鬥型
}

var keeperProfiles = map[int32]keeperProfile{
	70932: {targetX: 32654, targetY: 33000, heading: 2, bridgeID: 10040, walking: true},
	70937: {targetX: 32694, targetY: 33052, heading: 2, bridgeID: 10041, walking: true},
	70934: {bridgeID: 10042, walking: false},
}

// keeperLifetimeTicks 門衛存活時間（ticks）。7200 秒 × 5 ticks/秒 = 36000 ticks。
const keeperLifetimeTicks = 36000

// keeperEntry 追蹤一個活躍的門衛 NPC。
type keeperEntry struct {
	npcObjID  int32 // NPC 在世界中的物件 ID
	npcID     int32 // NPC 模板 ID (70932/70937/70934)
	lifetime  int   // 剩餘存活 ticks
	profile   keeperProfile
	arrived   bool // 走路型：已抵達目的地
	moveTimer int  // 移動冷卻 ticks
}

// DragonDoorSystem 龍門系統。
// 實作 handler.DragonDoorManager 介面。
type DragonDoorSystem struct {
	deps    *handler.Deps
	ws      *world.State
	keepers []*keeperEntry
}

func NewDragonDoorSystem(ws *world.State, deps *handler.Deps) *DragonDoorSystem {
	return &DragonDoorSystem{
		deps: deps,
		ws:   ws,
	}
}

func (s *DragonDoorSystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

func (s *DragonDoorSystem) Update(_ time.Duration) {
	if len(s.keepers) == 0 {
		return
	}

	alive := s.keepers[:0]
	for _, k := range s.keepers {
		k.lifetime--

		// 檢查 NPC 是否仍存在
		npc := s.ws.GetNpc(k.npcObjID)
		if npc == nil {
			// NPC 已被其他系統移除
			continue
		}

		// 存活時間到期 → 強制移除
		if k.lifetime <= 0 {
			s.removeKeeper(npc)
			continue
		}

		// 戰鬥型門衛（70934）：檢查是否死亡
		if !k.profile.walking {
			if npc.Dead {
				// 死亡時開橋（Java: FireDragonDoorKeeper3.death → openBridge）
				s.openBridge(npc, k.profile.bridgeID)
				// 不立即移除 — 讓 NpcRespawnSystem 處理屍體動畫
				// 但從追蹤列表移除（RespawnDelay=0 會讓它不重生）
				continue
			}
			alive = append(alive, k)
			continue
		}

		// 走路型門衛（70932/70937）
		if !k.arrived {
			s.tickWalkingKeeper(k, npc)
		}
		alive = append(alive, k)
	}
	s.keepers = alive
}

// ==================== DragonDoorManager 介面實作 ====================

// GetAvailableCounts 計算各類型門衛的可用名額。
// Java: DragonKey.execute() — 遍歷世界所有 NPC，計算活躍門衛數。
// 每類最大 6 隻，可用 = 6 - 活躍數。
func (s *DragonDoorSystem) GetAvailableCounts() (a, b, c int) {
	a, b, c = 6, 6, 6
	for _, k := range s.keepers {
		switch k.npcID {
		case 70932:
			a--
		case 70937:
			b--
		case 70934:
			c--
		}
	}
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if c < 0 {
		c = 0
	}
	return
}

// SpawnKeeper 在玩家位置生成門衛 NPC。
// Java: L1SpawnUtil.spawn(pc, npcID, 0, 7200) — 在玩家位置生成，0 距離，7200 秒存活。
func (s *DragonDoorSystem) SpawnKeeper(sess *net.Session, player *world.PlayerInfo, npcID int32) {
	profile, ok := keeperProfiles[npcID]
	if !ok {
		return
	}

	// 查找 NPC 模板
	if s.deps.Npcs == nil {
		return
	}
	tmpl := s.deps.Npcs.Get(npcID)
	if tmpl == nil {
		s.deps.Log.Warn("龍門：找不到門衛 NPC 模板",
			zap.Int32("npcID", npcID))
		return
	}

	// 對齊 yiwei L1SpawnUtil.spawn(pc, npcID, 0, 7200)：r=0 時生成在玩家座標。
	x := player.X
	y := player.Y

	// 解析動畫速度
	atkSpeed := tmpl.AtkSpeed
	moveSpeed := tmpl.PassiveSpeed
	if s.deps.SprTable != nil {
		gfx := int(tmpl.GfxID)
		if tmpl.AtkSpeed != 0 {
			if v := s.deps.SprTable.GetAttackSpeed(gfx, data.ActAttack); v > 0 {
				atkSpeed = int16(v)
			}
		}
		if tmpl.PassiveSpeed != 0 {
			if v := s.deps.SprTable.GetMoveSpeed(gfx, data.ActWalk); v > 0 {
				moveSpeed = int16(v)
			}
		}
	}

	// 走路型用 "L1DragonKeeper"（NpcAISystem 會跳過），戰鬥型用 "L1Monster"（正常 AI）
	impl := "L1DragonKeeper"
	if !profile.walking {
		impl = "L1Monster"
	}

	npc := &world.NpcInfo{
		ID:                world.NextNpcID(),
		NpcID:             tmpl.NpcID,
		Impl:              impl,
		GfxID:             tmpl.GfxID,
		Name:              tmpl.Name,
		NameID:            tmpl.NameID,
		Level:             tmpl.Level,
		X:                 x,
		Y:                 y,
		MapID:             player.MapID,
		Heading:           5,
		HP:                tmpl.HP,
		MaxHP:             tmpl.HP,
		MP:                tmpl.MP,
		MaxMP:             tmpl.MP,
		AC:                tmpl.AC,
		STR:               tmpl.STR,
		DEX:               tmpl.DEX,
		Intel:             tmpl.INT,
		Exp:               tmpl.Exp,
		Lawful:            tmpl.Lawful,
		Size:              tmpl.Size,
		MR:                tmpl.MR,
		Undead:            tmpl.Undead,
		UndeadType:        tmpl.UndeadType,
		TurnUndeadable:    tmpl.EffectiveTurnUndeadable(),
		TurnUndeadableSet: true,
		Hard:              tmpl.Hard,
		CantResurrect:     tmpl.CantResurrect,
		Agro:              false, // 門衛不主動攻擊
		AtkDmg:            int32(tmpl.Level) + int32(tmpl.STR)/3,
		Ranged:            tmpl.Ranged,
		AtkSpeed:          atkSpeed,
		AtkMagicSpeed:     tmpl.AtkMagicSpeed,
		SubMagicSpeed:     tmpl.SubMagicSpeed,
		MoveSpeed:         moveSpeed,
		ShowID:            player.ShowID,
		SpawnX:            x,
		SpawnY:            y,
		SpawnMapID:        player.MapID,
		RespawnDelay:      0, // 動態生成：不重生
	}
	s.ws.AddNpc(npc)

	// 廣播給附近玩家
	nearby := s.ws.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, viewer := range nearby {
		handler.SendNpcPack(viewer.Session, npc)
	}

	// 加入追蹤列表
	s.keepers = append(s.keepers, &keeperEntry{
		npcObjID: npc.ID,
		npcID:    npcID,
		lifetime: keeperLifetimeTicks,
		profile:  profile,
	})

	s.deps.Log.Info("龍門：門衛 NPC 已生成",
		zap.Int32("npcID", npcID),
		zap.String("name", tmpl.Name),
		zap.Int32("x", x),
		zap.Int32("y", y),
		zap.Int16("mapID", player.MapID),
	)
}

// ==================== 內部邏輯 ====================

// tickWalkingKeeper 處理走路型門衛的移動 AI。
// Java: FireDragonDoorKeeper1/2.Work.run() — 每次移動間隔走一步，抵達後開橋。
func (s *DragonDoorSystem) tickWalkingKeeper(k *keeperEntry, npc *world.NpcInfo) {
	if npc.Dead {
		k.arrived = true
		return
	}

	// 移動冷卻
	if k.moveTimer > 0 {
		k.moveTimer--
		return
	}

	// 計算距離
	dx := k.profile.targetX - npc.X
	dy := k.profile.targetY - npc.Y
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

	if dist <= 0 {
		// 已抵達目的地
		k.arrived = true
		s.keeperArrived(k, npc)
		return
	}

	// 向目的地移動一步（使用現有的 npcMoveToward 邏輯）
	npcMoveToward(s.ws, npc, k.profile.targetX, k.profile.targetY, s.deps.MapData)

	// 計算移動冷卻（與 NPC AI 系統相同）
	moveTicks := 4
	if npc.MoveSpeed > 0 {
		moveTicks = int(npc.MoveSpeed) / 200
		if moveTicks < 2 {
			moveTicks = 2
		}
	}
	k.moveTimer = moveTicks

	// 移動後重新檢查是否抵達
	if npc.X == k.profile.targetX && npc.Y == k.profile.targetY {
		k.arrived = true
		s.keeperArrived(k, npc)
	}
}

// keeperArrived 門衛抵達目的地後的處理。
// Java: 到達後 → 設定 heading → 死亡台詞 → 死亡動畫 → 刪除 → 開橋
func (s *DragonDoorSystem) keeperArrived(k *keeperEntry, npc *world.NpcInfo) {
	nearby := s.ws.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)

	// 設定 heading
	npc.Heading = k.profile.heading
	headingData := handler.BuildChangeHeading(npc.ID, k.profile.heading)
	handler.BroadcastToPlayers(nearby, headingData)

	// 播放死亡動畫（Java: S_DoActionGFX(npc.getId(), 8)）
	deathData := handler.BuildActionGfx(npc.ID, 8)
	handler.BroadcastToPlayers(nearby, deathData)

	// 開橋
	s.openBridge(npc, k.profile.bridgeID)

	// 移除 NPC
	s.removeKeeper(npc)
}

// openBridge 開啟指定 bridgeID 的橋門。
// Java: openBridge() — 遍歷世界所有門，開啟 showId 匹配且 doorId 匹配的門。
// Go 簡化：由於 Go 暫無 showId 系統，改為開啟同地圖上 DoorID 匹配的門。
func (s *DragonDoorSystem) openBridge(npc *world.NpcInfo, bridgeID int32) {
	if bridgeID == 0 {
		return
	}

	doors := s.ws.GetDoorsByMap(npc.MapID)
	for _, door := range doors {
		if door.DoorID == bridgeID {
			if door.Open() {
				handler.BroadcastDoorOpen(door, s.deps)
				s.deps.Log.Info("龍門：橋門已開啟",
					zap.Int32("bridgeID", bridgeID),
					zap.Int16("mapID", npc.MapID),
				)
			}
		}
	}
}

// removeKeeper 從世界中移除門衛 NPC 並廣播。
func (s *DragonDoorSystem) removeKeeper(npc *world.NpcInfo) {
	nearby := s.ws.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	rmData := handler.BuildRemoveObject(npc.ID)
	handler.BroadcastToPlayers(nearby, rmData)

	// 解除地圖格子封鎖
	if s.deps.MapData != nil {
		s.deps.MapData.SetImpassable(npc.MapID, npc.X, npc.Y, false)
	}

	s.ws.RemoveNpc(npc.ID)
}
