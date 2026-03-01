package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// Warehouse types: DB storage value (wh_type column).
const (
	WhTypePersonal  int16 = 3
	WhTypeElf       int16 = 4
	WhTypeClan      int16 = 5
	WhTypeCharacter int16 = 6 // 角色專屬倉庫（每角色獨立，非帳號共享）
)

// retrieveListType 回傳 S_RetrieveList 封包中的 type 字節。
// Java: Personal=3, Clan=5, Elf=9, Character=18
func retrieveListType(whType int16) byte {
	switch whType {
	case WhTypeElf:
		return 9
	case WhTypeCharacter:
		return 18 // Java S_RetrieveChaList: writeC(18)
	default:
		return byte(whType) // 3 or 5
	}
}

// ResultTypeToWhType maps C_Result resultType to warehouse DB type.
// Java C_Result: 2/3=personal, 4/5=clan, 8/9=elf, 17/18=character
func ResultTypeToWhType(resultType byte) (whType int16, isDeposit bool, ok bool) {
	switch resultType {
	case 2:
		return WhTypePersonal, true, true
	case 3:
		return WhTypePersonal, false, true
	case 4:
		return WhTypeClan, true, true
	case 5:
		return WhTypeClan, false, true
	case 8:
		return WhTypeElf, true, true
	case 9:
		return WhTypeElf, false, true
	case 17:
		return WhTypeCharacter, true, true
	case 18:
		return WhTypeCharacter, false, true
	default:
		return 0, false, false
	}
}

// HandleWarehouseResult processes warehouse operations from C_BUY_SELL (opcode 161).
// Called from HandleBuySell when resultType >= 2.
// 薄層：解封包 → 委派 deps.Warehouse.HandleWarehouseOp()。
func HandleWarehouseResult(sess *net.Session, r *packet.Reader, resultType byte, count int, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}
	if deps.Warehouse == nil {
		return
	}
	deps.Warehouse.HandleWarehouseOp(sess, r, resultType, count, player)
}

// ========================================================================
//  封包建構器（供 system/warehouse.go 呼叫）
// ========================================================================

// SendWarehouseList sends S_RETRIEVE_LIST (opcode 176) — warehouse item list.
// Format matches Java S_RetrieveList / S_RetrieveElfList / S_RetrievePledgeList:
//
//	[C opcode=176][D npcObjID][H itemCount][C warehouseType]
//	Per item: [D objID][C useType][H invGfx][C bless][D count][C identified][S viewName]
//	Trailing: [D fee] (personal/clan only, omitted for elf)
//
// Type byte: 3=personal, 5=clan, 9=elf, 18=character.
// 3.80C 客戶端倉庫視窗內建提取/存入 tab。
func SendWarehouseList(sess *net.Session, npcObjID int32, whType int16, items []*world.WarehouseCache, fee int32) {
	typeCode := retrieveListType(whType)

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_RETRIEVE_LIST)
	w.WriteD(npcObjID)
	w.WriteH(uint16(len(items)))
	w.WriteC(typeCode)

	for _, it := range items {
		viewName := it.Name
		if it.EnchantLvl > 0 {
			viewName = fmt.Sprintf("+%d %s", it.EnchantLvl, viewName)
		} else if it.EnchantLvl < 0 {
			viewName = fmt.Sprintf("%d %s", it.EnchantLvl, viewName)
		}

		w.WriteD(it.TempObjID) // item object ID
		if whType == WhTypeElf {
			w.WriteC(0) // elf warehouse: useType always 0
		} else {
			w.WriteC(it.UseType) // personal/clan: actual useType
		}
		w.WriteH(uint16(it.InvGfx)) // inventory graphic
		w.WriteC(byte(it.Bless))     // bless state
		w.WriteD(it.Count)           // stack count
		if it.Identified {
			w.WriteC(1)
		} else {
			w.WriteC(0)
		}
		w.WriteS(viewName) // display name
	}

	// Trailing: per-item withdrawal fee (30 adena). Client displays at top-left.
	if whType != WhTypeElf {
		w.WriteD(fee)
	}

	sess.Send(w.Bytes())
}

// ========================================================================
//  倉庫密碼系統（C_Password / opcode 13）
// ========================================================================

// whPasswordTable 是客戶端密碼編碼表。
// 客戶端將 6 位數密碼的每一位轉成對應的 int32 值發送。
// Java: C_Password.java static { password.add(...) }
var whPasswordTable = [10]int32{
	994303243, // 0
	994303242, // 1
	994303241, // 2
	994303240, // 3
	994303247, // 4
	994303246, // 5
	994303245, // 6
	994303244, // 7
	994303235, // 8
	994303234, // 9
}

// decodeWarehousePassword 從封包讀取 6 個 int32，查表解碼為 6 位數密碼。
// 若任何一位查表失敗（不在表中），返回負數表示無效。
func decodeWarehousePassword(r *packet.Reader) int32 {
	result := int32(0)
	multipliers := [6]int32{100000, 10000, 1000, 100, 10, 1}
	for i := 0; i < 6; i++ {
		raw := r.ReadD()
		digit := int32(-1)
		for idx, val := range whPasswordTable {
			if int32(val) == int32(raw) {
				digit = int32(idx)
				break
			}
		}
		if digit < 0 {
			// 消耗剩餘位元組（避免偏移錯誤）
			for j := i + 1; j < 6; j++ {
				_ = r.ReadD()
			}
			return -1
		}
		result += digit * multipliers[i]
	}
	return result
}

// HandleWarehousePassword 處理 C_Password (opcode 13)。
// Java: C_Password.java — 倉庫密碼設定/變更/驗證。
// 封包格式: [C type][D×6 pass1][D×6 pass2 (type=0)][D npcObjID (type=1/2/4)]
func HandleWarehousePassword(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	whType := r.ReadC() // 0=設定密碼, 1=個人倉庫, 2=血盟倉庫, 4=角色倉庫
	pass1 := decodeWarehousePassword(r)

	if whType == 0 {
		// 設定或變更密碼
		pass2 := decodeWarehousePassword(r)
		handleWarehousePasswordChange(sess, player, pass1, pass2, deps)
		return
	}

	// type=1/2/4：密碼驗證後開啟倉庫
	npcObjID := int32(r.ReadD())

	if player.WarehousePassword != pass1 {
		SendServerMessage(sess, 835) // 倉庫密碼錯誤
		return
	}

	// 等級限制（Java: pc.getLevel() >= 5）
	if player.Level < 5 {
		return
	}

	switch whType {
	case 1: // 個人倉庫
		// Java: NPC 60028 對精靈用 S_RetrieveElfList，其餘用 S_RetrieveList
		npc := deps.World.GetNpc(npcObjID)
		if npc == nil {
			return
		}
		if npc.NpcID == 60028 && player.ClassType == 2 { // 精靈種族
			deps.Warehouse.OpenWarehouse(sess, player, npcObjID, WhTypeElf)
		} else {
			deps.Warehouse.OpenWarehouse(sess, player, npcObjID, WhTypePersonal)
		}

	case 2: // 血盟倉庫
		if player.ClanID == 0 {
			SendServerMessage(sess, 208) // 尚未加入血盟
			return
		}
		if player.ClanRank == 2 {
			SendServerMessage(sess, 728) // 倉庫管理員才可訪問
			return
		}
		deps.Warehouse.OpenClanWarehouse(sess, player, npcObjID)

	case 4: // 角色專屬倉庫
		deps.Warehouse.OpenWarehouse(sess, player, npcObjID, WhTypeCharacter)
	}
}

// handleWarehousePasswordChange 處理密碼設定/變更（type=0）。
func handleWarehousePasswordChange(sess *net.Session, player *world.PlayerInfo, pass1, pass2 int32, deps *Deps) {
	if pass1 < 0 && pass2 < 0 {
		// 兩組密碼都無效
		SendServerMessage(sess, 79)
		return
	}

	if pass1 < 0 && player.WarehousePassword == 0 {
		// 首次設定密碼（帳號尚無倉庫密碼）
		player.WarehousePassword = pass2
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := deps.AccountRepo.UpdateWarehousePassword(ctx, sess.AccountName, pass2); err != nil {
			deps.Log.Error("更新倉庫密碼資料庫錯誤", zap.Error(err))
		}
		SendSystemMessage(sess, "倉庫密碼設定完成，請牢記您的新密碼。")
		return
	}

	if pass1 > 0 && pass1 == player.WarehousePassword {
		if pass1 == pass2 {
			// 新舊密碼相同
			SendServerMessage(sess, 342)
			return
		}
		// 變更密碼
		player.WarehousePassword = pass2
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := deps.AccountRepo.UpdateWarehousePassword(ctx, sess.AccountName, pass2); err != nil {
			deps.Log.Error("更新倉庫密碼資料庫錯誤", zap.Error(err))
		}
		return
	}

	// 密碼錯誤
	SendServerMessage(sess, 835)
}
