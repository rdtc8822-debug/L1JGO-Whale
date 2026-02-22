package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
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

	// Always use server-tracked position (matching Java Taiwan behavior).
	curX := player.X
	curY := player.Y

	// Calculate destination from base position + heading
	destX := curX + headingDX[heading]
	destY := curY + headingDY[heading]

	// Mark old tile passable, new tile impassable (for NPC pathfinding only).
	// Java C_MoveChar does NOT validate IsPassable for player movement — client
	// handles its own collision. Server trusts client position. The impassable
	// flag is only used by NPC AI pathfinding to avoid walking through players.
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, curX, curY, false)
		deps.MapData.SetImpassable(player.MapID, destX, destY, true)
	}

	// Get old nearby set BEFORE moving
	oldNearby := ws.GetNearbyPlayers(curX, curY, player.MapID, sess.ID)

	// Update position to DESTINATION
	ws.UpdatePosition(sess.ID, destX, destY, player.MapID, heading)

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

	// Get new nearby set AFTER moving
	newNearby := ws.GetNearbyPlayers(destX, destY, player.MapID, sess.ID)

	// Build lookup sets for diffing
	oldSet := make(map[uint64]struct{}, len(oldNearby))
	for _, p := range oldNearby {
		oldSet[p.SessionID] = struct{}{}
	}
	newSet := make(map[uint64]struct{}, len(newNearby))
	for _, p := range newNearby {
		newSet[p.SessionID] = struct{}{}
	}

	// 1. Players in BOTH old and new: send movement packet with PREVIOUS position
	for _, other := range newNearby {
		if _, wasOld := oldSet[other.SessionID]; wasOld {
			sendMoveObject(other.Session, player.CharID, curX, curY, heading)
		}
	}

	// 2. Players in NEW but not OLD: they just entered our view
	for _, other := range newNearby {
		if _, wasOld := oldSet[other.SessionID]; !wasOld {
			sendPutObject(sess, other)           // We see them appear
			sendPutObject(other.Session, player)  // They see us appear
		}
	}

	// 3. Players in OLD but not NEW: they left our view
	for _, other := range oldNearby {
		if _, isNew := newSet[other.SessionID]; !isNew {
			sendRemoveObject(sess, other.CharID)
			sendRemoveObject(other.Session, player.CharID)
		}
	}

	// --- NPC AOI: show/hide NPCs as player moves ---
	oldNpcs := ws.GetNearbyNpcs(curX, curY, player.MapID)
	newNpcs := ws.GetNearbyNpcs(destX, destY, player.MapID)

	oldNpcSet := make(map[int32]struct{}, len(oldNpcs))
	for _, n := range oldNpcs {
		oldNpcSet[n.ID] = struct{}{}
	}
	newNpcSet := make(map[int32]struct{}, len(newNpcs))
	for _, n := range newNpcs {
		newNpcSet[n.ID] = struct{}{}
	}

	// NPCs newly visible
	for _, n := range newNpcs {
		if _, wasOld := oldNpcSet[n.ID]; !wasOld {
			sendNpcPack(sess, n)
		}
	}
	// NPCs no longer visible
	for _, n := range oldNpcs {
		if _, isNew := newNpcSet[n.ID]; !isNew {
			sendRemoveObject(sess, n.ID)
		}
	}

	// --- Ground item AOI: show/hide ground items as player moves ---
	oldGnd := ws.GetNearbyGroundItems(curX, curY, player.MapID)
	newGnd := ws.GetNearbyGroundItems(destX, destY, player.MapID)

	oldGndSet := make(map[int32]struct{}, len(oldGnd))
	for _, g := range oldGnd {
		oldGndSet[g.ID] = struct{}{}
	}
	newGndSet := make(map[int32]struct{}, len(newGnd))
	for _, g := range newGnd {
		newGndSet[g.ID] = struct{}{}
	}

	for _, g := range newGnd {
		if _, wasOld := oldGndSet[g.ID]; !wasOld {
			sendDropItem(sess, g)
		}
	}
	for _, g := range oldGnd {
		if _, isNew := newGndSet[g.ID]; !isNew {
			sendRemoveObject(sess, g.ID)
		}
	}

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
