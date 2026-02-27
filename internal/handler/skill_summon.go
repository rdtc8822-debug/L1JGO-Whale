package handler

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// ========================================================================
//  Skill 51: Summon Monster — Java: SUMMON_MONSTER.java
// ========================================================================

// Summon NPC lookup table (without ring): level → {npc_id, pet_cost}
var summonByLevel = []struct {
	maxLevel int16
	npcID    int32
	cost     int
}{
	{31, 81210, 6},
	{35, 81213, 6},
	{39, 81216, 6},
	{43, 81219, 6},
	{47, 81222, 6},
	{51, 81225, 6},
	{127, 81228, 6}, // 52+ (max sentinel)
}

// summonRingEntry maps a client-side summon selection ID to NPC template + requirements.
// Java: SUMMON_MONSTER.java arrays — client sends these IDs via C_UseSkill targetID when ring equipped.
type summonRingEntry struct {
	npcID    int32
	minLevel int16
	chaCost  int
}

// summonRingTable: summonID (from client) → {npcID, minLevel, chaCost}
// Java arrays: summon_id[], summon_npcid[], summon_lv[], petcost[]
var summonRingTable = map[int32]summonRingEntry{
	7:   {81210, 28, 8},
	263: {81211, 28, 8},
	519: {81212, 28, 8},
	8:   {81213, 32, 8},
	264: {81214, 32, 8},
	520: {81215, 32, 8},
	9:   {81216, 36, 8},
	265: {81217, 36, 8},
	521: {81218, 36, 8},
	10:  {81219, 40, 8},
	266: {81220, 40, 8},
	522: {81221, 40, 8},
	11:  {81222, 44, 8},
	267: {81223, 44, 8},
	523: {81224, 44, 8},
	12:  {81225, 48, 8},
	268: {81226, 48, 8},
	524: {81227, 48, 8},
	13:  {81228, 52, 8},
	269: {81229, 52, 8},
	525: {81230, 52, 8},
	14:  {81231, 56, 10},
	270: {81232, 56, 10},
	526: {81233, 56, 10},
	15:  {81234, 60, 12},
	271: {81235, 60, 12},
	527: {81236, 60, 12},
	16:  {81237, 64, 20},
	17:  {81238, 68, 42},
	18:  {81239, 72, 42},
	274: {81240, 72, 50},
}

// specialSummonNpcIDs are summons that cannot coexist with other pets/summons.
// Java: 變形怪(81238), 黑豹(81239), 巨大牛人(81240)
var specialSummonNpcIDs = map[int32]bool{
	81238: true,
	81239: true,
	81240: true,
}

// hasSummonRing returns true if the player has a summoning control ring equipped.
func hasSummonRing(player *world.PlayerInfo) bool {
	for _, slot := range []world.EquipSlot{world.SlotRing1, world.SlotRing2} {
		item := player.Equip.Get(slot)
		if item != nil && (item.ItemID == 20284 || item.ItemID == 120284) {
			return true
		}
	}
	return false
}

// HandleSummonRingSelection is called from HandleNpcAction when the player responds to
// the "summonlist" HTML dialog with a numeric string (e.g. "7", "263", "519").
// Java: L1ActionPc.java checks isSummonMonster() → calls summonMonster(pc, cmd).
func HandleSummonRingSelection(sess *net.Session, player *world.PlayerInfo, summonIDStr string, deps *Deps) {
	// NOTE: Do NOT clear SummonSelectionMode here — keep it true so the player
	// can click another option if this one fails. The flag is cleared only on success
	// (inside executeSummonMonster after spawning) or when the player does something else.

	// Parse the summon selection string to int32
	var summonID int32
	for _, c := range summonIDStr {
		if c < '0' || c > '9' {
			return // invalid
		}
		summonID = summonID*10 + int32(c-'0')
	}
	if summonID == 0 {
		return
	}

	// Look up the skill info for Summon Monster (skill 51) to consume resources
	skill := deps.Skills.Get(51)
	if skill == nil {
		return
	}

	// Re-validate MP/HP/materials (player state may have changed since dialog was shown)
	if skill.HpConsume > 0 && player.HP <= int16(skill.HpConsume) {
		sendServerMessage(sess, msgNotEnoughHP)
		return
	}
	if skill.MpConsume > 0 && player.MP < int16(skill.MpConsume) {
		sendServerMessage(sess, msgNotEnoughMP)
		return
	}
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot == nil || slot.Count < int32(skill.ItemConsumeCount) {
			sendServerMessage(sess, 299) // 施放魔法所需材料不足
			return
		}
	}

	// Delegate to executeSummonMonster with the selected summon ID as targetID.
	// On success, executeSummonMonster clears SummonSelectionMode.
	executeSummonMonster(sess, player, skill, summonID, deps)
}

// executeSummonMonster handles skill 51 (Summon Monster).
// Without the Summon Ring, auto-selects NPC based on caster level.
// With the Summon Ring (ItemID 20284/120284), uses targetID to select a specific summon.
// Java: SUMMON_MONSTER.java — ring check → lookup table → level/CHA validation → spawn.
func executeSummonMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	ws := deps.World

	deps.Log.Info("executeSummonMonster",
		zap.String("player", player.Name),
		zap.Int16("level", player.Level),
		zap.Int16("mapID", player.MapID),
		zap.Int32("targetID", targetID))

	// Check map allows summoning (RecallPets flag)
	if deps.MapData != nil {
		md := deps.MapData.GetInfo(player.MapID)
		if md != nil && !md.RecallPets {
			deps.Log.Info("summon blocked: map !RecallPets", zap.Int16("map", player.MapID))
			sendServerMessage(sess, 353) // "此處無法召喚怪物。"
			return
		}
	}

	// Level check (minimum level 28)
	if player.Level < 28 {
		deps.Log.Info("summon blocked: level < 28", zap.Int16("level", player.Level))
		sendServerMessage(sess, 743) // "等級太低而無法召喚怪物。"
		return
	}

	var npcID int32
	var petCost int

	ringEquipped := hasSummonRing(player)

	if ringEquipped && targetID > 0 {
		// Ring path: player selected a specific summon via targetID (from C_UseSkill)
		entry, ok := summonRingTable[targetID]
		if !ok {
			deps.Log.Warn("summon blocked: unknown ring summon ID", zap.Int32("summonID", targetID))
			sendServerMessage(sess, msgCastFail)
			return
		}
		if player.Level < entry.minLevel {
			deps.Log.Info("summon blocked: level too low for ring summon",
				zap.Int16("level", player.Level), zap.Int16("minLevel", entry.minLevel))
			sendServerMessage(sess, 743)
			return
		}
		// Special summons (81238/81239/81240) cannot coexist with other pets/summons
		if specialSummonNpcIDs[entry.npcID] {
			existingSummons := ws.GetSummonsByOwner(player.CharID)
			existingPets := ws.GetPetsByOwner(player.CharID)
			if len(existingSummons) > 0 || len(existingPets) > 0 {
				sendServerMessage(sess, 319) // "你不能擁有太多的怪物。"
				return
			}
		}
		npcID = entry.npcID
		petCost = entry.chaCost
	} else if ringEquipped {
		// Ring equipped but no selection yet (targetID == 0):
		// Send "summonlist" HTML dialog so the player can choose.
		// Java: S_ShowSummonList → opcode 39 with htmlID "summonlist".
		// The client's built-in dialog shows available summon options.
		// Player's response arrives via C_NPCAction (opcode 125) as a numeric string.
		player.SummonSelectionMode = true
		sendHypertext(sess, player.CharID, "summonlist")
		deps.Log.Info("summon ring: showing selection dialog")
		return
	} else {
		// Non-ring path: auto-select NPC by level
		for _, entry := range summonByLevel {
			if player.Level <= entry.maxLevel {
				npcID = entry.npcID
				petCost = entry.cost
				break
			}
		}
	}

	// Calculate available CHA
	baseCHA := int(player.Cha) + 6 // always +6 for summon skill
	usedCHA := calcUsedPetCost(player.CharID, ws)
	availCHA := baseCHA - usedCHA

	deps.Log.Info("summon CHA check",
		zap.Int("baseCHA", baseCHA), zap.Int("usedCHA", usedCHA),
		zap.Int("availCHA", availCHA), zap.Int("petCost", petCost),
		zap.Int32("npcID", npcID))

	if availCHA < petCost {
		sendServerMessage(sess, 319) // "你不能擁有太多的怪物。"
		return
	}

	// Look up NPC template
	tmpl := deps.Npcs.Get(npcID)
	if tmpl == nil {
		deps.Log.Warn("summon blocked: NPC template not found", zap.Int32("npcID", npcID))
		sendServerMessage(sess, msgCastFail)
		return
	}

	// Calculate how many can be summoned
	count := availCHA / petCost
	if count <= 0 {
		sendServerMessage(sess, 319)
		return
	}

	// Special summons (81238/81239/81240): cap to 1 — they are exclusive high-power summons.
	// Java: these have very high CHA cost (42-50) so players rarely get >1,
	// but we enforce it explicitly to prevent issues.
	if specialSummonNpcIDs[npcID] && count > 1 {
		count = 1
	}

	// All validation passed — consume resources now (MP/HP/items + cooldown)
	consumeSkillResources(sess, player, skill)

	// Clear summon selection mode on success (dialog can close now)
	player.SummonSelectionMode = false

	masterName := player.Name
	for i := 0; i < count; i++ {
		sum := &world.SummonInfo{
			ID:          world.NextNpcID(),
			OwnerCharID: player.CharID,
			NpcID:       npcID,
			GfxID:       tmpl.GfxID,
			NameID:      tmpl.NameID,
			Name:        tmpl.Name,
			Level:       tmpl.Level,
			HP:          tmpl.HP,
			MaxHP:       tmpl.HP,
			MP:          tmpl.MP,
			MaxMP:       tmpl.MP,
			AC:          tmpl.AC,
			STR:         tmpl.STR,
			DEX:         tmpl.DEX,
			MR:          tmpl.MR,
			AtkDmg:      int32(tmpl.Level) + int32(tmpl.STR)/3,
			AtkSpeed:    tmpl.AtkSpeed,
			MoveSpd:     tmpl.PassiveSpeed,
			Ranged:      tmpl.Ranged,
			Lawful:      tmpl.Lawful,
			Size:        tmpl.Size,
			PetCost:     petCost,
			X:           player.X + int32(world.RandInt(5)) - 2,
			Y:           player.Y + int32(world.RandInt(5)) - 2,
			MapID:       player.MapID,
			Heading:     player.Heading,
			Status:      world.SummonAggressive,
			Tamed:       false,
			TimerTicks:  3600 * 5, // 3600 seconds × 5 ticks/sec = 18000 ticks
		}

		ws.AddSummon(sum)

		// Broadcast appearance to nearby players
		nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
		for _, viewer := range nearby {
			isOwner := viewer.CharID == player.CharID
			SendSummonPack(viewer.Session, sum, isOwner, masterName)
		}
		// Also send to caster if not already in nearby list
		SendSummonPack(sess, sum, true, masterName)
	}

	// Show summon control menu for the first summon
	summons := ws.GetSummonsByOwner(player.CharID)
	if len(summons) > 0 {
		sendSummonMenu(sess, summons[0])
	}
}

// ========================================================================
//  Skill 36: Taming Monster — Java: L1SkillUse taming section
// ========================================================================

// executeTamingMonster handles skill 36 (Taming Monster).
// Tames a living NPC and converts it to a summon owned by the caster.
func executeTamingMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	ws := deps.World

	// Target must be a living NPC
	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		sendServerMessage(sess, 79) // "無效的目標。"
		return
	}

	// Check tameable flag from template
	tmpl := deps.Npcs.Get(npc.NpcID)
	if tmpl == nil || !tmpl.Tameable {
		sendServerMessage(sess, 79) // "無效的目標。"
		return
	}

	// Only works on L1Monster
	if npc.Impl != "L1Monster" {
		sendServerMessage(sess, 79)
		return
	}

	// CHA check with class bonus
	charisma := int(player.Cha)
	switch player.ClassType {
	case 2: // Elf
		charisma += 12
	case 3: // Wizard
		charisma += 6
	}
	usedCHA := calcUsedPetCost(player.CharID, ws)
	availCHA := charisma - usedCHA
	if availCHA < 6 {
		sendServerMessage(sess, 319) // "你不能擁有太多的怪物。"
		return
	}

	// All validation passed — consume resources now
	consumeSkillResources(sess, player, skill)

	// Create summon from NPC
	sum := &world.SummonInfo{
		ID:          world.NextNpcID(),
		OwnerCharID: player.CharID,
		NpcID:       npc.NpcID,
		GfxID:       npc.GfxID,
		NameID:      npc.NameID,
		Name:        npc.Name,
		Level:       npc.Level,
		HP:          npc.HP,
		MaxHP:       npc.MaxHP,
		MP:          npc.MP,
		MaxMP:       npc.MaxMP,
		AC:          npc.AC,
		STR:         npc.STR,
		DEX:         npc.DEX,
		MR:          npc.MR,
		AtkDmg:      npc.AtkDmg,
		AtkSpeed:    npc.AtkSpeed,
		MoveSpd:     npc.MoveSpeed,
		Ranged:      npc.Ranged,
		Lawful:      npc.Lawful,
		Size:        npc.Size,
		PetCost:     6,
		X:           npc.X,
		Y:           npc.Y,
		MapID:       npc.MapID,
		Heading:     npc.Heading,
		Status:      world.SummonRest, // tamed summons start in rest mode
		Tamed:       true,
		TimerTicks:  0, // permanent (no timer for tamed)
	}

	// 移除原始 NPC + 解鎖格子
	npc.Dead = true
	ws.NpcDied(npc)
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		SendRemoveObject(viewer.Session, npc.ID)
	}

	// Add summon to world
	ws.AddSummon(sum)

	masterName := player.Name
	nearby = ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		SendSummonPack(viewer.Session, sum, isOwner, masterName)
	}
	SendSummonPack(sess, sum, true, masterName)
	sendSummonMenu(sess, sum)
}

// ========================================================================
//  Skill 41: Create Zombie — Java: L1SummonInstance zombie constructor
// ========================================================================

// Zombie NPC lookup tables by class
var zombieByWizardLevel = []struct {
	minLevel, maxLevel int16
	npcID              int32
}{
	{24, 31, 81183},
	{32, 39, 81184},
	{40, 43, 81185},
	{44, 47, 81186},
	{48, 51, 81187},
	{52, 127, 81188},
}

// executeCreateZombie handles skill 41 (Create Zombie).
// Raises a dead NPC corpse as an undead summon.
func executeCreateZombie(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	ws := deps.World

	// Target must be a dead NPC
	npc := ws.GetNpc(targetID)
	if npc == nil || !npc.Dead {
		sendServerMessage(sess, 79) // "無效的目標。"
		return
	}

	// CHA check with class bonus
	charisma := int(player.Cha)
	switch player.ClassType {
	case 2: // Elf
		charisma += 12
	case 3: // Wizard
		charisma += 6
	}
	usedCHA := calcUsedPetCost(player.CharID, ws)
	availCHA := charisma - usedCHA
	if availCHA < 6 {
		sendServerMessage(sess, 319)
		return
	}

	// Select zombie NPC template by class + level
	var zombieNpcID int32 = 45065 // default fallback
	switch player.ClassType {
	case 3: // Wizard
		for _, entry := range zombieByWizardLevel {
			if player.Level >= entry.minLevel && player.Level <= entry.maxLevel {
				zombieNpcID = entry.npcID
				break
			}
		}
	case 2: // Elf
		if player.Level >= 48 {
			zombieNpcID = 81183
		}
	}

	tmpl := deps.Npcs.Get(zombieNpcID)
	if tmpl == nil {
		return
	}

	// All validation passed — consume resources now
	consumeSkillResources(sess, player, skill)

	sum := &world.SummonInfo{
		ID:          world.NextNpcID(),
		OwnerCharID: player.CharID,
		NpcID:       zombieNpcID,
		GfxID:       tmpl.GfxID,
		NameID:      tmpl.NameID,
		Name:        tmpl.Name,
		Level:       tmpl.Level,
		HP:          tmpl.HP,
		MaxHP:       tmpl.HP,
		MP:          tmpl.MP,
		MaxMP:       tmpl.MP,
		AC:          tmpl.AC,
		STR:         tmpl.STR,
		DEX:         tmpl.DEX,
		MR:          tmpl.MR,
		AtkDmg:      int32(tmpl.Level) + int32(tmpl.STR)/3,
		AtkSpeed:    tmpl.AtkSpeed,
		MoveSpd:     tmpl.PassiveSpeed,
		Ranged:      tmpl.Ranged,
		Lawful:      tmpl.Lawful,
		Size:        tmpl.Size,
		PetCost:     6,
		X:           npc.X,
		Y:           npc.Y,
		MapID:       npc.MapID,
		Heading:     npc.Heading,
		Status:      world.SummonRest,
		Tamed:       true,
		TimerTicks:  0, // permanent
	}

	// Remove corpse NPC from view (already dead, just remove the sprite)
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		SendRemoveObject(viewer.Session, npc.ID)
	}

	// Add summon to world
	ws.AddSummon(sum)

	masterName := player.Name
	nearby = ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		SendSummonPack(viewer.Session, sum, isOwner, masterName)
	}
	SendSummonPack(sess, sum, true, masterName)
	sendSummonMenu(sess, sum)
}

// ========================================================================
//  Skill 145: Return to Nature — Java: L1SummonInstance.returnToNature()
// ========================================================================

// executeReturnToNature handles skill 145 (Return to Nature).
// Non-tamed summons are destroyed. Tamed summons are liberated back to NPC form.
func executeReturnToNature(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, deps *Deps) {
	ws := deps.World
	summons := ws.GetSummonsByOwner(player.CharID)
	if len(summons) == 0 {
		return
	}

	// Validation passed (have summons) — consume resources now
	consumeSkillResources(sess, player, skill)

	for _, sum := range summons {
		if sum.Tamed {
			// Liberate: recreate the original NPC at summon's position
			liberateSummon(sum, deps)
		} else {
			// Death: skill-summoned creatures just disappear
			killSummon(sum, deps)
		}
	}
}

// liberateSummon converts a tamed summon back to a normal NPC.
func liberateSummon(sum *world.SummonInfo, deps *Deps) {
	ws := deps.World

	// Remove summon from world
	ws.RemoveSummon(sum.ID)
	nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		sendCompanionEffect(viewer.Session, sum.ID, 2245) // return to nature sound
		SendRemoveObject(viewer.Session, sum.ID)
	}

	// Look up original NPC template to recreate
	tmpl := deps.Npcs.Get(sum.NpcID)
	if tmpl == nil {
		return
	}

	// Create a new NPC at the summon's location
	npcID := world.NextNpcID()
	npc := &world.NpcInfo{
		ID:      npcID,
		NpcID:   sum.NpcID,
		Impl:    "L1Monster",
		GfxID:   tmpl.GfxID,
		Name:    tmpl.Name,
		NameID:  tmpl.NameID,
		Level:   sum.Level,
		HP:      sum.HP,
		MaxHP:   sum.MaxHP,
		MP:      sum.MP,
		MaxMP:   sum.MaxMP,
		AC:      sum.AC,
		STR:     sum.STR,
		DEX:     sum.DEX,
		MR:        sum.MR,
		PoisonAtk: tmpl.PoisonAtk,
		Exp:       0, // liberated NPCs give no exp
		Lawful:  sum.Lawful,
		Size:    sum.Size,
		AtkDmg:  sum.AtkDmg,
		Ranged:  sum.Ranged,
		X:       sum.X,
		Y:       sum.Y,
		MapID:   sum.MapID,
		Heading: sum.Heading,
	}

	ws.AddNpc(npc)
	nearby = ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		SendNpcPack(viewer.Session, npc)
	}
}

// killSummon destroys a non-tamed summon (skill-summoned creatures).
func killSummon(sum *world.SummonInfo, deps *Deps) {
	ws := deps.World
	ws.RemoveSummon(sum.ID)
	nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		sendCompanionEffect(viewer.Session, sum.ID, 169) // summon death sound
		SendRemoveObject(viewer.Session, sum.ID)
	}
}

// DismissSummon handles voluntary summon dismissal (from NPC action menu).
// Tamed summons are liberated; skill-summoned are destroyed.
func DismissSummon(sum *world.SummonInfo, player *world.PlayerInfo, deps *Deps) {
	if sum.Tamed {
		liberateSummon(sum, deps)
	} else {
		killSummon(sum, deps)
	}
}

// ========================================================================
//  CHA cost helper
// ========================================================================

// calcUsedPetCost sums the CHA cost of all active pets/summons owned by a player.
// Includes pets (PetInfo) and summons (SummonInfo).
// Java: iterates petList, sums each pet's petcost field.
func calcUsedPetCost(charID int32, ws *world.State) int {
	cost := 0
	// Pets: each costs 6 CHA
	for _, pet := range ws.GetPetsByOwner(charID) {
		_ = pet
		cost += 6
	}
	// Summons: use actual PetCost (ring summons have variable costs 8-50)
	for _, sum := range ws.GetSummonsByOwner(charID) {
		c := sum.PetCost
		if c <= 0 {
			c = 6 // default for non-ring summons
		}
		cost += c
	}
	return cost
}

// --- Exported wrappers for system package usage ---

// ExecuteSummonMonster 召喚怪物（技能 51）。
func ExecuteSummonMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	executeSummonMonster(sess, player, skill, targetID, deps)
}

// ExecuteTamingMonster 馴服怪物（技能 36）。
func ExecuteTamingMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	executeTamingMonster(sess, player, skill, targetID, deps)
}

// ExecuteCreateZombie 創造殭屍（技能 41）。
func ExecuteCreateZombie(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, deps *Deps) {
	executeCreateZombie(sess, player, skill, targetID, deps)
}

// ExecuteReturnToNature 歸返自然（技能 145）。
func ExecuteReturnToNature(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, deps *Deps) {
	executeReturnToNature(sess, player, skill, deps)
}
