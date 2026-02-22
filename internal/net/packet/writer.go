package packet

import (
	"encoding/binary"

	"golang.org/x/text/encoding/traditionalchinese"
)

// Writer builds an L1J server packet. All multi-byte writes are little-endian.
// The final Bytes() output is padded to a 4-byte boundary (matching ServerBasePacket.java).
type Writer struct {
	buf []byte
}

func NewWriter() *Writer {
	return &Writer{buf: make([]byte, 0, 64)}
}

func NewWriterWithOpcode(opcode byte) *Writer {
	w := &Writer{buf: make([]byte, 0, 64)}
	w.WriteC(opcode)
	return w
}

// WriteC writes 1 byte.
func (w *Writer) WriteC(v byte) {
	w.buf = append(w.buf, v)
}

// WriteH writes 2 bytes little-endian.
func (w *Writer) WriteH(v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

// WriteD writes 4 bytes little-endian (signed or unsigned via cast).
func (w *Writer) WriteD(v int32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(v))
	w.buf = append(w.buf, b[:]...)
}

// WriteDU writes 4 bytes little-endian unsigned.
func (w *Writer) WriteDU(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

// WriteS writes a null-terminated string, converting UTF-8 to MS950 (Big5).
func (w *Writer) WriteS(s string) {
	if len(s) == 0 {
		w.buf = append(w.buf, 0) // just null terminator
		return
	}
	encoded, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(s))
	if err != nil {
		// Fallback: write raw bytes (works for pure ASCII)
		w.buf = append(w.buf, []byte(s)...)
	} else {
		w.buf = append(w.buf, encoded...)
	}
	w.buf = append(w.buf, 0) // null terminator
}

// WriteBytes writes raw bytes.
func (w *Writer) WriteBytes(b []byte) {
	w.buf = append(w.buf, b...)
}

// Bytes returns the packet content padded to a 4-byte boundary.
// This matches ServerBasePacket.getBytes() in Java.
func (w *Writer) Bytes() []byte {
	size := len(w.buf)
	padding := size % 4
	if padding != 0 {
		for i := padding; i < 4; i++ {
			w.buf = append(w.buf, 0)
		}
	}
	return w.buf
}

// RawBytes returns the packet content without padding (for init packet).
func (w *Writer) RawBytes() []byte {
	return w.buf
}

// Len returns the current unpadded length.
func (w *Writer) Len() int {
	return len(w.buf)
}
