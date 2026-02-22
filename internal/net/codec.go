package net

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ReadFrame reads one L1J packet frame from r.
// Wire format: [2 bytes LE: total length including header][payload].
// Returns the payload bytes (without the 2-byte length header).
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}

	totalLen := int(binary.LittleEndian.Uint16(header[:]))
	payloadLen := totalLen - 2
	if payloadLen <= 0 || payloadLen > 65533 {
		return nil, fmt.Errorf("invalid frame length: %d", totalLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read frame payload (%d bytes): %w", payloadLen, err)
	}
	return payload, nil
}

// WriteFrame writes one L1J packet frame to w.
// Wire format: [2 bytes LE: len(data)+2][data].
func WriteFrame(w io.Writer, data []byte) error {
	totalLen := len(data) + 2
	var header [2]byte
	binary.LittleEndian.PutUint16(header[:], uint16(totalLen))

	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}
