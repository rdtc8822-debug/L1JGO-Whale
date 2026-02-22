package net

import "math/bits"

// Cipher is an exact port of the L1J 3.80C XOR rolling cipher (Cipher.java).
// It maintains separate encode (eb) and decode (db) key state, plus a 4-byte
// temporary buffer (tb) used during key update.
type Cipher struct {
	eb [8]byte // encode key bytes
	db [8]byte // decode key bytes
	tb [4]byte // temporary buffer
}

const (
	cipherMask1 = 0x9c30d539
	cipherMask2 = 0x930fd7e2
	cipherMask3 = 0x7c72e993
	cipherMask4 = 0x287effc3
)

// NewCipher creates a cipher initialized with the given seed, matching Cipher.java exactly.
func NewCipher(seed int32) *Cipher {
	c := &Cipher{}
	key := uint32(seed)

	keys := [2]uint32{
		key ^ cipherMask1,
		cipherMask2,
	}
	keys[0] = bits.RotateLeft32(keys[0], 0x13) // rotateLeft by 19 bits
	keys[1] ^= keys[0] ^ cipherMask3

	for i := 0; i < 2; i++ {
		for j := 0; j < 4; j++ {
			b := byte((keys[i] >> (j * 8)) & 0xff)
			c.eb[i*4+j] = b
			c.db[i*4+j] = b
		}
	}
	return c
}

// Encrypt encrypts data in place and returns it. Matches Cipher.java encrypt() exactly.
func (c *Cipher) Encrypt(data []byte) []byte {
	if len(data) < 4 {
		return data
	}

	// Save first 4 bytes to tb
	copy(c.tb[:], data[:4])

	// Forward XOR chain
	data[0] ^= c.eb[0]
	for i := 1; i < len(data); i++ {
		data[i] ^= data[i-1] ^ c.eb[i&7]
	}

	// Reverse scramble on first 4 bytes
	data[3] ^= c.eb[2]
	data[2] ^= c.eb[3] ^ data[3]
	data[1] ^= c.eb[4] ^ data[2]
	data[0] ^= c.eb[5] ^ data[1]

	c.update(c.eb[:], c.tb[:])
	return data
}

// Decrypt decrypts data in place and returns it. Matches Cipher.java decrypt() exactly.
func (c *Cipher) Decrypt(data []byte) []byte {
	if len(data) < 4 {
		return data
	}

	// Undo the scramble
	data[0] ^= c.db[5] ^ data[1]
	data[1] ^= c.db[4] ^ data[2]
	data[2] ^= c.db[3] ^ data[3]
	data[3] ^= c.db[2]

	// Reverse XOR chain (from end to start)
	for i := len(data) - 1; i >= 1; i-- {
		data[i] ^= data[i-1] ^ c.db[i&7]
	}
	data[0] ^= c.db[0]

	c.update(c.db[:], data)
	return data
}

// update modifies the key bytes using the reference data. Matches Cipher.java update() exactly.
func (c *Cipher) update(keyBytes []byte, ref []byte) {
	// XOR first 4 key bytes with ref
	for i := 0; i < 4; i++ {
		keyBytes[i] ^= ref[i]
	}

	// Compute int32 from bytes 4-7 (little-endian), add mask4
	val := uint32(keyBytes[4]) |
		uint32(keyBytes[5])<<8 |
		uint32(keyBytes[6])<<16 |
		uint32(keyBytes[7])<<24
	val += cipherMask4

	// Write back to bytes 4-7
	keyBytes[4] = byte(val)
	keyBytes[5] = byte(val >> 8)
	keyBytes[6] = byte(val >> 16)
	keyBytes[7] = byte(val >> 24)
}
