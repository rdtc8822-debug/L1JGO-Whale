package system

// pet_mgr.go — 寵物生命週期系統（召喚/收回/解放/死亡/經驗/指令）。
// 業務邏輯由 handler/pet.go 抽出，handler 只負責解封包 + 委派。

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
)

// PetSystem 實作 handler.PetLifecycleManager。
type PetSystem struct {
	deps *handler.Deps
}

// NewPetSystem 建立 PetSystem。
func NewPetSystem(deps *handler.Deps) *PetSystem {
	return &PetSystem{deps: deps}
}

// UsePetCollar 使用寵物項圈召喚寵物，或收回已召喚的寵物（切換行為）。
func (s *PetSystem) UsePetCollar(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem) {
	ws := s.deps.World

	// 檢查此項圈是否已有寵物召喚
	if ws.GetPetByItemObjID(invItem.ObjectID) != nil {
		pet := ws.GetPetByItemObjID(invItem.ObjectID)
		if pet != nil && pet.OwnerCharID == player.CharID {
			s.CollectPet(pet, player)
		}
		return
	}

	// 檢查地圖是否允許召喚寵物
	if s.deps.MapData != nil {
		md := s.deps.MapData.GetInfo(player.MapID)
		if md != nil && !md.RecallPets {
			handler.SendServerMessage(sess, 353) // "此處無法召喚。"
			return
		}
	}

	// CHA 檢查
	usedCost := s.CalcUsedPetCost(player.CharID)
	availCHA := int(player.Cha) - usedCost
	if availCHA < 6 {
		handler.SendServerMessage(sess, 319) // "你的魅力值不夠。"
		return
	}

	// 從 DB 載入寵物
	if s.deps.PetRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	petRow, err := s.deps.PetRepo.LoadByItemObjID(ctx, invItem.ObjectID)
	if err != nil || petRow == nil {
		return
	}

	// 查詢 NPC 模板
	tmpl := s.deps.Npcs.Get(petRow.NpcID)
	if tmpl == nil {
		return
	}

	// 查詢寵物類型（升級資訊）
	petType := s.deps.PetTypes.Get(petRow.NpcID)

	// 主人附近隨機生成位置（±2 格）
	spawnX := player.X + int32(world.RandInt(5)) - 2
	spawnY := player.Y + int32(world.RandInt(5)) - 2

	// 計算 MaxHP/MaxMP：使用 DB 值，回退到模板值
	maxHP := petRow.MaxHP
	if maxHP <= 0 {
		maxHP = petRow.HP
	}
	if maxHP <= 0 {
		maxHP = tmpl.HP
	}
	maxMP := petRow.MaxMP
	if maxMP <= 0 {
		maxMP = petRow.MP
	}
	if maxMP <= 0 {
		maxMP = tmpl.MP
	}

	// 死亡恢復：重新召喚的寵物總是滿血（Java 行為）
	hp := petRow.HP
	mp := petRow.MP
	if hp <= 0 {
		hp = maxHP
	}
	if mp <= 0 {
		mp = maxMP
	}

	pet := &world.PetInfo{
		ID:          world.NextNpcID(),
		OwnerCharID: player.CharID,
		ItemObjID:   invItem.ObjectID,
		NpcID:       petRow.NpcID,
		Name:        petRow.Name,
		Level:       petRow.Level,
		HP:          hp,
		MaxHP:       maxHP,
		MP:          mp,
		MaxMP:       maxMP,
		Exp:         petRow.Exp,
		Lawful:      petRow.Lawful,
		GfxID:       tmpl.GfxID,
		NameID:      tmpl.NameID,
		MoveSpeed:   tmpl.PassiveSpeed,
		X:           spawnX,
		Y:           spawnY,
		MapID:       player.MapID,
		Heading:     player.Heading,
		Status:      world.PetStatusRest,
		AC:          tmpl.AC,
		STR:         tmpl.STR,
		DEX:         tmpl.DEX,
		MR:          tmpl.MR,
		AtkDmg:      tmpl.HP / 4,
		AtkSpeed:    tmpl.AtkSpeed,
		Ranged:      tmpl.Ranged,
	}

	// 註冊到世界
	ws.AddPet(pet)

	// 廣播外觀給附近玩家
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		handler.SendPetPack(viewer.Session, pet, isOwner, player.Name)
	}

	// 發送寵物控制面板給主人
	handler.SendPetCtrlMenu(sess, pet, true)
	handler.SendPetHpMeter(sess, pet.ID, pet.HP, pet.MaxHP)

	// 廣播寵物等級訊息
	if petType != nil {
		msgID := petType.LevelUpMsgID(int(pet.Level))
		if msgID > 0 {
			broadcastNpcChat(ws, pet.ID, pet.X, pet.Y, pet.MapID, fmt.Sprintf("$%d", msgID))
		}
	}
}

// HandlePetAction 處理寵物控制指令。
func (s *PetSystem) HandlePetAction(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, action string) {
	switch action {
	case "aggressive":
		s.changePetStatus(player, pet, world.PetStatusAggressive)
	case "defensive":
		s.changePetStatus(player, pet, world.PetStatusDefensive)
	case "stay":
		s.changePetStatus(player, pet, world.PetStatusRest)
		pet.AggroTarget = 0
		pet.AggroPlayerID = 0
	case "extend":
		s.changePetStatus(player, pet, world.PetStatusExtend)
	case "alert":
		if s.changePetStatus(player, pet, world.PetStatusAlert) {
			pet.HomeX = pet.X
			pet.HomeY = pet.Y
		}
	case "dismiss":
		s.DismissPet(pet, player)
	case "attackchr":
		handler.SendSelectTarget(sess, pet.ID)
	case "getitem":
		s.collectPetItems(sess, player, pet)
	case "changename":
		handler.SendYesNoDialog(sess, 325, pet.Name)
	}
}

// HandlePetNameChange 處理寵物改名。
func (s *PetSystem) HandlePetNameChange(sess *net.Session, player *world.PlayerInfo, petID int32, newName string) {
	pet := s.deps.World.GetPet(petID)
	if pet == nil || pet.OwnerCharID != player.CharID {
		return
	}
	newName = strings.TrimSpace(newName)
	if newName == "" || len(newName) > 16 {
		return
	}
	pet.Name = newName
	pet.Dirty = true

	// 重新廣播更新後的外觀
	nearby := s.deps.World.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		handler.SendPetPack(viewer.Session, pet, isOwner, player.Name)
	}
}

// changePetStatus 變更寵物 AI 狀態（含等級檢查）。
func (s *PetSystem) changePetStatus(player *world.PlayerInfo, pet *world.PetInfo, newStatus world.PetStatus) bool {
	if player.Level < pet.Level {
		petType := s.deps.PetTypes.Get(pet.NpcID)
		if petType != nil && petType.DefyMsgID > 0 {
			broadcastNpcChat(s.deps.World, pet.ID, pet.X, pet.Y, pet.MapID,
				fmt.Sprintf("$%d", petType.DefyMsgID))
		}
		return false
	}
	pet.Status = newStatus
	return true
}

// DismissPet 解放寵物（轉為野生 NPC、刪除 DB、移除項圈）。
func (s *PetSystem) DismissPet(pet *world.PetInfo, player *world.PlayerInfo) {
	ws := s.deps.World

	// 從世界移除
	ws.RemovePet(pet.ID)

	// 廣播移除
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, pet.ID)
	}

	// 關閉寵物控制面板
	handler.SendPetCtrlMenu(player.Session, pet, false)

	// 在寵物位置生成野生 NPC
	tmpl := s.deps.Npcs.Get(pet.NpcID)
	if tmpl != nil {
		npc := &world.NpcInfo{
			ID:         world.NextNpcID(),
			NpcID:      pet.NpcID,
			Impl:       tmpl.Impl,
			GfxID:      tmpl.GfxID,
			Name:       tmpl.Name,
			NameID:     tmpl.NameID,
			Level:      pet.Level,
			HP:         pet.HP,
			MaxHP:      pet.MaxHP,
			MP:         pet.MP,
			MaxMP:      pet.MaxMP,
			AC:         tmpl.AC,
			STR:        tmpl.STR,
			DEX:        tmpl.DEX,
			Exp:        tmpl.Exp,
			Lawful:     tmpl.Lawful,
			Size:       tmpl.Size,
			MR:         tmpl.MR,
			PoisonAtk:  tmpl.PoisonAtk,
			X:          pet.X,
			Y:          pet.Y,
			MapID:      pet.MapID,
			SpawnX:     pet.X,
			SpawnY:     pet.Y,
			SpawnMapID: pet.MapID,
		}
		ws.AddNpc(npc)
		for _, viewer := range nearby {
			handler.SendNpcPack(viewer.Session, npc)
		}
	}

	// 從 DB 刪除寵物
	if s.deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.deps.PetRepo.Delete(ctx, pet.ItemObjID)
		cancel()
	}

	// 從背包移除項圈
	collarItem := player.Inv.FindByObjectID(pet.ItemObjID)
	if collarItem != nil {
		player.Inv.RemoveItem(pet.ItemObjID, 0)
		handler.SendRemoveInventoryItem(player.Session, pet.ItemObjID)
	}
}

// CollectPet 收回寵物至項圈（儲存 DB、從世界移除）。
func (s *PetSystem) CollectPet(pet *world.PetInfo, player *world.PlayerInfo) {
	ws := s.deps.World

	// 儲存到 DB
	if s.deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.deps.PetRepo.Save(ctx, &persist.PetRow{
			ItemObjID: pet.ItemObjID,
			ObjID:     pet.ID,
			NpcID:     pet.NpcID,
			Name:      pet.Name,
			Level:     pet.Level,
			HP:        pet.HP,
			MaxHP:     pet.MaxHP,
			MP:        pet.MP,
			MaxMP:     pet.MaxMP,
			Exp:       pet.Exp,
			Lawful:    pet.Lawful,
		})
		cancel()
	}

	// 從世界移除
	ws.RemovePet(pet.ID)

	// 廣播移除
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, pet.ID)
	}

	// 關閉寵物控制面板
	handler.SendPetCtrlMenu(player.Session, pet, false)
}

// PetDie 處理寵物死亡：-5% 經驗懲罰、死亡動畫、釋放格子。
func (s *PetSystem) PetDie(pet *world.PetInfo) {
	ws := s.deps.World

	pet.Dead = true
	pet.HP = 0
	pet.AggroTarget = 0
	pet.AggroPlayerID = 0

	// -5% 經驗懲罰
	penalty := pet.Exp / 20
	pet.Exp -= penalty
	if pet.Exp < 0 {
		pet.Exp = 0
	}
	pet.Dirty = true

	// 釋放格子（死亡寵物不阻擋移動）
	ws.PetDied(pet)

	// 廣播死亡動畫
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, pet.ID, 8) // ACTION_Die = 8
	}
}

// AddPetExp 增加寵物經驗值並處理升級。
func (s *PetSystem) AddPetExp(pet *world.PetInfo, expGain int32) {
	if expGain <= 0 || s.deps.Scripting == nil {
		return
	}
	pet.Exp += expGain

	maxLevel := int16(50) // Java: 寵物最高 50 級
	for {
		nextLevelExp := int32(s.deps.Scripting.ExpForLevel(int(pet.Level) + 1))
		if pet.Exp < nextLevelExp || pet.Level >= maxLevel {
			break
		}
		pet.Level++
		petType := s.deps.PetTypes.Get(pet.NpcID)
		if petType != nil {
			hpGain := petType.HPUpMin + world.RandInt(petType.HPUpMax-petType.HPUpMin+1)
			mpGain := petType.MPUpMin + world.RandInt(petType.MPUpMax-petType.MPUpMin+1)
			pet.MaxHP += int32(hpGain)
			pet.MaxMP += int32(mpGain)
		}
		pet.HP = pet.MaxHP
		pet.MP = pet.MaxMP
	}
	pet.Dirty = true
}

// PetExpPercent 計算寵物經驗百分比（0-100）。
func (s *PetSystem) PetExpPercent(pet *world.PetInfo) int {
	if s.deps.Scripting == nil {
		return 0
	}
	expForCurrent := s.deps.Scripting.ExpForLevel(int(pet.Level))
	expForNext := s.deps.Scripting.ExpForLevel(int(pet.Level) + 1)
	if expForNext <= expForCurrent {
		return 100
	}
	pct := 100 * (int(pet.Exp) - expForCurrent) / (expForNext - expForCurrent)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// CalcUsedPetCost 計算玩家已使用的寵物/召喚獸 CHA 消耗。
func (s *PetSystem) CalcUsedPetCost(charID int32) int {
	ws := s.deps.World
	cost := 0
	// 寵物：每隻消耗 6 CHA
	for _, pet := range ws.GetPetsByOwner(charID) {
		_ = pet
		cost += 6
	}
	// 召喚獸：使用實際 PetCost（指環召喚獸有不同消耗 8-50）
	for _, sum := range ws.GetSummonsByOwner(charID) {
		c := sum.PetCost
		if c <= 0 {
			c = 6
		}
		cost += c
	}
	return cost
}

// collectPetItems 收回寵物身上所有物品到主人背包。
func (s *PetSystem) collectPetItems(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo) {
	if len(pet.Items) == 0 {
		return
	}
	for _, petItem := range pet.Items {
		// 先脫下裝備
		if petItem.Equipped {
			if petItem.IsWeapon {
				s.unequipPetWeapon(pet)
			} else {
				s.unequipPetArmor(pet)
			}
		}
		// 查詢物品模板取得重量/名稱/圖示
		name := petItem.Name
		gfx := petItem.GfxID
		var weight int32
		if s.deps.Items != nil {
			tmpl := s.deps.Items.Get(petItem.ItemID)
			if tmpl != nil {
				name = tmpl.Name
				gfx = tmpl.InvGfx
				weight = tmpl.Weight
			}
		}
		invItem := player.Inv.AddItemWithID(petItem.ObjectID, petItem.ItemID, petItem.Count,
			name, gfx, weight, false, petItem.Bless)
		handler.SendAddItem(sess, invItem)
	}
	pet.Items = nil
	handler.SendPetInventory(sess, pet)
}

// broadcastNpcChat 向附近玩家廣播 NPC 對話。
func broadcastNpcChat(ws *world.State, npcID int32, x, y int32, mapID int16, msg string) {
	nearby := ws.GetNearbyPlayersAt(x, y, mapID)
	for _, viewer := range nearby {
		handler.SendNpcChatPacket(viewer.Session, npcID, msg)
	}
}

// ========================================================================
//  寵物給予 / 馴服 / 進化
// ========================================================================

// GiveToPet 處理給予寵物物品（裝備或進化）。
func (s *PetSystem) GiveToPet(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, invItem *world.InvItem) {
	petType := s.deps.PetTypes.Get(pet.NpcID)
	if petType == nil {
		return
	}

	// 檢查是否為進化物品
	if petType.CanEvolve() && invItem.ItemID == petType.EvolvItemID {
		s.evolvePet(sess, player, pet, invItem)
		return
	}

	// 檢查是否為寵物裝備
	if s.deps.PetItems != nil {
		petItemInfo := s.deps.PetItems.Get(invItem.ItemID)
		if petItemInfo != nil {
			if !petType.CanEquip {
				return
			}
			if petNoEquipNpcIDs[pet.NpcID] {
				return
			}

			// 從玩家背包轉移到寵物
			player.Inv.RemoveItem(invItem.ObjectID, 1)
			handler.SendRemoveInventoryItem(sess, invItem.ObjectID)

			pet.Items = append(pet.Items, &world.PetInvItem{
				ItemID:   invItem.ItemID,
				ObjectID: invItem.ObjectID,
				Name:     invItem.Name,
				GfxID:    invItem.InvGfx,
				Count:    1,
				Equipped: false,
				IsWeapon: petItemInfo.IsWeapon(),
				Bless:    invItem.Bless,
			})
			handler.SendPetInventory(sess, pet)
		}
	}
}

// petNoEquipNpcIDs 不能裝備物品的寵物 NPC ID（Java 硬編碼列表）。
var petNoEquipNpcIDs = map[int32]bool{
	45034: true, 45039: true, 45040: true, 45042: true,
	45043: true, 45044: true, 45046: true, 45047: true,
	45048: true, 45049: true, 45053: true, 45054: true,
	45313: true, 46042: true, 45711: true, 46044: true,
}

// TameNpc 處理馴服野生 NPC 為寵物。
func (s *PetSystem) TameNpc(sess *net.Session, player *world.PlayerInfo, npc *world.NpcInfo) {
	ws := s.deps.World
	tmpl := s.deps.Npcs.Get(npc.NpcID)
	if tmpl == nil || !tmpl.Tameable {
		handler.SendServerMessage(sess, 324) // 馴養失敗
		return
	}

	// Java: NPC 血量必須低於 1/3 才能馴服
	if npc.HP > npc.MaxHP/3 {
		handler.SendServerMessage(sess, 324)
		return
	}

	// 特殊案例：Tiger Man (45313) 即使血量低於 1/3 也只有 1/16 機率
	if npc.NpcID == 45313 {
		if world.RandInt(16) != 15 {
			handler.SendServerMessage(sess, 324)
			return
		}
	}

	// CHA 檢查
	usedCost := s.CalcUsedPetCost(player.CharID)
	availCHA := int(player.Cha) - usedCost
	if availCHA < 6 {
		handler.SendServerMessage(sess, 489) // 你無法一次控制那麼多寵物
		return
	}

	// 背包空間檢查
	if player.Inv.Size() >= 180 {
		return
	}

	// 移除野生 NPC
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	ws.RemoveNpc(npc.ID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, npc.ID)
	}

	// 在玩家背包建立項圈物品
	var collarInfo *world.InvItem
	if s.deps.Items != nil {
		collarTmpl := s.deps.Items.Get(petCollarNormal)
		if collarTmpl != nil {
			collarInfo = player.Inv.AddItem(petCollarNormal, 1,
				collarTmpl.Name, collarTmpl.InvGfx, collarTmpl.Weight,
				false, 0)
			handler.SendAddItem(sess, collarInfo)
		}
	}
	if collarInfo == nil {
		return
	}

	// 儲存寵物到 DB
	petType := s.deps.PetTypes.Get(npc.NpcID)
	petName := npc.Name
	if petType != nil && petType.Name != "" {
		petName = petType.Name
	}

	if s.deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.deps.PetRepo.Save(ctx, &persist.PetRow{
			ItemObjID: collarInfo.ObjectID,
			NpcID:     npc.NpcID,
			Name:      petName,
			Level:     npc.Level,
			HP:        npc.MaxHP,
			MaxHP:     npc.MaxHP,
			MP:        npc.MaxMP,
			MaxMP:     npc.MaxMP,
			Exp:       750, // Java 預設馴服初始經驗
			Lawful:    0,
		})
		cancel()
	}
}

// Pet collar item IDs（與 handler/pet.go 相同常數，供馴服/進化使用）。
const (
	petCollarNormal int32 = 40314
	petCollarHigher int32 = 40316
)

// evolvePet 處理寵物進化。
func (s *PetSystem) evolvePet(sess *net.Session, player *world.PlayerInfo, pet *world.PetInfo, invItem *world.InvItem) {
	ws := s.deps.World

	// 等級檢查：必須 >= 30
	if pet.Level < 30 {
		return
	}

	petType := s.deps.PetTypes.Get(pet.NpcID)
	if petType == nil || !petType.CanEvolve() {
		return
	}

	// 查詢新 NPC 模板
	newTmpl := s.deps.Npcs.Get(petType.EvolvNpcID)
	if newTmpl == nil {
		return
	}

	// 消耗進化物品
	player.Inv.RemoveItem(invItem.ObjectID, 1)
	handler.SendRemoveInventoryItem(sess, invItem.ObjectID)

	// 移除舊項圈
	oldCollarItem := player.Inv.FindByObjectID(pet.ItemObjID)
	if oldCollarItem != nil {
		player.Inv.RemoveItem(pet.ItemObjID, 0)
		handler.SendRemoveInventoryItem(sess, pet.ItemObjID)
	}

	// 建立高級項圈
	var newCollar *world.InvItem
	if s.deps.Items != nil {
		collarTmpl := s.deps.Items.Get(petCollarHigher)
		if collarTmpl != nil {
			newCollar = player.Inv.AddItem(petCollarHigher, 1,
				collarTmpl.Name, collarTmpl.InvGfx, collarTmpl.Weight,
				false, 0)
			handler.SendAddItem(sess, newCollar)
		}
	}
	if newCollar == nil {
		return
	}

	// 刪除舊 DB 記錄
	if s.deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.deps.PetRepo.Delete(ctx, pet.ItemObjID)
		cancel()
	}

	// 廣播移除舊外觀
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, pet.ID)
	}

	// 變換寵物屬性 — Java: maxHP/2, maxMP/2, level=1, exp=0
	pet.NpcID = petType.EvolvNpcID
	pet.GfxID = newTmpl.GfxID
	pet.NameID = newTmpl.NameID
	pet.MaxHP = pet.MaxHP / 2
	if pet.MaxHP < 1 {
		pet.MaxHP = 1
	}
	pet.MaxMP = pet.MaxMP / 2
	pet.HP = pet.MaxHP
	pet.MP = pet.MaxMP
	pet.Level = 1
	pet.Exp = 0
	pet.ItemObjID = newCollar.ObjectID
	pet.AC = newTmpl.AC
	pet.STR = newTmpl.STR
	pet.DEX = newTmpl.DEX
	pet.MR = newTmpl.MR
	pet.AtkDmg = newTmpl.HP / 4
	pet.MoveSpeed = newTmpl.PassiveSpeed
	pet.Dirty = true

	// 脫下所有寵物裝備（進化後屬性改變）
	if pet.WeaponObjID != 0 {
		s.unequipPetWeapon(pet)
	}
	if pet.ArmorObjID != 0 {
		s.unequipPetArmor(pet)
	}

	// 儲存新 DB 記錄
	if s.deps.PetRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.deps.PetRepo.Save(ctx, &persist.PetRow{
			ItemObjID: newCollar.ObjectID,
			NpcID:     pet.NpcID,
			Name:      pet.Name,
			Level:     pet.Level,
			HP:        pet.HP,
			MaxHP:     pet.MaxHP,
			MP:        pet.MP,
			MaxMP:     pet.MaxMP,
			Exp:       pet.Exp,
			Lawful:    pet.Lawful,
		})
		cancel()
	}

	// 廣播新外觀 + 進化特效
	nearby = ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		handler.SendPetPack(viewer.Session, pet, isOwner, player.Name)
		handler.SendCompanionEffect(viewer.Session, pet.ID, 2127) // 升級光效
	}

	// 重新整理寵物控制面板
	handler.SendPetCtrlMenu(sess, pet, true)
	handler.SendPetHpMeter(sess, pet.ID, pet.HP, pet.MaxHP)
}

// ========================================================================
//  寵物裝備穿脫
// ========================================================================

// UsePetItem 處理寵物裝備穿脫（業務邏輯從 handler/pet_inventory.go 搬入）。
func (s *PetSystem) UsePetItem(sess *net.Session, pet *world.PetInfo, listNo int) {
	// 檢查此寵物類型是否可裝備物品
	if petNoEquipNpcIDs[pet.NpcID] {
		handler.SendPetInventory(sess, pet)
		return
	}
	petType := s.deps.PetTypes.Get(pet.NpcID)
	if petType != nil && !petType.CanEquip {
		handler.SendPetInventory(sess, pet)
		return
	}

	// 查詢物品索引
	if listNo < 0 || listNo >= len(pet.Items) {
		handler.SendPetInventory(sess, pet)
		return
	}
	item := pet.Items[listNo]

	// 切換裝備/脫下
	if item.IsWeapon {
		if item.Equipped {
			s.unequipPetWeapon(pet)
		} else {
			s.equipPetWeapon(pet, item)
		}
	} else {
		if item.Equipped {
			s.unequipPetArmor(pet)
		} else {
			s.equipPetArmor(pet, item)
		}
	}

	// 發送裝備更新 + 重新整理背包
	var equipMode byte
	if item.IsWeapon {
		equipMode = 1
	}
	var equipStatus byte
	if item.Equipped {
		equipStatus = 1
	}
	handler.SendPetEquipUpdate(sess, pet, equipMode, equipStatus)
	handler.SendPetInventory(sess, pet)
}

// equipPetWeapon 裝備寵物武器（牙齒），套用屬性加成。
func (s *PetSystem) equipPetWeapon(pet *world.PetInfo, item *world.PetInvItem) {
	if s.deps.PetItems == nil {
		return
	}
	info := s.deps.PetItems.Get(item.ItemID)
	if info == nil {
		return
	}

	// 先脫下現有武器
	if pet.WeaponObjID != 0 {
		s.unequipPetWeapon(pet)
	}

	pet.WeaponObjID = item.ObjectID
	item.Equipped = true

	pet.HitByWeapon = info.Hit
	pet.DamageByWeapon = info.Dmg

	pet.AddStr += info.AddStr
	pet.AddCon += info.AddCon
	pet.AddDex += info.AddDex
	pet.AddInt += info.AddInt
	pet.AddWis += info.AddWis
	pet.AddHP += info.AddHP
	pet.AddMP += info.AddMP
	pet.AddSP += info.AddSP
	pet.MDef += info.MDef
}

// unequipPetWeapon 脫下寵物武器，還原屬性加成。
func (s *PetSystem) unequipPetWeapon(pet *world.PetInfo) {
	if pet.WeaponObjID == 0 {
		return
	}

	var equippedItem *world.PetInvItem
	for _, it := range pet.Items {
		if it.ObjectID == pet.WeaponObjID && it.Equipped {
			equippedItem = it
			break
		}
	}

	if equippedItem != nil && s.deps.PetItems != nil {
		info := s.deps.PetItems.Get(equippedItem.ItemID)
		if info != nil {
			pet.AddStr -= info.AddStr
			pet.AddCon -= info.AddCon
			pet.AddDex -= info.AddDex
			pet.AddInt -= info.AddInt
			pet.AddWis -= info.AddWis
			pet.AddHP -= info.AddHP
			pet.AddMP -= info.AddMP
			pet.AddSP -= info.AddSP
			pet.MDef -= info.MDef
		}
		equippedItem.Equipped = false
	}

	pet.HitByWeapon = 0
	pet.DamageByWeapon = 0
	pet.WeaponObjID = 0
}

// equipPetArmor 裝備寵物防具，套用 AC 和屬性加成。
func (s *PetSystem) equipPetArmor(pet *world.PetInfo, item *world.PetInvItem) {
	if s.deps.PetItems == nil {
		return
	}
	info := s.deps.PetItems.Get(item.ItemID)
	if info == nil {
		return
	}

	// 先脫下現有防具
	if pet.ArmorObjID != 0 {
		s.unequipPetArmor(pet)
	}

	pet.ArmorObjID = item.ObjectID
	item.Equipped = true

	pet.AC -= int16(info.AC)

	pet.AddStr += info.AddStr
	pet.AddCon += info.AddCon
	pet.AddDex += info.AddDex
	pet.AddInt += info.AddInt
	pet.AddWis += info.AddWis
	pet.AddHP += info.AddHP
	pet.AddMP += info.AddMP
	pet.AddSP += info.AddSP
	pet.MDef += info.MDef
}

// unequipPetArmor 脫下寵物防具，還原 AC 和屬性加成。
func (s *PetSystem) unequipPetArmor(pet *world.PetInfo) {
	if pet.ArmorObjID == 0 {
		return
	}

	var equippedItem *world.PetInvItem
	for _, it := range pet.Items {
		if it.ObjectID == pet.ArmorObjID && it.Equipped {
			equippedItem = it
			break
		}
	}

	if equippedItem != nil && s.deps.PetItems != nil {
		info := s.deps.PetItems.Get(equippedItem.ItemID)
		if info != nil {
			pet.AC += int16(info.AC)
			pet.AddStr -= info.AddStr
			pet.AddCon -= info.AddCon
			pet.AddDex -= info.AddDex
			pet.AddInt -= info.AddInt
			pet.AddWis -= info.AddWis
			pet.AddHP -= info.AddHP
			pet.AddMP -= info.AddMP
			pet.AddSP -= info.AddSP
			pet.MDef -= info.MDef
		}
		equippedItem.Equipped = false
	}

	pet.ArmorObjID = 0
}
