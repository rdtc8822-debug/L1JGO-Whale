package packet

import (
	"encoding/binary"

	"golang.org/x/text/encoding/traditionalchinese"
)

// Reader reads L1J packet fields from a decrypted payload.
// Byte 0 is always the opcode.
type Reader struct {
	data []byte
	off  int
}

func NewReader(data []byte) *Reader {
	return &Reader{data: data, off: 1} // skip opcode byte
}

func (r *Reader) Opcode() byte {
	if len(r.data) == 0 {
		return 0
	}
	return r.data[0]
}

// ReadC reads 1 unsigned byte.
func (r *Reader) ReadC() byte {
	if r.off >= len(r.data) {
		return 0
	}
	v := r.data[r.off]
	r.off++
	return v
}

// ReadH reads 2 bytes as little-endian uint16.
func (r *Reader) ReadH() uint16 {
	if r.off+2 > len(r.data) {
		return 0
	}
	v := binary.LittleEndian.Uint16(r.data[r.off:])
	r.off += 2
	return v
}

// ReadD reads 4 bytes as little-endian int32.
func (r *Reader) ReadD() int32 {
	if r.off+4 > len(r.data) {
		return 0
	}
	v := int32(binary.LittleEndian.Uint32(r.data[r.off:]))
	r.off += 4
	return v
}

// ReadS reads a null-terminated MS950 (Big5) string and returns UTF-8.
func (r *Reader) ReadS() string {
	start := r.off
	for r.off < len(r.data) {
		if r.data[r.off] == 0 {
			raw := r.data[start:r.off]
			r.off++ // skip null terminator
			return ms950ToUTF8(raw)
		}
		r.off++
	}
	return ms950ToUTF8(r.data[start:r.off])
}

// ms950ToUTF8 converts MS950 (Big5) bytes to a UTF-8 string.
// Pure ASCII passes through unchanged; only multi-byte sequences are decoded.
func ms950ToUTF8(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	// Fast path: if all bytes are ASCII, no conversion needed
	allASCII := true
	for _, b := range raw {
		if b >= 0x80 {
			allASCII = false
			break
		}
	}
	if allASCII {
		return string(raw)
	}
	decoded, err := traditionalchinese.Big5.NewDecoder().Bytes(raw)
	if err != nil {
		return string(raw) // fallback to raw bytes
	}
	return string(decoded)
}

// ReadBytes reads n raw bytes.
func (r *Reader) ReadBytes(n int) []byte {
	if r.off+n > len(r.data) {
		remaining := r.data[r.off:]
		r.off = len(r.data)
		return remaining
	}
	b := make([]byte, n)
	copy(b, r.data[r.off:r.off+n])
	r.off += n
	return b
}

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int {
	return len(r.data) - r.off
}
