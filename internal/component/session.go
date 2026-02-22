package component

// SessionRef links an ECS entity to its network session.
// This is a reference, not the session itself — the session lives in net/.
type SessionRef struct {
	SessionID uint64
}
