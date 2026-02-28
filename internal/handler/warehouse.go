package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
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
