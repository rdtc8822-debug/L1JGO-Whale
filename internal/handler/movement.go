package handler

import (
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// Direction deltas indexed by heading (0-7).
var headingDX = [8]int32{0, 1, 1, 1, 0, -1, -1, -1}
var headingDY = [8]int32{-1, -1, 0, 1, 1, 1, 0, -1}

// HandleMove processes C_MOVE (opcode 29).
// Taiwan 3.80C client: heading XOR'd with 0x49, sends current X/Y.
// Java (Taiwan, CLIENT_LANGUAGE == 3) IGNORES client X/Y and always uses
// server-tracked position: locx = pc.getX(); locy = pc.getY();
// We do the same — InQueue is blocking so no packets are dropped.
func HandleMove(sess *net.Session, r *packet.Reader, deps *Deps) {
	_ = r.ReadH() // client X (ignored — Taiwan client offset differs from server)
	_ = r.ReadH() // client Y (ignored)
	rawHeading := r.ReadC()

	// Taiwan client: heading XOR'd with 0x49
	heading := int16(rawHeading ^ 0x49)

	if heading < 0 || heading > 7 {
		return
	}

	ws := deps.World
	player := ws.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// 麻痺/暈眩/凍結/睡眠時無法移動（客戶端已鎖定，這裡做伺服器端防護）
	if player.Paralyzed || player.Sleeped {
		return
	}

	// --- 移動速度驗證（反加速外掛） ---
	// 一般走路 ~200ms，加速 ~133ms。套用 50% 容許值（避免 tick 批次處理導致誤判）。
	// 誤判時靜默丟棄（不觸發 rejectMove），避免全畫面彈回造成卡頓。
	now := time.Now().UnixNano()
	minInterval := int64(100_000_000) // 200ms * 50% = 100ms
	if player.MoveSpeed == 1 {
		minInterval = 66_000_000 // 133ms * 50% = 66ms
	}
	if player.LastMoveTime > 0 && (now-player.LastMoveTime) < minInterval {
		return // 靜默丟棄：不發封包、不更新座標、不更新 LastMoveTime
	}
	player.LastMoveTime = now

	// 永遠使用伺服器端座標（與 Java 台版行為一致）
	curX := player.X
	curY := player.Y

	// 從目前位置 + 朝向計算目的地
	destX := curX + headingDX[heading]
	destY := curY + headingDY[heading]

	// Java C_MoveChar 流程（第 130 行）：先清除舊座標 0x80，再做通行性判定。
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, curX, curY, false)
	}

	// 地形通行性檢查 + Java fallback（第 160-174 行）：
	// 1. isPassable 失敗 → 2. CheckUtil.checkPassable 檢查目的地有無實體
	// 地形不通 + 無實體 → 放行（信任客戶端，tile 資料可能與客戶端不完全吻合）
	// 地形不通 + 有實體佔位 → 拒絕
	if deps.MapData != nil && !deps.MapData.IsPassableIgnoreOccupant(player.MapID, curX, curY, int(heading)) {
		if ws.IsOccupied(destX, destY, player.MapID, player.CharID) {
			// 恢復舊座標 0x80（因為上面已經清除了，拒絕時要恢復）
			deps.MapData.SetImpassable(player.MapID, curX, curY, true)
			rejectMove(sess, player, ws, deps)
			return
		}
		// 地形不通但目的地無實體 → 信任客戶端，放行
	}

	// Update position to DESTINATION
	ws.UpdatePosition(sess.ID, destX, destY, player.MapID, heading)

	// Mark new position as impassable (for NPC pathfinding)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, destX, destY, true)
	}

	// Auto-cancel trade if partner is too far (> 15 tiles or different map)
	if player.TradePartnerID != 0 {
		partner := deps.World.GetByCharID(player.TradePartnerID)
		if partner != nil {
			tdx := destX - partner.X
			tdy := destY - partner.Y
			if tdx < 0 {
				tdx = -tdx
			}
			if tdy < 0 {
				tdy = -tdy
			}
			dist := tdx
			if tdy > dist {
				dist = tdy
			}
			if dist > 15 || player.MapID != partner.MapID {
				cancelTradeIfActive(player, deps)
			}
		} else {
			cancelTradeIfActive(player, deps)
		}
	}

	// 廣播移動封包 + 格子封鎖（匹配 Java C_MoveChar: broadcastPacketAll）。
	// 只做 1 次 GetNearbyPlayers 查詢（原本 16 次）。
	// 玩家進出視野由 VisibilitySystem（Phase 3, 每 400ms）獨立處理。
	nearby := ws.GetNearbyPlayers(destX, destY, player.MapID, sess.ID)
	for _, other := range nearby {
		sendMoveObject(other.Session, player.CharID, curX, curY, heading)
	}
}

// rejectMove 碰撞拒絕：回彈玩家位置 + 重發所有附近實體。
// 對應 Java L1PcUnlock.Pc_Unlock() 流程：
//   S_OwnCharPack → removeAllKnownObjects → updateObject → S_CharVisualUpdate
// 只發 S_OwnCharPack 會讓客戶端清除附近物件渲染，必須立即重發所有可見實體。
func rejectMove(sess *net.Session, player *world.PlayerInfo, ws *world.State, deps *Deps) {
	// 1. 回彈：告知客戶端正確座標
	sendOwnCharPackPlayer(sess, player)

	// 2. 重置 Known 集合（客戶端收到 OwnCharPack 後會清除附近物件渲染）
	if player.Known != nil {
		player.Known.Reset()
	}

	px, py := player.X, player.Y

	// 3. 重發所有附近玩家 + 封鎖格子 + 填入 Known
	nearbyPlayers := ws.GetNearbyPlayers(px, py, player.MapID, sess.ID)
	for _, other := range nearbyPlayers {
		SendPutObject(sess, other)
		if player.Known != nil {
			player.Known.Players[other.CharID] = world.KnownPos{X: other.X, Y: other.Y}
		}
	}

	// 4. 重發所有附近 NPC + 封鎖格子
	nearbyNpcs := ws.GetNearbyNpcs(px, py, player.MapID)
	for _, n := range nearbyNpcs {
		SendNpcPack(sess, n)
		if player.Known != nil {
			player.Known.Npcs[n.ID] = world.KnownPos{X: n.X, Y: n.Y}
		}
	}

	// 5. 重發所有附近召喚獸 + 封鎖格子
	nearbySummons := ws.GetNearbySummons(px, py, player.MapID)
	for _, s := range nearbySummons {
		isOwner := s.OwnerCharID == player.CharID
		masterName := ""
		if m := ws.GetByCharID(s.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendSummonPack(sess, s, isOwner, masterName)
		if player.Known != nil {
			player.Known.Summons[s.ID] = world.KnownPos{X: s.X, Y: s.Y}
		}
	}

	// 6. 重發所有附近魔法娃娃 + 封鎖格子
	nearbyDolls := ws.GetNearbyDolls(px, py, player.MapID)
	for _, d := range nearbyDolls {
		masterName := ""
		if m := ws.GetByCharID(d.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendDollPack(sess, d, masterName)
		if player.Known != nil {
			player.Known.Dolls[d.ID] = world.KnownPos{X: d.X, Y: d.Y}
		}
	}

	// 7. 重發所有附近隨從 + 封鎖格子
	nearbyFollowers := ws.GetNearbyFollowers(px, py, player.MapID)
	for _, f := range nearbyFollowers {
		SendFollowerPack(sess, f)
		if player.Known != nil {
			player.Known.Followers[f.ID] = world.KnownPos{X: f.X, Y: f.Y}
		}
	}

	// 8. 重發所有附近寵物 + 封鎖格子
	nearbyPets := ws.GetNearbyPets(px, py, player.MapID)
	for _, p := range nearbyPets {
		isOwner := p.OwnerCharID == player.CharID
		masterName := ""
		if m := ws.GetByCharID(p.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendPetPack(sess, p, isOwner, masterName)
		if player.Known != nil {
			player.Known.Pets[p.ID] = world.KnownPos{X: p.X, Y: p.Y}
		}
	}

	// 9. 重發所有附近地面物品（地面物品不佔格子，不需封鎖）
	nearbyGnd := ws.GetNearbyGroundItems(player.X, player.Y, player.MapID)
	for _, g := range nearbyGnd {
		SendDropItem(sess, g)
		if player.Known != nil {
			player.Known.GroundItems[g.ID] = world.KnownPos{X: g.X, Y: g.Y}
		}
	}

	// 10. 重發所有附近門
	nearbyDoors := ws.GetNearbyDoors(player.X, player.Y, player.MapID)
	for _, d := range nearbyDoors {
		SendDoorPerceive(sess, d)
		if player.Known != nil {
			player.Known.Doors[d.ID] = world.KnownPos{X: d.X, Y: d.Y}
		}
	}

	// 11. 武器/變身視覺更新（Java 流程最後一步）
	sendCharVisualUpdate(sess, player)
}

// HandleChangeDirection processes C_CHANGE_DIRECTION (opcode 225).
// NOTE: Unlike C_MOVE, C_ChangeHeading does NOT XOR heading with 0x49 — raw value.
func HandleChangeDirection(sess *net.Session, r *packet.Reader, deps *Deps) {
	heading := int16(r.ReadC())
	if heading < 0 || heading > 7 {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}
	player.Heading = heading

	// Broadcast direction change to nearby players
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendChangeHeading(other.Session, player.CharID, heading)
	}
}
