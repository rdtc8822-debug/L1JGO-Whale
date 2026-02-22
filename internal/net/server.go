package net

import (
	"fmt"
	"net"
	"sync/atomic"

	"go.uber.org/zap"
)

// Server accepts TCP connections and creates Sessions.
// New/dead sessions are communicated to the game loop via channels.
type Server struct {
	listener net.Listener
	nextID   atomic.Uint64
	newConns chan *Session
	deadCh   chan uint64 // session IDs of dead sessions
	inSize   int
	outSize  int
	log      *zap.Logger
	closeCh  chan struct{}
}

func NewServer(bindAddr string, inSize, outSize int, log *zap.Logger) (*Server, error) {
	ln, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		listener: ln,
		newConns: make(chan *Session, 64),
		deadCh:   make(chan uint64, 64),
		inSize:   inSize,
		outSize:  outSize,
		log:      log,
		closeCh:  make(chan struct{}),
	}
	return s, nil
}

// AcceptLoop runs in its own goroutine. It accepts connections, creates
// sessions, sends the init packet, and pushes them onto the newConns channel.
func (s *Server) AcceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closeCh:
				return // server shutting down
			default:
			}
			s.log.Error("連線接受失敗", zap.Error(err))
			continue
		}

		id := s.nextID.Add(1)
		sess := NewSession(conn, id, s.inSize, s.outSize, s.log)
		sess.Start()

		s.log.Info(fmt.Sprintf("玩家連線  session=%d  ip=%s", id, sess.IP))

		select {
		case s.newConns <- sess:
		default:
			s.log.Warn("連線佇列已滿，拒絕新連線")
			sess.Close()
		}
	}
}

// NewSessions returns the channel of newly connected sessions.
func (s *Server) NewSessions() <-chan *Session {
	return s.newConns
}

// NotifyDead reports a dead session ID to the game loop.
func (s *Server) NotifyDead(sessionID uint64) {
	select {
	case s.deadCh <- sessionID:
	default:
	}
}

// DeadSessions returns the channel of dead session IDs.
func (s *Server) DeadSessions() <-chan uint64 {
	return s.deadCh
}

// Shutdown stops accepting new connections.
func (s *Server) Shutdown() {
	close(s.closeCh)
	s.listener.Close()
}

// Addr returns the listener's address.
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}
