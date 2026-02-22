package packet

import (
	"fmt"

	"go.uber.org/zap"
)

// SessionState represents the session's current protocol phase.
type SessionState int

const (
	StateHandshake         SessionState = iota
	StateVersionOK                      // received version, awaiting login
	StateAuthenticated                  // logged in, at character select
	StateInWorld                        // playing
	StateReturningToSelect              // returning to char select from world
	StateDisconnecting
)

func (s SessionState) String() string {
	switch s {
	case StateHandshake:
		return "Handshake"
	case StateVersionOK:
		return "VersionOK"
	case StateAuthenticated:
		return "Authenticated"
	case StateInWorld:
		return "InWorld"
	case StateReturningToSelect:
		return "ReturningToSelect"
	case StateDisconnecting:
		return "Disconnecting"
	default:
		return fmt.Sprintf("Unknown(%d)", int(s))
	}
}

// HandlerFunc is the callback signature for packet handlers.
// The session pointer is passed as an opaque interface to avoid import cycles.
type HandlerFunc func(sess any, r *Reader)

type handlerEntry struct {
	fn            HandlerFunc
	allowedStates map[SessionState]bool
}

// Registry maps opcodes to handlers with state-based access control.
type Registry struct {
	handlers map[byte]*handlerEntry
	log      *zap.Logger
}

func NewRegistry(log *zap.Logger) *Registry {
	return &Registry{
		handlers: make(map[byte]*handlerEntry),
		log:      log,
	}
}

// Register maps an opcode to a handler, restricted to the given session states.
func (reg *Registry) Register(opcode byte, states []SessionState, fn HandlerFunc) {
	allowed := make(map[SessionState]bool, len(states))
	for _, s := range states {
		allowed[s] = true
	}
	reg.handlers[opcode] = &handlerEntry{
		fn:            fn,
		allowedStates: allowed,
	}
}

// Dispatch finds the handler for the opcode in data[0], validates the session
// state, and calls the handler. Returns an error if the opcode is unknown or
// the session state is not allowed.
func (reg *Registry) Dispatch(sess any, state SessionState, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty packet")
	}
	opcode := data[0]
	reg.log.Debug("收到封包",
		zap.Uint8("opcode", opcode),
		zap.Int("size", len(data)),
		zap.String("state", state.String()),
	)

	entry, ok := reg.handlers[opcode]
	if !ok {
		reg.log.Debug("未知操作碼", zap.Uint8("opcode", opcode), zap.String("state", state.String()))
		return nil // silently ignore unknown opcodes
	}

	if !entry.allowedStates[state] {
		reg.log.Warn("操作碼在此狀態下不允許",
			zap.Uint8("opcode", opcode),
			zap.String("state", state.String()),
		)
		return fmt.Errorf("opcode %d not allowed in state %s", opcode, state)
	}

	r := NewReader(data)
	if err := reg.safeCall(entry.fn, sess, r, opcode); err != nil {
		return err
	}
	return nil
}

// safeCall executes a handler with panic recovery to prevent a single
// bad packet from crashing the entire game loop.
func (reg *Registry) safeCall(fn HandlerFunc, sess any, r *Reader, opcode byte) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			reg.log.Error("處理器 panic 已恢復",
				zap.Uint8("opcode", opcode),
				zap.Any("panic", rec),
			)
			err = fmt.Errorf("handler panic for opcode %d: %v", opcode, rec)
		}
	}()
	fn(sess, r)
	return nil
}
