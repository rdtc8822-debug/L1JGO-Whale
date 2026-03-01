package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// HandleEnterWorld processes C_ENTER_WORLD (opcode 137).
// Packet order matches Java: LoginGame → InvList → OwnCharStatus → MapID → OwnCharPack → SPMR → Weather
func HandleEnterWorld(sess *net.Session, r *packet.Reader, deps *Deps) {
	charName := r.ReadS()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Load and validate character
	ch, err := deps.CharRepo.LoadByName(ctx, charName)
	if err != nil || ch == nil {
		deps.Log.Warn("進入世界: 找不到角色", zap.String("name", charName))
		sess.Close()
		return
	}
	if ch.AccountName != sess.AccountName {
		deps.Log.Warn("進入世界: 帳號不符",
			zap.String("char", charName),
			zap.String("account", sess.AccountName),
		)
		sess.Close()
		return
	}

	sess.CharName = charName
	sess.SetState(packet.StateInWorld)

	deps.Log.Info(fmt.Sprintf("角色進入世界  帳號=%s  角色=%s", sess.AccountName, charName))

	// Register player in world state
	player := &world.PlayerInfo{
		SessionID: sess.ID,
		Session:   sess,
		CharID:    ch.ID,
		Name:      ch.Name,
		X:         ch.X,
		Y:         ch.Y,
		MapID:     ch.MapID,
		Heading:   ch.Heading,
		ClassID:   ch.ClassID,
		ClassType: ch.ClassType,
		Level:     ch.Level,
		Lawful:    ch.Lawful,
		Title:     ch.Title,
		ClanID:    ch.ClanID,
		ClanName:  ch.ClanName,
		ClanRank:  ch.ClanRank,
		HP:        ch.HP,
		MaxHP:     ch.MaxHP,
		MP:        ch.MP,
		MaxMP:     ch.MaxMP,
		Str:       ch.Str,
		Dex:       ch.Dex,
		Con:       ch.Con,
		Wis:       ch.Wis,
		Intel:     ch.Intel,
		Cha:       ch.Cha,
		Exp:        int32(ch.Exp),
		BonusStats:  ch.BonusStats,
		ElixirStats: ch.ElixirStats,
		Food:        40, // Java: initial food = 40 (max 225, increased by eating food items)
		PKCount:     ch.PKCount,
		Karma:       ch.Karma,
		Inv:        world.NewInventory(),
	}
	// 載入帳號的倉庫密碼
	if deps.AccountRepo != nil {
		acct, acctErr := deps.AccountRepo.Load(ctx, sess.AccountName)
		if acctErr == nil && acct != nil {
			player.WarehousePassword = acct.WarehousePassword
		}
	}

	deps.World.AddPlayer(player)

	// Load inventory from DB (or give starting gold if empty)
	loadInventoryFromDB(player, deps)

	// Load bookmarks from DB (JSONB column)
	loadBookmarksFromDB(player, deps)

	// Load known spells from DB (JSONB column)
	loadKnownSpellsFromDB(player, deps)

	// 從 DB 載入限時地圖已使用時間（JSONB column）
	loadMapTimesFromDB(player, deps)

	// Load buddy list from DB
	loadBuddiesFromDB(player, deps)

	// Load exclude/block list from DB
	loadExcludesFromDB(player, deps)

	// 初始化裝備屬性（偵測套裝 + 設定基礎 AC + 計算裝備加成）
	if deps.Equip != nil {
		deps.Equip.InitEquipStats(player)
	}

	// Restore persisted buffs (including polymorph state)
	loadAndRestoreBuffs(player, deps)

	// --- 發送初始化封包（順序參考 Java C_LoginToServer）---

	// 1. S_ENTER_WORLD_CHECK (opcode 223) — LoginToGame
	sendLoginGame(sess, ch.ClanID, ch.ID)

	// 2. S_ADD_INVENTORY_BATCH (opcode 5) — 背包列表
	sendInvList(sess, player.Inv, deps.Items)

	// 3. S_STATUS (opcode 8) — 角色狀態（使用 PlayerInfo 即時數據）
	sendPlayerStatus(sess, player)

	// 4. S_WORLD (opcode 206) — 地圖 ID
	sendMapID(sess, uint16(ch.MapID), false)

	// 5. S_PUT_OBJECT (opcode 87) — 自己角色外觀（支援變身 GFX）
	sendOwnCharPack(sess, ch, player.CurrentWeapon, PlayerGfx(player))

	// 6. S_MAGIC_STATUS (opcode 37) — SP/MR（含裝備 + buff）
	sendMagicStatus(sess, byte(player.SP), uint16(player.MR))

	// 7. S_WEATHER (opcode 115)
	sendWeather(sess, deps.World.Weather)

	// 8. S_ABILITY_SCORES (opcode 174) — AC + 屬性抗性
	sendAbilityScores(sess, player)

	// 9. S_SkillList (opcode 164) — 已學魔法
	if deps.Skills != nil {
		var spells []*data.SkillInfo
		for _, sid := range player.KnownSpells {
			if sk := deps.Skills.Get(sid); sk != nil {
				spells = append(spells, sk)
			}
		}
		sendSkillList(sess, spells)
	}

	// 9b. S_EquipmentSlot (opcode 64, sub-type 0x42) — 已裝備欄位
	if deps.Equip != nil {
		deps.Equip.SendEquipList(sess, player)
	}

	// 10. 已儲存書籤
	SendAllBookmarks(sess, player.Bookmarks)

	// 11. 角色設定（F5-F12 快捷鍵、UI 位置）
	loadAndSendCharConfig(sess, ch.ID, deps)

	// 11b. S_CharResetInfo (opcode 64, sub-type 0x04) — 角色屬性配點資訊
	sendCharResetInfo(sess, ch, player, deps)

	// 12. 血盟資訊
	if player.ClanID > 0 {
		sendClanName(sess, player.CharID, player.ClanName, player.ClanID, true)
		clan := deps.World.Clans.GetClan(player.ClanID)
		if clan != nil {
			sendPledgeEmblemStatus(sess, int(clan.EmblemStatus))
		}
		sendClanAttention(sess)
	}

	// 12b. S_Karma — 善惡值
	SendKarma(sess, player.Karma)

	// 13. 屬性配點對話框（等級 51+）
	if player.Level >= bonusStatMinLevel {
		available := player.Level - 50 - player.BonusStats
		totalStats := player.Str + player.Dex + player.Con + player.Wis + player.Intel + player.Cha
		if available > 0 && totalStats < maxTotalStats {
			sendRaiseAttrDialog(sess, player.CharID)
		}
	}

	// 初始化 Known 集合（VisibilitySystem 用於 AOI diff）
	player.Known = world.NewKnownEntities()

	// 初始化限時地圖計時器
	if player.MapTimeUsed == nil {
		player.MapTimeUsed = make(map[int]int)
	}

	// 檢查是否在限時地圖中（斷線重連場景）
	OnEnterTimedMap(sess, player, player.MapID)

	// --- 發送附近玩家（AOI）+ 封鎖格子 + 填入 Known ---
	nearby := deps.World.GetNearbyPlayers(ch.X, ch.Y, ch.MapID, sess.ID)
	for _, other := range nearby {
		SendPutObject(sess, other)
		player.Known.Players[other.CharID] = world.KnownPos{X: other.X, Y: other.Y}
		SendPutObject(other.Session, player)
	}

	// --- 發送附近 NPC + 封鎖格子 + 填入 Known ---
	nearbyNpcs := deps.World.GetNearbyNpcs(ch.X, ch.Y, ch.MapID)
	for _, npc := range nearbyNpcs {
		SendNpcPack(sess, npc)
		player.Known.Npcs[npc.ID] = world.KnownPos{X: npc.X, Y: npc.Y}
	}

	// --- 發送附近寵伴 + 封鎖格子 + 填入 Known ---
	nearbySum := deps.World.GetNearbySummons(ch.X, ch.Y, ch.MapID)
	for _, sum := range nearbySum {
		isOwner := sum.OwnerCharID == player.CharID
		masterName := ""
		if master := deps.World.GetByCharID(sum.OwnerCharID); master != nil {
			masterName = master.Name
		}
		SendSummonPack(sess, sum, isOwner, masterName)
		player.Known.Summons[sum.ID] = world.KnownPos{X: sum.X, Y: sum.Y}
	}
	nearbyDolls := deps.World.GetNearbyDolls(ch.X, ch.Y, ch.MapID)
	for _, doll := range nearbyDolls {
		masterName := ""
		if master := deps.World.GetByCharID(doll.OwnerCharID); master != nil {
			masterName = master.Name
		}
		SendDollPack(sess, doll, masterName)
		player.Known.Dolls[doll.ID] = world.KnownPos{X: doll.X, Y: doll.Y}
	}
	nearbyFollowers := deps.World.GetNearbyFollowers(ch.X, ch.Y, ch.MapID)
	for _, f := range nearbyFollowers {
		SendFollowerPack(sess, f)
		player.Known.Followers[f.ID] = world.KnownPos{X: f.X, Y: f.Y}
	}
	nearbyPets := deps.World.GetNearbyPets(ch.X, ch.Y, ch.MapID)
	for _, pet := range nearbyPets {
		isOwner := pet.OwnerCharID == player.CharID
		masterName := ""
		if master := deps.World.GetByCharID(pet.OwnerCharID); master != nil {
			masterName = master.Name
		}
		SendPetPack(sess, pet, isOwner, masterName)
		player.Known.Pets[pet.ID] = world.KnownPos{X: pet.X, Y: pet.Y}
	}

	// --- 發送附近地面物品 + 填入 Known ---
	nearbyGnd := deps.World.GetNearbyGroundItems(ch.X, ch.Y, ch.MapID)
	for _, g := range nearbyGnd {
		SendDropItem(sess, g)
		player.Known.GroundItems[g.ID] = world.KnownPos{X: g.X, Y: g.Y}
	}

	// --- 發送附近門 + 填入 Known ---
	nearbyDoors := deps.World.GetNearbyDoors(ch.X, ch.Y, ch.MapID)
	for _, d := range nearbyDoors {
		SendDoorPerceive(sess, d)
		player.Known.Doors[d.ID] = world.KnownPos{X: d.X, Y: d.Y}
	}

	// Mark player tile as impassable (for NPC pathfinding, matching Java)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, player.X, player.Y, true)
	}

	// --- 恢復 buff 圖示（必須在所有初始化封包之後）---
	sendRestoredBuffIcons(player, deps)

	// S_GameTime — 最後發送，避免干擾客戶端初始化
	sendGameTime(sess, world.GameTimeNow().Seconds())
}

func sendLoginGame(sess *net.Session, clanID int32, clanMemberID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ENTER_WORLD_CHECK)
	w.WriteC(0x03) // language
	if clanID > 0 {
		w.WriteD(clanMemberID) // clan member ID — must be non-zero for client to recognize clan membership
	} else {
		w.WriteC(0x53)
		w.WriteC(0x01)
		w.WriteC(0x00)
		w.WriteC(0x8b)
	}
	w.WriteC(0x9c) // unknown
	w.WriteC(0x1f) // unknown
	sess.Send(w.Bytes())
}

// loadInventoryFromDB loads saved items from DB, or gives starting gold if no items exist.
func loadInventoryFromDB(player *world.PlayerInfo, deps *Deps) {
	if deps.ItemRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		items, err := deps.ItemRepo.LoadByCharID(ctx, player.CharID)
		if err != nil {
			deps.Log.Error("載入背包失敗", zap.String("name", player.Name), zap.Error(err))
		} else if len(items) > 0 {
			for _, row := range items {
				itemInfo := deps.Items.Get(row.ItemID)
				if itemInfo == nil {
					continue
				}
				stackable := itemInfo.Stackable || row.ItemID == world.AdenaItemID
				invItem := player.Inv.AddItemWithID(
					row.ObjID, // preserve persisted ObjectID for shortcut bar stability (0 → generate new)
					row.ItemID, row.Count, itemInfo.Name, itemInfo.InvGfx,
					itemInfo.Weight, stackable, byte(row.Bless),
				)
				invItem.EnchantLvl = int8(row.EnchantLvl)
				invItem.Identified = row.Identified
				invItem.UseType = itemInfo.UseTypeID
				invItem.Durability = int8(row.Durability)
				if row.Equipped && row.EquipSlot > 0 {
					invItem.Equipped = true
					slot := world.EquipSlot(row.EquipSlot)
					player.Equip.Set(slot, invItem)
					if slot == world.SlotWeapon {
						player.CurrentWeapon = world.WeaponVisualID(itemInfo.Type)
					}
				}
			}
			return
		}
	}

	// No saved items — give starting gold (bless=1 = normal)
	adenaInfo := deps.Items.Get(world.AdenaItemID)
	if adenaInfo != nil {
		player.Inv.AddItem(world.AdenaItemID, 20000, adenaInfo.Name, adenaInfo.InvGfx, 0, true, byte(adenaInfo.Bless))
	} else {
		player.Inv.AddItem(world.AdenaItemID, 20000, "金幣", 318, 0, true, 1)
	}
}

// loadBookmarksFromDB loads saved bookmarks from the JSONB column.
func loadBookmarksFromDB(player *world.PlayerInfo, deps *Deps) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := deps.CharRepo.LoadBookmarks(ctx, player.Name)
	if err != nil {
		deps.Log.Error("載入書籤失敗", zap.String("name", player.Name), zap.Error(err))
		return
	}
	for _, row := range rows {
		player.Bookmarks = append(player.Bookmarks, world.Bookmark{
			ID:    row.ID,
			Name:  row.Name,
			X:     row.X,
			Y:     row.Y,
			MapID: row.MapID,
		})
	}
}

// loadKnownSpellsFromDB loads saved known spells from the JSONB column.
func loadKnownSpellsFromDB(player *world.PlayerInfo, deps *Deps) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	spells, err := deps.CharRepo.LoadKnownSpells(ctx, player.Name)
	if err != nil {
		deps.Log.Error("載入魔法書失敗", zap.String("name", player.Name), zap.Error(err))
		return
	}
	player.KnownSpells = spells
}

// loadMapTimesFromDB 從 JSONB 欄位載入限時地圖已使用時間。
func loadMapTimesFromDB(player *world.PlayerInfo, deps *Deps) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mt, err := deps.CharRepo.LoadMapTimes(ctx, player.Name)
	if err != nil {
		deps.Log.Error("載入限時地圖時間失敗", zap.String("name", player.Name), zap.Error(err))
		return
	}
	if mt != nil {
		player.MapTimeUsed = mt
	}
}

// SendMapID 匯出 sendMapID — 供 system 套件發送地圖切換封包。
func SendMapID(sess *net.Session, mapID uint16, underwater bool) {
	sendMapID(sess, mapID, underwater)
}

func sendMapID(sess *net.Session, mapID uint16, underwater bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_WORLD)
	w.WriteH(mapID)
	if underwater {
		w.WriteC(1)
	} else {
		w.WriteC(0)
	}
	w.WriteD(0)
	w.WriteD(0)
	w.WriteD(0)
	sess.Send(w.Bytes())
}

// sendOwnCharPack sends S_PUT_OBJECT (opcode 87) for the player's own character.
// Status byte uses 0x04 (bit 2 = PC flag) matching Java S_OwnCharPack.
// gfxID: use PlayerGfx(player) to support polymorph appearance on login.
func sendOwnCharPack(sess *net.Session, ch *persist.CharacterRow, currentWeapon byte, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(ch.X))
	w.WriteH(uint16(ch.Y))
	w.WriteD(ch.ID)
	w.WriteH(uint16(gfxID))
	w.WriteC(currentWeapon)    // current weapon
	w.WriteC(byte(ch.Heading))
	w.WriteC(0)                // light size
	w.WriteC(0)                // move speed
	w.WriteD(1)                // unknown (always 1)
	w.WriteH(uint16(ch.Lawful))
	w.WriteS(ch.Name)
	w.WriteS(ch.Title)
	w.WriteC(0x04)             // status flags: bit 2 = PC
	w.WriteD(0)                // clan emblem ID
	w.WriteS(ch.ClanName)
	w.WriteS("")               // null
	// Clan rank: rank << 4 if rank > 0, else 0xb0
	if ch.ClanRank > 0 {
		w.WriteC(byte(ch.ClanRank << 4))
	} else {
		w.WriteC(0xb0)
	}
	w.WriteC(0xff)             // party HP (0xff = not in party)
	w.WriteC(0x00)             // third speed
	w.WriteC(0x00)             // PC = 0
	w.WriteC(0x00)             // unknown
	w.WriteC(0xff)             // unknown
	w.WriteC(0xff)             // unknown
	w.WriteS("")               // null
	w.WriteC(0x00)             // unknown
	sess.Send(w.Bytes())
}

// loadAndRestoreBuffs loads persisted buffs from DB and restores stats/flags silently.
// NO PACKETS are sent here — call sendRestoredBuffIcons after init packets are done.
// Called after applyEquipStats so stat deltas stack correctly on top of equipment.
func loadAndRestoreBuffs(player *world.PlayerInfo, deps *Deps) {
	if deps.BuffRepo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := deps.BuffRepo.LoadByCharID(ctx, player.CharID)
	if err != nil {
		deps.Log.Error("載入buff失敗", zap.String("name", player.Name), zap.Error(err))
		return
	}
	if len(rows) == 0 {
		return
	}

	for i := range rows {
		row := &rows[i]
		if row.RemainingTime <= 0 {
			continue // expired
		}

		buff := &world.ActiveBuff{
			SkillID:       row.SkillID,
			TicksLeft:     row.RemainingTime * 5, // seconds → ticks (200ms each)
			DeltaAC:       row.DeltaAC,
			DeltaStr:      row.DeltaStr,
			DeltaDex:      row.DeltaDex,
			DeltaCon:      row.DeltaCon,
			DeltaWis:      row.DeltaWis,
			DeltaIntel:    row.DeltaIntel,
			DeltaCha:      row.DeltaCha,
			DeltaMaxHP:    row.DeltaMaxHP,
			DeltaMaxMP:    row.DeltaMaxMP,
			DeltaHitMod:   row.DeltaHitMod,
			DeltaDmgMod:   row.DeltaDmgMod,
			DeltaSP:       row.DeltaSP,
			DeltaMR:       row.DeltaMR,
			DeltaHPR:      row.DeltaHPR,
			DeltaMPR:      row.DeltaMPR,
			DeltaBowHit:   row.DeltaBowHit,
			DeltaBowDmg:   row.DeltaBowDmg,
			DeltaFireRes:  row.DeltaFireRes,
			DeltaWaterRes: row.DeltaWaterRes,
			DeltaWindRes:  row.DeltaWindRes,
			DeltaEarthRes: row.DeltaEarthRes,
			DeltaDodge:    row.DeltaDodge,
			SetMoveSpeed:  row.SetMoveSpeed,
			SetBraveSpeed: row.SetBraveSpeed,
		}

		player.AddBuff(buff)

		// Apply stat deltas to player (silently — no packets)
		player.AC += buff.DeltaAC
		player.Str += buff.DeltaStr
		player.Dex += buff.DeltaDex
		player.Con += buff.DeltaCon
		player.Wis += buff.DeltaWis
		player.Intel += buff.DeltaIntel
		player.Cha += buff.DeltaCha
		player.MaxHP += buff.DeltaMaxHP
		player.MaxMP += buff.DeltaMaxMP
		player.HitMod += buff.DeltaHitMod
		player.DmgMod += buff.DeltaDmgMod
		player.SP += buff.DeltaSP
		player.MR += buff.DeltaMR
		player.HPR += buff.DeltaHPR
		player.MPR += buff.DeltaMPR
		player.BowHitMod += buff.DeltaBowHit
		player.BowDmgMod += buff.DeltaBowDmg
		player.Dodge += buff.DeltaDodge
		player.FireRes += buff.DeltaFireRes
		player.WaterRes += buff.DeltaWaterRes
		player.WindRes += buff.DeltaWindRes
		player.EarthRes += buff.DeltaEarthRes

		// Restore speed flags (state only, no packets)
		if buff.SetMoveSpeed > 0 {
			player.MoveSpeed = buff.SetMoveSpeed
			player.HasteTicks = buff.TicksLeft
		}
		if buff.SetBraveSpeed > 0 {
			player.BraveSpeed = buff.SetBraveSpeed
			player.BraveTicks = buff.TicksLeft
		}

		// Restore wisdom potion tracking fields (SP delta already applied above)
		if row.SkillID == SkillStatusWisdomPotion && buff.DeltaSP > 0 {
			player.WisdomSP = buff.DeltaSP
			player.WisdomTicks = buff.TicksLeft
		}

		// Restore polymorph state (state only, no packets)
		if row.SkillID == SkillShapeChange && row.PolyID > 0 {
			player.TempCharGfx = row.PolyID
			player.PolyID = row.PolyID
			if deps.Polys != nil {
				poly := deps.Polys.GetByID(row.PolyID)
				if poly != nil && player.CurrentWeapon != 0 {
					wpn := player.Equip.Weapon()
					if wpn != nil {
						wpnInfo := deps.Items.Get(wpn.ItemID)
						if wpnInfo != nil && !poly.IsWeaponEquipable(wpnInfo.Type) {
							player.CurrentWeapon = 0
						}
					}
				}
			}
		}
	}

	// Delete persisted buffs after loading (they live in memory now)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if err := deps.BuffRepo.DeleteByCharID(ctx2, player.CharID); err != nil {
		deps.Log.Error("清除已載入buff失敗", zap.String("name", player.Name), zap.Error(err))
	}

	deps.Log.Info(fmt.Sprintf("恢復buff  角色=%s  數量=%d", player.Name, len(rows)))
}

// sendRestoredBuffIcons sends buff icon/speed/poly packets for all active buffs.
// Must be called AFTER the init packet sequence (OwnCharPack etc.) is complete.
func sendRestoredBuffIcons(player *world.PlayerInfo, deps *Deps) {
	if len(player.ActiveBuffs) == 0 {
		return
	}
	sess := player.Session
	for _, buff := range player.ActiveBuffs {
		remainSec := uint16(buff.TicksLeft / 5)
		if remainSec == 0 {
			continue
		}

		// Speed packets
		if buff.SetMoveSpeed > 0 {
			sendSpeedPacket(sess, player.CharID, buff.SetMoveSpeed, remainSec)
		}
		if buff.SetBraveSpeed > 0 {
			sendBravePacket(sess, player.CharID, buff.SetBraveSpeed, remainSec)
		}

		// Polymorph icon
		if buff.SkillID == SkillShapeChange && player.PolyID > 0 {
			sendPolyIcon(sess, remainSec)
		} else {
			// Other buff icons
			sendBuffIcon(player, buff.SkillID, remainSec, deps)
		}
	}
}

// loadAndSendCharConfig loads the saved character config from DB and sends it to the client.
func loadAndSendCharConfig(sess *net.Session, charID int32, deps *Deps) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	data, err := deps.CharRepo.LoadCharConfig(ctx, charID)
	if err != nil {
		deps.Log.Error("載入角色設定失敗", zap.Int32("charID", charID), zap.Error(err))
		return
	}
	if len(data) > 0 {
		sendCharConfig(sess, data)
	}
}

// sendCharResetInfo 發送 S_CharResetInfo（opcode 64, sub-type 0x04）。
// Java: C_LoginToServer line 303，告知客戶端角色屬性配點增加量。
// 格式：nibble-packed，每 byte 高 4 位 + 低 4 位各一項屬性增加值。
func sendCharResetInfo(sess *net.Session, ch *persist.CharacterRow, player *world.PlayerInfo, deps *Deps) {
	if deps.Scripting == nil {
		return
	}
	classData := deps.Scripting.GetCharCreateData(int(ch.ClassType))
	if classData == nil {
		return
	}

	// ch.Str 等是 DB 中的基礎值（不含裝備/buff），減去職業最低值得出配點增量
	clamp := func(v int) byte {
		if v < 0 {
			return 0
		}
		if v > 15 {
			return 15
		}
		return byte(v)
	}
	upStr := clamp(int(ch.Str) - classData.BaseSTR)
	upInt := clamp(int(ch.Intel) - classData.BaseINT)
	upWis := clamp(int(ch.Wis) - classData.BaseWIS)
	upDex := clamp(int(ch.Dex) - classData.BaseDEX)
	upCon := clamp(int(ch.Con) - classData.BaseCON)
	upCha := clamp(int(ch.Cha) - classData.BaseCHA)

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARSYNACK) // opcode 64
	w.WriteC(0x04)                                               // sub-type: 屬性增加資訊
	w.WriteC((upInt << 4) | upStr)
	w.WriteC((upDex << 4) | upWis)
	w.WriteC((upCha << 4) | upCon)
	w.WriteC(0x00) // Java: S_CharResetInfo 尾部填充
	w.WriteH(0x00) // Java: S_CharResetInfo 尾部填充
	sess.Send(w.Bytes())
}

// loadBuddiesFromDB loads the buddy list from the character_buddys table.
func loadBuddiesFromDB(player *world.PlayerInfo, deps *Deps) {
	if deps.BuddyRepo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := deps.BuddyRepo.LoadByCharID(ctx, player.CharID)
	if err != nil {
		deps.Log.Error("載入好友失敗", zap.String("name", player.Name), zap.Error(err))
		return
	}
	for _, row := range rows {
		player.Buddies = append(player.Buddies, world.BuddyEntry{
			CharID: row.BuddyID,
			Name:   row.BuddyName,
		})
	}
}

// loadExcludesFromDB loads the exclude/block list from the character_excludes table.
func loadExcludesFromDB(player *world.PlayerInfo, deps *Deps) {
	if deps.ExcludeRepo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	names, err := deps.ExcludeRepo.LoadByCharID(ctx, player.CharID)
	if err != nil {
		deps.Log.Error("載入黑名單失敗", zap.String("name", player.Name), zap.Error(err))
		return
	}
	player.ExcludeList = names
}
