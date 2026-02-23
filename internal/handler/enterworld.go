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
		BonusStats: ch.BonusStats,
		Food:       40, // Java: initial food = 40 (max 225, increased by eating food items)
		PKCount:    ch.PKCount,
		Inv:        world.NewInventory(),
	}
	deps.World.AddPlayer(player)

	// Set tile collision at player's position
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, player.X, player.Y, true)
	}

	// Load inventory from DB (or give starting gold if empty)
	loadInventoryFromDB(player, deps)

	// Load bookmarks from DB (JSONB column)
	loadBookmarksFromDB(player, deps)

	// Load known spells from DB (JSONB column)
	loadKnownSpellsFromDB(player, deps)

	// Apply all equipment stat bonuses (AC, STR, DEX, etc.) silently — no packets yet.
	// EquipBonuses starts at zero, so this correctly adds all equipment contributions.
	player.AC = 10 // base AC before equipment
	applyEquipStats(player, deps.Items)

	// --- Send initialization packets (order matches Java) ---

	// 1. S_ENTER_WORLD_CHECK (opcode 223) — LoginToGame
	sendLoginGame(sess, ch.ClanID, ch.ID)

	// 2. S_ADD_INVENTORY_BATCH (opcode 5) — inventory list
	sendInvList(sess, player.Inv, deps.Items)

	// 2b. S_EquipmentSlot (opcode 64, TYPE_EQUIPONLOGIN 0x41) — tell client which slots are occupied
	sendEquipSlotList(sess, player)

	// 3. S_STATUS (opcode 8) — OwnCharStatus (use PlayerInfo for live stats)
	sendPlayerStatus(sess, player)

	// 4. S_WORLD (opcode 206) — MapID
	sendMapID(sess, uint16(ch.MapID), false)

	// 5. S_PUT_OBJECT (opcode 87) — OwnCharPack
	sendOwnCharPack(sess, ch, player.CurrentWeapon)

	// 6. S_MAGIC_STATUS (opcode 37) — SPMR (real values from equipment + buffs)
	sendMagicStatus(sess, byte(player.SP), uint16(player.MR))

	// 7. S_WEATHER (opcode 115) — weather
	sendWeather(sess, 0)

	// 8. S_ABILITY_SCORES (opcode 174) — AC + resistances
	sendAbilityScores(sess, player)

	// 9. Send known spells (always send — client needs this to initialize the spell window)
	if deps.Skills != nil {
		var spells []*data.SkillInfo
		for _, sid := range player.KnownSpells {
			if sk := deps.Skills.Get(sid); sk != nil {
				spells = append(spells, sk)
			}
		}
		sendSkillList(sess, spells) // sends all-zero bitmask if no spells known
	}

	// 10. Send saved bookmarks
	SendAllBookmarks(sess, player.Bookmarks)

	// 11. Send saved character config (F5-F12 hotkeys, UI positions)
	loadAndSendCharConfig(sess, ch.ID, deps)

	// 12. Send clan info on login
	if player.ClanID > 0 {
		sendClanName(sess, player.CharID, player.ClanName, player.ClanID, true)
		clan := deps.World.Clans.GetClan(player.ClanID)
		if clan != nil {
			sendPledgeEmblemStatus(sess, int(clan.EmblemStatus))
		}
		sendClanAttention(sess)
	}

	// 10. RaiseAttr dialog if bonus stat points available (level 51+)
	if player.Level >= bonusStatMinLevel {
		available := player.Level - 50 - player.BonusStats
		totalStats := player.Str + player.Dex + player.Con + player.Wis + player.Intel + player.Cha
		if available > 0 && totalStats < maxTotalStats {
			sendRaiseAttrDialog(sess, player.CharID)
		}
	}

	// --- Send nearby players (AOI) ---
	nearby := deps.World.GetNearbyPlayers(ch.X, ch.Y, ch.MapID, sess.ID)
	for _, other := range nearby {
		sendPutObject(sess, other)
		sendPutObject(other.Session, player)
	}

	// --- Send nearby NPCs ---
	nearbyNpcs := deps.World.GetNearbyNpcs(ch.X, ch.Y, ch.MapID)
	for _, npc := range nearbyNpcs {
		sendNpcPack(sess, npc)
	}

	// --- Send nearby ground items ---
	nearbyGnd := deps.World.GetNearbyGroundItems(ch.X, ch.Y, ch.MapID)
	for _, g := range nearbyGnd {
		sendDropItem(sess, g)
	}
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
				invItem.EnchantLvl = byte(row.EnchantLvl)
				invItem.Identified = row.Identified
				invItem.UseType = itemInfo.UseTypeID
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
func sendOwnCharPack(sess *net.Session, ch *persist.CharacterRow, currentWeapon byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(ch.X))
	w.WriteH(uint16(ch.Y))
	w.WriteD(ch.ID)
	w.WriteH(uint16(ch.ClassID))
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
