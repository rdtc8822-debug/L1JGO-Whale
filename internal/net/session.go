package net

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// firstPacket is the L1J 3.80C Taiwan handshake constant.
var firstPacket = [11]byte{
	0x9d, 0xd1, 0xd6, 0x7a, 0xf4,
	0x62, 0xe7, 0xa0, 0x66, 0x02,
	0xfa,
}

// Session represents a single client connection. Network I/O runs in
// dedicated goroutines; game state is accessed only from the game loop.
type Session struct {
	ID   uint64
	conn net.Conn

	cipher *Cipher
	state  atomic.Int32 // packet.SessionState stored as int32
	mu     sync.Mutex   // protects conn writes during init

	InQueue  chan []byte // game loop reads packets from here
	OutQueue chan []byte // writer goroutine reads from here

	IP          string
	AccountName string
	CharName    string

	outBuf [][]byte // buffered packets, flushed by OutputSystem (game loop only)

	closeCh   chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool

	// Per-second packet rate limiter (readLoop goroutine only, no lock needed)
	pktPerSec  int   // max packets/sec (0 = unlimited)
	pktCount   int   // packets received this second
	pktResetAt int64 // unix second of last counter reset

	log *zap.Logger
}

func NewSession(conn net.Conn, id uint64, inSize, outSize, pktPerSec int, log *zap.Logger) *Session {
	s := &Session{
		ID:        id,
		conn:      conn,
		InQueue:   make(chan []byte, inSize),
		OutQueue:  make(chan []byte, outSize),
		IP:        conn.RemoteAddr().String(),
		closeCh:   make(chan struct{}),
		pktPerSec: pktPerSec,
		log:       log.With(zap.Uint64("session", id)),
	}
	s.state.Store(int32(packet.StateHandshake))
	return s
}

func (s *Session) State() packet.SessionState {
	return packet.SessionState(s.state.Load())
}

func (s *Session) SetState(st packet.SessionState) {
	s.state.Store(int32(st))
}

// Start sends the plaintext init packet, initializes the cipher, and
// launches the reader and writer goroutines.
func (s *Session) Start() {
	seed := rand.Int31n(0x7FFFFFFE) + 1 // positive non-zero int32

	// Build init packet (plaintext, written directly — no cipher, no sendPacket)
	// [2B LE length=18][1B opcode=150][4B LE seed][11B firstPacket]
	buf := make([]byte, 18)
	binary.LittleEndian.PutUint16(buf[0:2], 18)
	buf[2] = packet.S_OPCODE_INITPACKET
	binary.LittleEndian.PutUint32(buf[3:7], uint32(seed))
	copy(buf[7:18], firstPacket[:])

	s.mu.Lock()
	_, err := s.conn.Write(buf)
	s.mu.Unlock()
	if err != nil {
		s.log.Error("初始封包發送失敗", zap.Error(err))
		s.Close()
		return
	}

	// Initialize cipher with the seed
	s.cipher = NewCipher(seed)

	go s.readLoop()
	go s.writeLoop()
}

// Send buffers a packet for sending. The packet is not written to TCP until
// FlushOutput is called by OutputSystem at Phase 4.
// Called only from the game loop goroutine — no lock needed on outBuf.
func (s *Session) Send(data []byte) {
	if s.closed.Load() {
		return
	}
	s.outBuf = append(s.outBuf, data)
}

// FlushOutput drains the output buffer to OutQueue for the writeLoop goroutine.
// Called by OutputSystem at Phase 4 (once per tick).
// Non-blocking: if OutQueue is full, the session is disconnected (backpressure).
func (s *Session) FlushOutput() {
	for _, data := range s.outBuf {
		select {
		case s.OutQueue <- data:
		default:
			s.log.Warn("輸出佇列已滿，斷開慢速連線")
			s.Close()
			s.outBuf = s.outBuf[:0]
			return
		}
	}
	s.outBuf = s.outBuf[:0]
}

// Close gracefully shuts down the session.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.SetState(packet.StateDisconnecting)
		close(s.closeCh)
		s.conn.Close()
	})
}

func (s *Session) IsClosed() bool {
	return s.closed.Load()
}

// readLoop runs in its own goroutine. It reads frames from the TCP connection,
// decrypts them, and pushes them onto InQueue for the game loop to consume.
func (s *Session) readLoop() {
	defer s.Close()

	for {
		select {
		case <-s.closeCh:
			return
		default:
		}

		payload, err := ReadFrame(s.conn)
		if err != nil {
			if !s.closed.Load() {
				s.log.Debug("讀取錯誤", zap.Error(err))
			}
			return
		}

		decrypted := s.cipher.Decrypt(payload)

		// Per-second packet rate limiter
		if s.pktPerSec > 0 {
			now := time.Now().Unix()
			if now != s.pktResetAt {
				s.pktCount = 0
				s.pktResetAt = now
			}
			s.pktCount++
			if s.pktCount > s.pktPerSec {
				s.log.Warn("封包速率超限，斷開連線", zap.Int("pps", s.pktCount))
				return
			}
		}

		// Block until InQueue has space or session closes.
		// Java processes packets inline (no queue, no drops). Dropping C_MOVE
		// packets causes permanent position desync because the Taiwan client
		// mode uses server-tracked position. Blocking here is safe — the
		// readLoop goroutine is per-session, so it only blocks this client.
		select {
		case s.InQueue <- decrypted:
		case <-s.closeCh:
			return
		}
	}
}

// writeLoop runs in its own goroutine. It reads packets from OutQueue,
// encrypts them, and writes them as framed data to the TCP connection.
//
// 模擬 Java PacketSc 的節奏控制：封包間保持 1ms 間隔，避免客戶端
// 一次收到大量封包導致掉幀（天氣動畫變慢、NPC 停止呼吸等現象）。
func (s *Session) writeLoop() {
	defer s.Close()

	for {
		select {
		case data := <-s.OutQueue:
			if !s.writeOnePacket(data) {
				return
			}
			// 批量排出：若 OutQueue 還有更多封包，以 1ms 間隔送出
			for len(s.OutQueue) > 0 {
				select {
				case more := <-s.OutQueue:
					time.Sleep(time.Millisecond)
					if !s.writeOnePacket(more) {
						return
					}
				case <-s.closeCh:
					return
				}
			}
		case <-s.closeCh:
			return
		}
	}
}

// writeOnePacket 加密並寫入單一封包到 TCP socket。成功回傳 true。
func (s *Session) writeOnePacket(data []byte) bool {
	if len(data) > 0 {
		s.log.Debug("TX",
			zap.String("op", fmt.Sprintf("0x%02X(%d)", data[0], data[0])),
			zap.Int("len", len(data)),
		)
	}

	encrypted := make([]byte, len(data))
	copy(encrypted, data)
	s.cipher.Encrypt(encrypted)

	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := WriteFrame(s.conn, encrypted); err != nil {
		if !s.closed.Load() {
			s.log.Debug("寫入錯誤", zap.Error(err))
		}
		return false
	}
	return true
}
