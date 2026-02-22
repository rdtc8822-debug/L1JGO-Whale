package event

import "github.com/l1jgo/server/internal/core/ecs"

// Phase 1 event types (minimal set).

type PlayerLoggedIn struct {
	EntityID    ecs.EntityID
	AccountName string
}

type PlayerDisconnected struct {
	EntityID  ecs.EntityID
	SessionID uint64
}
