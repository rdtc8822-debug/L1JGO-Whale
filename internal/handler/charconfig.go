package handler

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleCharConfig processes C_SAVEIO (opcode 244).
// Client sends the hotkey bar / UI config as a raw binary blob.
// Java: C_CharcterConfig — readD (length header), readByte (ALL remaining bytes).
//
// Java stores readD()-3 as the "length" column and readByte() as the "data" column.
// These are NOT the same value — readD()-3 != len(readByte()) in general.
// We must preserve the readD()-3 value to reconstruct the exact packet on login.
//
// Storage format in DB (BYTEA): [4 bytes LE: readD()-3] [remaining config bytes]
func HandleCharConfig(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Java: int length = readD() - 3;
	lengthField := r.ReadD()
	javaLength := lengthField - 3

	// Java: byte data[] = readByte(); — reads ALL remaining bytes
	remaining := r.Remaining()
	if remaining <= 0 || remaining > 8192 {
		return
	}
	configData := r.ReadBytes(remaining)

	deps.Log.Info(fmt.Sprintf("C_SAVEIO: 儲存角色設定  角色=%s  角色ID=%d  readD=%d  javaLength=%d  設定位元組=%d", player.Name, player.CharID, lengthField, javaLength, len(configData)))

	// Build storage blob: [4 bytes LE: javaLength] + [config data bytes]
	blob := make([]byte, 4+len(configData))
	binary.LittleEndian.PutUint32(blob[:4], uint32(javaLength))
	copy(blob[4:], configData)

	// Save to DB asynchronously (non-blocking for game loop)
	go func(charID int32, b []byte) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := deps.CharRepo.SaveCharConfig(ctx, charID, b); err != nil {
			deps.Log.Error("儲存角色設定失敗",
				zap.Int32("charID", charID),
				zap.Error(err),
			)
		}
	}(player.CharID, blob)
}

// sendCharConfig sends S_CharacterConfig (opcode 250, sub-type 41) — hotkey/UI config.
// Java: writeD(length) + writeByte(data), where length = the stored readD()-3 value.
//
// Storage format: [4 bytes LE: javaLength] [config data bytes]
func sendCharConfig(sess *net.Session, blob []byte) {
	if len(blob) < 5 {
		return // need at least 4-byte length prefix + 1 byte config data
	}

	javaLength := int32(binary.LittleEndian.Uint32(blob[:4]))
	configData := blob[4:]

	if javaLength <= 0 {
		return
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(41) // sub-opcode: CHARACTER_CONFIG
	w.WriteD(javaLength)
	w.WriteBytes(configData)
	sess.Send(w.Bytes())
}
