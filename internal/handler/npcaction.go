package handler

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// HandleNpcAction processes C_HACTION (opcode 125) — player clicks a button in NPC dialog.
// Also handles S_Message_YN (yes/no dialog) responses — client sends objectID=yesNoCount.
// The action string determines what to do: "buy", "sell", "teleportURL", etc.
func HandleNpcAction(sess *net.Session, r *packet.Reader, deps *Deps) {
	objID := r.ReadD()
	action := r.ReadS()

	deps.Log.Debug("C_NpcAction",
		zap.Int32("objID", objID),
		zap.String("action", action),
	)

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	// Clear pending craft state — any new NPC interaction overrides
	player.PendingCraftAction = ""

	// --- Summon ring selection: numeric string response from "summonlist" dialog ---
	// Java: L1ActionPc.java checks cmd.matches("[0-9]+") && isSummonMonster().
	if player.SummonSelectionMode && isNumericString(action) {
		HandleSummonRingSelection(sess, player, action, deps)
		return
	}

	// --- Companion entity control (summon/pet before NPC lookup) ---
	if sum := deps.World.GetSummon(objID); sum != nil {
		if sum.OwnerCharID == player.CharID {
			handleSummonAction(sess, player, sum, strings.ToLower(action), deps)
		}
		return
	}
	if pet := deps.World.GetPet(objID); pet != nil {
		if pet.OwnerCharID == player.CharID {
			handlePetAction(sess, player, pet, strings.ToLower(action), deps)
		}
		return
	}

	npc := deps.World.GetNpc(objID)
	if npc == nil {
		// Not an NPC — check for S_Message_YN (yes/no dialog) response
		if player.PendingYesNoType != 0 {
			lAction := strings.ToLower(action)
			accepted := lAction != "no" && lAction != "n"
			handleYesNoResponse(sess, player, accepted, deps)
		}
		return
	}
	dx := int32(math.Abs(float64(player.X - npc.X)))
	dy := int32(math.Abs(float64(player.Y - npc.Y)))
	if dx > 5 || dy > 5 {
		return
	}

	lowerAction := strings.ToLower(action)

	// Auto-cancel trade when interacting with NPC
	cancelTradeIfActive(player, deps)

	// Paginated teleporter NPC (e.g., NPC 91053): route all actions to paged handler
	if deps.TeleportPages != nil && deps.TeleportPages.IsPageTeleportNpc(npc.NpcID) {
		handlePagedTeleportAction(sess, player, npc, action, deps)
		return
	}

	switch lowerAction {
	case "buy":
		handleShopBuy(sess, npc.NpcID, objID, deps)
	case "sell":
		handleShopSell(sess, npc.NpcID, objID, deps)
	case "buyskill":
		openSpellShop(sess, deps)
	case "teleporturl", "teleporturla", "teleporturlb", "teleporturlc",
		"teleporturld", "teleporturle", "teleporturlf", "teleporturlg",
		"teleporturlh", "teleporturli", "teleporturlj", "teleporturlk":
		handleTeleportURLGeneric(sess, npc.NpcID, objID, action, deps)

	// Warehouse — 個人帳號倉庫
	case "retrieve":
		OpenWarehouse(sess, player, objID, WhTypePersonal, deps)
	case "deposit":
		OpenWarehouseDeposit(sess, player, objID, WhTypePersonal, deps)

	// Warehouse — 角色專屬倉庫（Java: retrieve-char → S_RetrieveChaList type=18）
	case "retrieve-char":
		OpenWarehouse(sess, player, objID, WhTypeCharacter, deps)

	// Warehouse — 精靈倉庫
	case "retrieve-elven":
		OpenWarehouse(sess, player, objID, WhTypeElf, deps)
	case "deposit-elven":
		OpenWarehouseDeposit(sess, player, objID, WhTypeElf, deps)

	// Warehouse — 血盟倉庫（含權限驗證 + 單人鎖定）
	case "retrieve-pledge":
		OpenClanWarehouse(sess, player, objID, deps)
	case "deposit-pledge":
		OpenClanWarehouse(sess, player, objID, deps) // 同 retrieve，客戶端內建 tab 處理
	case "history":
		// 血盟倉庫歷史記錄（Java: S_PledgeWarehouseHistory）
		if player.ClanID > 0 {
			SendClanWarehouseHistory(sess, player.ClanID, deps)
		}

	// EXP recovery / PK redemption (stub)
	case "exp":
		sendHypertext(sess, objID, "expr")
	case "pk":
		sendHypertext(sess, objID, "pkr")

	// ---------- NPC Services (data-driven from npc_services.yaml) ----------

	case "haste":
		handleNpcHaste(sess, player, npc, deps)
	case "0":
		handleNpcActionZero(sess, player, npc, objID, deps)
	case "fullheal":
		handleNpcFullHeal(sess, player, npc, deps)
	case "encw":
		handleNpcWeaponEnchant(sess, player, deps)
	case "enca":
		handleNpcArmorEnchant(sess, player, deps)

	// Close dialog (empty string or explicit close)
	case "":
		// Do nothing — dialog closes

	default:
		// Check teleport destinations (handles "teleport xxx" and other
		// action names like "Strange21", "goto battle ring", "a"/"b"/etc.)
		if deps.Teleports.Get(npc.NpcID, action) != nil {
			handleTeleport(sess, player, npc.NpcID, action, deps)
			return
		}

		// Check if this is a polymorph NPC form (data-driven from npc_services.yaml)
		if polyID := deps.NpcServices.GetPolyForm(lowerAction); polyID > 0 {
			handleNpcPoly(sess, player, polyID, deps)
			return
		}

		// Check if this is a crafting recipe
		if deps.ItemMaking != nil {
			if recipe := deps.ItemMaking.Get(action); recipe != nil {
				handleCraftItem(sess, player, npc, recipe, action, deps)
				return
			}
		}

		deps.Log.Debug("unhandled NPC action",
			zap.String("action", action),
			zap.Int32("npc_id", npc.NpcID),
		)
	}
}

// handleShopBuy — player presses "Buy" — show items NPC sells.
// Sends S_SELL_LIST (opcode 70) = S_ShopSellList in Java (items NPC sells to player).
func handleShopBuy(sess *net.Session, npcID, objID int32, deps *Deps) {
	shop := deps.Shops.Get(npcID)
	if shop == nil || len(shop.SellingItems) == 0 {
		sendNoSell(sess, objID)
		return
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SELL_LIST) // opcode 70
	w.WriteD(objID)
	w.WriteH(uint16(len(shop.SellingItems)))

	for i, si := range shop.SellingItems {
		itemInfo := deps.Items.Get(si.ItemID)
		name := fmt.Sprintf("item#%d", si.ItemID)
		gfxID := int32(0)
		if itemInfo != nil {
			name = itemInfo.Name
			gfxID = itemInfo.InvGfx
		}

		// Append pack count to name if > 1
		if si.PackCount > 1 {
			name = fmt.Sprintf("%s (%d)", name, si.PackCount)
		}

		price := si.SellingPrice

		w.WriteD(int32(i))       // order index
		w.WriteH(uint16(gfxID)) // inventory graphic ID
		w.WriteD(price)          // price
		w.WriteS(name)           // item name

		// Status bytes: show item stats (damage, AC, class restrictions) like Java
		if itemInfo != nil {
			status := buildShopStatusBytes(itemInfo)
			w.WriteC(byte(len(status)))
			w.WriteBytes(status)
		} else {
			w.WriteC(0)
		}
	}

	w.WriteH(0x0007) // currency type: 7 = adena

	sess.Send(w.Bytes())
}

// handleShopSell — player presses "Sell" — show items NPC will buy from player.
// Sends S_SHOP_SELL_LIST (opcode 65) with assessed prices for player's items.
func handleShopSell(sess *net.Session, npcID, objID int32, deps *Deps) {
	shop := deps.Shops.Get(npcID)
	if shop == nil || len(shop.PurchasingItems) == 0 {
		sendNoSell(sess, objID)
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Inv == nil {
		sendNoSell(sess, objID)
		return
	}

	// Build purchasing price lookup
	purchMap := make(map[int32]int32, len(shop.PurchasingItems))
	for _, pi := range shop.PurchasingItems {
		purchMap[pi.ItemID] = pi.PurchasingPrice
	}

	// Find sellable items in player's inventory
	type assessedItem struct {
		objectID int32
		price    int32
	}
	var items []assessedItem
	for _, invItem := range player.Inv.Items {
		price, ok := purchMap[invItem.ItemID]
		if !ok {
			continue
		}
		if invItem.EnchantLvl != 0 || invItem.Bless >= 128 {
			continue // skip enchanted/sealed
		}
		items = append(items, assessedItem{objectID: invItem.ObjectID, price: price})
	}

	if len(items) == 0 {
		sendNoSell(sess, objID)
		return
	}

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SHOP_SELL_LIST) // opcode 65
	w.WriteD(objID)
	w.WriteH(uint16(len(items)))
	for _, it := range items {
		w.WriteD(it.objectID)
		w.WriteD(it.price)
	}
	w.WriteH(0x0007) // currency: adena
	sess.Send(w.Bytes())
}

// handleTeleportURLGeneric shows the NPC's teleport page with data values (prices).
// Handles teleportURL, teleportURLA, teleportURLB, etc.
func handleTeleportURLGeneric(sess *net.Session, npcID, objID int32, action string, deps *Deps) {
	// Look up HTML data (contains htmlID + data values for price display)
	htmlData := deps.TeleportHtml.Get(npcID, action)
	if htmlData != nil {
		sendHypertextWithData(sess, objID, htmlData.HtmlID, htmlData.Data)
		return
	}

	// Fallback: try NpcAction table for teleport_url / teleport_urla
	npcAction := deps.NpcActions.Get(npcID)
	if npcAction == nil {
		return
	}
	lowerAction := strings.ToLower(action)
	switch lowerAction {
	case "teleporturl":
		if npcAction.TeleportURL != "" {
			sendHypertext(sess, objID, npcAction.TeleportURL)
		}
	case "teleporturla":
		if npcAction.TeleportURLA != "" {
			sendHypertext(sess, objID, npcAction.TeleportURLA)
		}
	}
}

// sendHypertext sends S_HYPERTEXT (opcode 39) to show an HTML dialog (no data values).
func sendHypertext(sess *net.Session, objID int32, htmlID string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(objID)
	w.WriteS(htmlID)
	w.WriteH(0x00)
	w.WriteH(0)
	sess.Send(w.Bytes())
}

// sendHypertextWithData sends S_HYPERTEXT with data values injected into the HTML template.
// Data values replace %0, %1, %2... placeholders in the client's built-in HTML.
func sendHypertextWithData(sess *net.Session, objID int32, htmlID string, data []string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(objID)
	w.WriteS(htmlID)
	if len(data) > 0 {
		w.WriteH(0x01) // has data flag
		w.WriteH(uint16(len(data)))
		for _, val := range data {
			w.WriteS(val)
		}
	} else {
		w.WriteH(0x00)
		w.WriteH(0)
	}
	sess.Send(w.Bytes())
}

// sendNoSell sends S_HYPERTEXT with "nosell" HTML to indicate NPC doesn't trade.
func sendNoSell(sess *net.Session, objID int32) {
	sendHypertext(sess, objID, "nosell")
}

// handleTeleport processes a "teleport xxx" action from the NPC dialog.
// Looks up the destination, checks adena cost, and teleports the player.
func handleTeleport(sess *net.Session, player *world.PlayerInfo, npcID int32, action string, deps *Deps) {
	dest := deps.Teleports.Get(npcID, action)
	if dest == nil {
		deps.Log.Debug("teleport destination not found",
			zap.String("action", action),
			zap.Int32("npc_id", npcID),
		)
		return
	}

	// Check adena cost
	if dest.Price > 0 {
		currentGold := player.Inv.GetAdena()
		if currentGold < dest.Price {
			sendServerMessage(sess, 189) // "金幣不足" (Insufficient adena)
			return
		}

		// Deduct adena
		adenaItem := player.Inv.FindByItemID(world.AdenaItemID)
		if adenaItem != nil {
			adenaItem.Count -= dest.Price
			if adenaItem.Count <= 0 {
				player.Inv.RemoveItem(adenaItem.ObjectID, 0)
				sendRemoveInventoryItem(sess, adenaItem.ObjectID)
			} else {
				sendItemCountUpdate(sess, adenaItem)
			}
		}
	}

	teleportPlayer(sess, player, dest.X, dest.Y, dest.MapID, dest.Heading, deps)

	deps.Log.Info(fmt.Sprintf("玩家傳送  角色=%s  動作=%s  x=%d  y=%d  地圖=%d  花費=%d", player.Name, action, dest.X, dest.Y, dest.MapID, dest.Price))
}

// teleportPlayer moves a player to a new location with full AOI updates.
// Used by NPC teleport, death restart, GM commands, etc.
//
// Packet sequence matches Java Teleportation.actionTeleportation() exactly:
//  1. Remove from old location (broadcast S_REMOVE_OBJECT to old nearby)
//  2. Update world position
//  3. S_MapID — client loads new map
//  4. Broadcast S_OtherCharPacks to new nearby (they see us arrive)
//  5. S_OwnCharPack — self character at new position (live player data)
//  6. updateObject equivalent — send nearby players, NPCs, ground items to self
//  7. S_CharVisualUpdate — weapon/poly visual fix (LAST per Java)
// TeleportPlayer 處理完整傳送流程。Exported for system package usage.
func TeleportPlayer(sess *net.Session, player *world.PlayerInfo, x, y int32, mapID, heading int16, deps *Deps) {
	teleportPlayer(sess, player, x, y, mapID, heading, deps)
}

func teleportPlayer(sess *net.Session, player *world.PlayerInfo, x, y int32, mapID, heading int16, deps *Deps) {
	// 傳送時釋放血盟倉庫鎖定（Java: Teleportation.java 行 122-123）
	if player.ClanID != 0 {
		if clan := deps.World.Clans.GetClan(player.ClanID); clan != nil {
			if clan.WarehouseUsingCharID == player.CharID {
				clan.WarehouseUsingCharID = 0
			}
		}
	}

	// Reset move speed timer (teleport resets speed validation)
	player.LastMoveTime = 0

	// Clear old tile (for NPC pathfinding)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, player.X, player.Y, false)
	}

	// 1. 舊位置附近玩家：移除我 + 解鎖我的格子
	oldNearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range oldNearby {
		SendRemoveObject(other.Session, player.CharID)
	}

	// 2. 更新世界狀態位置（Java: moveVisibleObject + setLocation）
	deps.World.UpdatePosition(sess.ID, x, y, mapID, heading)

	// 標記新格子不可通行（NPC 尋路用）
	if deps.MapData != nil {
		deps.MapData.SetImpassable(mapID, x, y, true)
	}

	// 3. S_MapID（即使同地圖也要發——客戶端傳送需要）
	sendMapID(sess, uint16(mapID), false)

	// 重置 Known 集合（傳送 = 完全切換場景）
	if player.Known == nil {
		player.Known = world.NewKnownEntities()
	} else {
		player.Known.Reset()
	}

	// 4. 目的地附近玩家：顯示我 + 封鎖我的格子 + 填入 Known
	newNearby := deps.World.GetNearbyPlayers(x, y, mapID, sess.ID)
	for _, other := range newNearby {
		SendPutObject(other.Session, player)
	}

	// 5. S_OwnCharPack
	sendOwnCharPackPlayer(sess, player)

	// 6. 發送附近實體給自己 + 封鎖格子 + 填入 Known
	for _, other := range newNearby {
		SendPutObject(sess, other)
		player.Known.Players[other.CharID] = world.KnownPos{X: other.X, Y: other.Y}
	}

	nearbyNpcs := deps.World.GetNearbyNpcs(x, y, mapID)
	for _, npc := range nearbyNpcs {
		SendNpcPack(sess, npc)
		player.Known.Npcs[npc.ID] = world.KnownPos{X: npc.X, Y: npc.Y}
	}

	nearbyGnd := deps.World.GetNearbyGroundItems(x, y, mapID)
	for _, g := range nearbyGnd {
		SendDropItem(sess, g)
		player.Known.GroundItems[g.ID] = world.KnownPos{X: g.X, Y: g.Y}
	}

	nearbyDoors := deps.World.GetNearbyDoors(x, y, mapID)
	for _, d := range nearbyDoors {
		SendDoorPerceive(sess, d)
		player.Known.Doors[d.ID] = world.KnownPos{X: d.X, Y: d.Y}
	}

	nearbySum := deps.World.GetNearbySummons(x, y, mapID)
	for _, sum := range nearbySum {
		isOwner := sum.OwnerCharID == player.CharID
		masterName := ""
		if m := deps.World.GetByCharID(sum.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendSummonPack(sess, sum, isOwner, masterName)
		player.Known.Summons[sum.ID] = world.KnownPos{X: sum.X, Y: sum.Y}
	}
	nearbyDolls := deps.World.GetNearbyDolls(x, y, mapID)
	for _, doll := range nearbyDolls {
		masterName := ""
		if m := deps.World.GetByCharID(doll.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendDollPack(sess, doll, masterName)
		player.Known.Dolls[doll.ID] = world.KnownPos{X: doll.X, Y: doll.Y}
	}
	nearbyFollowers := deps.World.GetNearbyFollowers(x, y, mapID)
	for _, f := range nearbyFollowers {
		SendFollowerPack(sess, f)
		player.Known.Followers[f.ID] = world.KnownPos{X: f.X, Y: f.Y}
	}
	nearbyPets := deps.World.GetNearbyPets(x, y, mapID)
	for _, pet := range nearbyPets {
		isOwner := pet.OwnerCharID == player.CharID
		masterName := ""
		if m := deps.World.GetByCharID(pet.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendPetPack(sess, pet, isOwner, masterName)
		player.Known.Pets[pet.ID] = world.KnownPos{X: pet.X, Y: pet.Y}
	}

	// Release client teleport lock (Java: S_Paralysis always sent in finally block).
	sendTeleportUnlock(sess)
}

// handleYesNoResponse processes S_Message_YN dialog responses.
// Routes to trade or party accept/decline based on PendingYesNoType.
func handleYesNoResponse(sess *net.Session, player *world.PlayerInfo, accepted bool, deps *Deps) {
	msgType := player.PendingYesNoType
	data := player.PendingYesNoData
	player.PendingYesNoType = 0
	player.PendingYesNoData = 0

	switch msgType {
	case 252: // Trade confirmation
		handleTradeYesNo(sess, player, data, accepted, deps)
	}
}

// ========================================================================
//  NPC Service Handlers
// ========================================================================

// handleNpcHaste — Haste buffer NPC. Parameters from npc_services.yaml.
func handleNpcHaste(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, deps *Deps) {
	h := deps.NpcServices.Haste()
	if npc.NpcID != h.NpcID {
		return
	}
	applyHaste(sess, player, h.DurationSec, h.Gfx, deps)
	sendServerMessage(sess, h.MsgID)
}

// handleNpcActionZero — routes the "0" action based on NPC ID.
// Healer and cancellation NPC parameters from npc_services.yaml.
func handleNpcActionZero(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, objID int32, deps *Deps) {
	// Check if this NPC is a cancellation NPC
	cancel := deps.NpcServices.Cancel()
	if npc.NpcID == cancel.NpcID {
		if player.Level <= cancel.MaxLevel {
			cancelAllBuffs(player, deps)
			broadcastEffect(sess, player, cancel.Gfx, deps)
		}
		return
	}

	// Check if this NPC is a healer
	if healer := deps.NpcServices.GetHealer(npc.NpcID); healer != nil {
		execHeal(sess, player, healer, deps)
		return
	}

	// Unknown "0" action for this NPC — try showing dialog
	npcAction := deps.NpcActions.Get(npc.NpcID)
	if npcAction != nil && npcAction.NormalAction != "" {
		sendHypertext(sess, objID, npcAction.NormalAction)
	}
}

// handleNpcFullHeal — Full heal NPC. Parameters from npc_services.yaml.
func handleNpcFullHeal(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, deps *Deps) {
	if healer := deps.NpcServices.GetHealer(npc.NpcID); healer != nil {
		execHeal(sess, player, healer, deps)
		return
	}
	// Generic full heal for other healer NPCs not in YAML
	player.HP = player.MaxHP
	player.MP = player.MaxMP
	sendHpUpdate(sess, player)
	sendMpUpdate(sess, player)
	sendServerMessage(sess, 77) // "你覺得舒服多了"
	broadcastEffect(sess, player, 830, deps)
	UpdatePartyMiniHP(player, deps)
}

// execHeal executes a heal service based on YAML-defined healer parameters.
func execHeal(sess *net.Session, player *world.PlayerInfo, h *data.HealerDef, deps *Deps) {
	// Check cost
	if h.Cost > 0 {
		if !consumeAdena(player, h.Cost) {
			sendServerMessageArgs(sess, 337, "$4") // "金幣不足"
			return
		}
		sendAdenaUpdate(sess, player)
	}

	// Apply heal
	switch h.HealType {
	case "random":
		healRange := h.HealMax - h.HealMin + 1
		healAmt := int16(rand.Intn(healRange) + h.HealMin)
		if player.HP < player.MaxHP {
			player.HP += healAmt
			if player.HP > player.MaxHP {
				player.HP = player.MaxHP
			}
		}
		sendHpUpdate(sess, player)
	case "full":
		if h.Target == "hp_mp" || h.Target == "hp" {
			player.HP = player.MaxHP
			sendHpUpdate(sess, player)
		}
		if h.Target == "hp_mp" || h.Target == "mp" {
			player.MP = player.MaxMP
			sendMpUpdate(sess, player)
		}
		UpdatePartyMiniHP(player, deps)
	}

	sendServerMessage(sess, h.MsgID)
	broadcastEffect(sess, player, h.Gfx, deps)
}

// handleNpcWeaponEnchant — Weapon enchanter NPC. Parameters from npc_services.yaml.
func handleNpcWeaponEnchant(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	we := deps.NpcServices.WeaponEnchant()
	weapon := player.Equip.Weapon()
	if weapon == nil {
		sendServerMessage(sess, 79) // "沒有任何事情發生"
		return
	}

	// If already has enchant, cancel old bonus first
	if weapon.DmgByMagic > 0 && weapon.DmgMagicExpiry > 0 {
		weapon.DmgByMagic = 0
		weapon.DmgMagicExpiry = 0
	}

	weapon.DmgByMagic = we.DmgBonus
	weapon.DmgMagicExpiry = we.DurationSec * 5 // seconds → ticks

	recalcEquipStats(sess, player, deps)
	broadcastEffect(sess, player, we.Gfx, deps)
	sendServerMessageArgs(sess, 161, weapon.Name, "$245", "$247")
}

// handleNpcArmorEnchant — Armor enchanter NPC. Parameters from npc_services.yaml.
func handleNpcArmorEnchant(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	ae := deps.NpcServices.ArmorEnchant()
	armor := player.Equip.Get(world.SlotArmor)
	if armor == nil {
		sendServerMessage(sess, 79) // "沒有任何事情發生"
		return
	}

	// If already has enchant, cancel old bonus first
	if armor.AcByMagic > 0 && armor.AcMagicExpiry > 0 {
		armor.AcByMagic = 0
		armor.AcMagicExpiry = 0
	}

	armor.AcByMagic = ae.AcBonus
	armor.AcMagicExpiry = ae.DurationSec * 5 // seconds → ticks

	recalcEquipStats(sess, player, deps)
	broadcastEffect(sess, player, ae.Gfx, deps)
	sendServerMessageArgs(sess, 161, armor.Name, "$245", "$247")
}

// handleNpcPoly — Polymorph NPC. Cost/duration from npc_services.yaml.
func handleNpcPoly(sess *net.Session, player *world.PlayerInfo, polyID int32, deps *Deps) {
	poly := deps.NpcServices.Polymorph()
	if !consumeAdena(player, poly.Cost) {
		sendServerMessageArgs(sess, 337, "$4") // "金幣不足"
		return
	}
	sendAdenaUpdate(sess, player)
	DoPoly(player, polyID, poly.DurationSec, data.PolyCauseNPC, deps)
}

// consumeAdena deducts adena from player inventory. Returns false if insufficient.
func consumeAdena(player *world.PlayerInfo, amount int32) bool {
	adena := player.Inv.FindByItemID(world.AdenaItemID)
	if adena == nil || adena.Count < amount {
		return false
	}
	adena.Count -= amount
	return true
}

// sendAdenaUpdate sends the updated adena count to the client after consumption.
func sendAdenaUpdate(sess *net.Session, player *world.PlayerInfo) {
	adena := player.Inv.FindByItemID(world.AdenaItemID)
	if adena != nil {
		sendItemCountUpdate(sess, adena)
	} else {
		// Adena fully consumed — should have been removed, but just in case
	}
	sendWeightUpdate(sess, player)
}

// ========================================================================
//  Crafting System (NPC Item Making)
// ========================================================================

// handleCraftItem is the main crafting entry point when a recipe is found.
// Checks NPC restriction, counts material sets, shows batch dialog or executes.
// Java: L1NpcMakeItemAction.execute()
func handleCraftItem(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, recipe *data.CraftRecipe, action string, deps *Deps) {
	// NPC restriction: recipe.NpcID == 0 means any NPC
	if recipe.NpcID != 0 && recipe.NpcID != npc.NpcID {
		return
	}

	// Count available material sets
	sets := countMaterialSets(player.Inv, recipe.Materials)
	if sets <= 0 {
		// Report first missing material (Java: msg 337 with item name + shortage)
		for _, mat := range recipe.Materials {
			have := countUnequippedByID(player.Inv, mat.ItemID)
			if have < mat.Amount {
				shortage := mat.Amount - have
				itemInfo := deps.Items.Get(mat.ItemID)
				name := fmt.Sprintf("item#%d", mat.ItemID)
				if itemInfo != nil {
					name = itemInfo.Name
				}
				sendServerMessageArgs(sess, 337, name, fmt.Sprintf("%d", shortage))
				return
			}
		}
		return
	}

	// If multiple sets available and recipe supports batch input, show spinner dialog
	if sets > 1 && recipe.AmountInputable {
		sendInputAmount(sess, npc.ID, sets, action)
		player.PendingCraftAction = action
		return
	}

	// Single craft
	executeCraft(sess, player, npc, recipe, 1, deps)
}

// countMaterialSets returns how many complete material sets the player can provide.
// Only counts non-equipped items. Returns 0 if any material is insufficient.
func countMaterialSets(inv *world.Inventory, materials []data.CraftMaterial) int32 {
	if len(materials) == 0 {
		return 0
	}
	var minSets int32 = math.MaxInt32
	for _, mat := range materials {
		have := countUnequippedByID(inv, mat.ItemID)
		if mat.Amount <= 0 {
			continue
		}
		sets := have / mat.Amount
		if sets < minSets {
			minSets = sets
		}
	}
	if minSets == math.MaxInt32 {
		return 0
	}
	return minSets
}

// countUnequippedByID counts the total quantity of an item that is NOT equipped.
func countUnequippedByID(inv *world.Inventory, itemID int32) int32 {
	var total int32
	for _, it := range inv.Items {
		if it.ItemID == itemID && !it.Equipped {
			total += it.Count
		}
	}
	return total
}

// findUnequippedByID returns the first non-equipped item matching the given template ID.
func findUnequippedByID(inv *world.Inventory, itemID int32) *world.InvItem {
	for _, it := range inv.Items {
		if it.ItemID == itemID && !it.Equipped {
			return it
		}
	}
	return nil
}

// sendInputAmount sends S_OPCODE_INPUTAMOUNT (136) — S_HowManyMake crafting batch dialog.
// Java: S_HowManyMake(npcObjectId, maxAmount, actionName)
// The client concatenates the two writeS strings with a space separator when sending back C_Amount.
func sendInputAmount(sess *net.Session, npcObjID int32, maxSets int32, action string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_INPUTAMOUNT)
	w.WriteD(npcObjID)
	w.WriteD(0)       // unknown
	w.WriteD(0)       // spinner initial value
	w.WriteD(0)       // spinner minimum
	w.WriteD(maxSets) // spinner maximum
	w.WriteH(0)       // unknown

	// Split action: "request adena2" → prefix="request", suffix="adena2"
	// Client concatenates: "request" + " " + "adena2" = "request adena2" (matches YAML key)
	suffix := action
	if strings.HasPrefix(action, "request ") {
		suffix = action[len("request "):]
	}
	w.WriteS("request")
	w.WriteS(suffix)

	sess.Send(w.Bytes())
}

// executeCraft performs the actual crafting: verify materials, consume them, produce output items.
// Java: L1NpcMakeItemAction.makeItems()
func executeCraft(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo, recipe *data.CraftRecipe, amount int32, deps *Deps) {
	if amount <= 0 {
		return
	}

	// 1. Material check — verify all materials sufficient (non-equipped only)
	for _, mat := range recipe.Materials {
		have := countUnequippedByID(player.Inv, mat.ItemID)
		need := mat.Amount * amount
		if have < need {
			shortage := need - have
			itemInfo := deps.Items.Get(mat.ItemID)
			name := fmt.Sprintf("item#%d", mat.ItemID)
			if itemInfo != nil {
				name = itemInfo.Name
			}
			// msg 337: "%0が%1個不足しています" (Insufficient: need %1 more %0)
			sendServerMessageArgs(sess, 337, name, fmt.Sprintf("%d", shortage))
			return
		}
	}

	// 2. Inventory space check (max 180 slots)
	newSlots := 0
	for _, out := range recipe.Items {
		outInfo := deps.Items.Get(out.ItemID)
		if outInfo != nil && outInfo.Stackable {
			existing := player.Inv.FindByItemID(out.ItemID)
			if existing == nil {
				newSlots++ // new stack
			}
			// else: existing stack, no new slot needed
		} else {
			// Non-stackable: each item takes a slot
			newSlots += int(out.Amount) * int(amount)
		}
	}
	if player.Inv.Size()+newSlots > world.MaxInventorySize {
		// msg 263: "持有物品過多" (Carrying too many items)
		sendServerMessage(sess, 263)
		return
	}

	// 3. Weight check
	var addWeight int32
	for _, out := range recipe.Items {
		outInfo := deps.Items.Get(out.ItemID)
		if outInfo != nil {
			addWeight += outInfo.Weight * out.Amount * amount
		}
	}
	maxW := world.MaxWeight(player.Str, player.Con)
	if player.Inv.IsOverWeight(addWeight, maxW) {
		// msg 82: "一つのキャラクターが持てるアイテム重量を超過しています" (Overweight)
		sendServerMessage(sess, 82)
		return
	}

	// 4. Consume materials
	for _, mat := range recipe.Materials {
		remaining := mat.Amount * amount
		for remaining > 0 {
			slot := findUnequippedByID(player.Inv, mat.ItemID)
			if slot == nil {
				break // should not happen — checked above
			}
			take := remaining
			if take > slot.Count {
				take = slot.Count
			}
			removed := player.Inv.RemoveItem(slot.ObjectID, take)
			if removed {
				sendRemoveInventoryItem(sess, slot.ObjectID)
			} else {
				sendItemCountUpdate(sess, slot)
			}
			remaining -= take
		}
	}

	// 5. Produce output items
	npcName := ""
	if npc != nil {
		npcInfo := deps.Npcs.Get(npc.NpcID)
		if npcInfo != nil {
			npcName = npcInfo.Name
		}
	}

	for _, out := range recipe.Items {
		outInfo := deps.Items.Get(out.ItemID)
		if outInfo == nil {
			continue
		}
		totalCount := out.Amount * amount

		if outInfo.Stackable {
			item := player.Inv.AddItem(out.ItemID, totalCount, outInfo.Name,
				outInfo.InvGfx, outInfo.Weight, true, byte(outInfo.Bless))
			item.UseType = data.UseTypeToID(outInfo.UseType)
			sendAddItem(sess, item, outInfo)
		} else {
			for i := int32(0); i < totalCount; i++ {
				item := player.Inv.AddItem(out.ItemID, 1, outInfo.Name,
					outInfo.InvGfx, outInfo.Weight, false, byte(outInfo.Bless))
				item.UseType = data.UseTypeToID(outInfo.UseType)
				sendAddItem(sess, item, outInfo)
			}
		}

		// msg 143: "%0が%1をくれました" ([NPC] gave you [item])
		if npcName != "" {
			sendServerMessageArgs(sess, 143, npcName, outInfo.Name)
		}
	}

	sendWeightUpdate(sess, player)

	deps.Log.Info(fmt.Sprintf("製作完成  角色=%s  配方=%s  數量=%d",
		player.Name, recipe.Action, amount))
}

// HandleCraftAmount processes C_Amount (opcode 11) when a crafting batch response is pending.
// Called from HandleHypertextInputResult when player.PendingCraftAction is set.
// Java: C_Amount.java — [D npcObjID][D amount][C unknown][S actionStr]
func HandleCraftAmount(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, deps *Deps) {
	action := player.PendingCraftAction
	player.PendingCraftAction = "" // clear pending state

	npcObjID := r.ReadD()
	amount := r.ReadD()
	_ = r.ReadC() // unknown delimiter
	actionStr := r.ReadS()

	if amount <= 0 {
		return
	}

	npc := deps.World.GetNpc(npcObjID)
	if npc == nil {
		return
	}

	// Distance check
	dx := int32(math.Abs(float64(player.X - npc.X)))
	dy := int32(math.Abs(float64(player.Y - npc.Y)))
	if dx > 5 || dy > 5 {
		return
	}

	// Look up recipe — prefer the action string from client, fallback to stored action
	recipe := deps.ItemMaking.Get(actionStr)
	if recipe == nil {
		recipe = deps.ItemMaking.Get(action)
	}
	if recipe == nil {
		return
	}

	executeCraft(sess, player, npc, recipe, amount, deps)
}

// TickItemMagicEnchants decrements item-level magic enchant timers for a player.
// Called once per game tick for each online player.
// When an enchant expires, the stat is reverted and equipment recalculated.
func TickItemMagicEnchants(player *world.PlayerInfo, deps *Deps) {
	changed := false

	// Check weapon enchant
	weapon := player.Equip.Weapon()
	if weapon != nil && weapon.DmgMagicExpiry > 0 {
		weapon.DmgMagicExpiry--
		if weapon.DmgMagicExpiry <= 0 {
			weapon.DmgByMagic = 0
			weapon.DmgMagicExpiry = 0
			changed = true
		}
	}

	// Check armor enchant
	armor := player.Equip.Get(world.SlotArmor)
	if armor != nil && armor.AcMagicExpiry > 0 {
		armor.AcMagicExpiry--
		if armor.AcMagicExpiry <= 0 {
			armor.AcByMagic = 0
			armor.AcMagicExpiry = 0
			changed = true
		}
	}

	if changed && player.Session != nil {
		recalcEquipStats(player.Session, player, deps)
	}
}

// ========================================================================
//  Summon control — Java: L1ActionSummon.action()
// ========================================================================

// handleSummonAction processes summon control commands from the moncom dialog.
// Action strings: "aggressive", "defensive", "stay", "extend", "alert", "dismiss".
func handleSummonAction(sess *net.Session, player *world.PlayerInfo, sum *world.SummonInfo, action string, deps *Deps) {
	switch action {
	case "aggressive":
		sum.Status = world.SummonAggressive
	case "defensive":
		sum.Status = world.SummonDefensive
		sum.AggroTarget = 0
		sum.AggroPlayerID = 0
	case "stay":
		sum.Status = world.SummonRest
		sum.AggroTarget = 0
		sum.AggroPlayerID = 0
	case "extend":
		sum.Status = world.SummonExtend
		sum.AggroTarget = 0
		sum.AggroPlayerID = 0
	case "alert":
		sum.Status = world.SummonAlert
		sum.HomeX = sum.X
		sum.HomeY = sum.Y
		sum.AggroTarget = 0
		sum.AggroPlayerID = 0
	case "dismiss":
		DismissSummon(sum, player, deps)
		return
	}
	// Refresh menu with updated status
	sendSummonMenu(sess, sum)
}

// isNumericString returns true if s is a non-empty string of ASCII digits.
// Java: cmd.matches("[0-9]+") — used to detect summon selection responses.
func isNumericString(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
