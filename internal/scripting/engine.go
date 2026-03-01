package scripting

import (
	"fmt"
	"os"
	"path/filepath"

	lua "github.com/yuin/gopher-lua"
	"go.uber.org/zap"
)

// Engine wraps a single gopher-lua VM for game logic execution.
// Single-goroutine access only (game loop). Hot-reload planned via atomic swap.
type Engine struct {
	vm  *lua.LState
	log *zap.Logger
}

// NewEngine creates a Lua engine and loads all scripts from the given directory.
func NewEngine(scriptsDir string, log *zap.Logger) (*Engine, error) {
	vm := lua.NewState(lua.Options{
		SkipOpenLibs: false,
	})

	// Set API version global
	vm.SetGlobal("API_VERSION", lua.LNumber(1))

	e := &Engine{vm: vm, log: log}

	// Load core scripts first, then feature scripts
	corePath := filepath.Join(scriptsDir, "core")
	if err := e.loadDir(corePath); err != nil {
		vm.Close()
		return nil, fmt.Errorf("load core scripts: %w", err)
	}

	combatPath := filepath.Join(scriptsDir, "combat")
	if err := e.loadDir(combatPath); err != nil {
		vm.Close()
		return nil, fmt.Errorf("load combat scripts: %w", err)
	}

	// Load optional feature script directories
	for _, sub := range []string{"item", "character", "skill", "world", "ai"} {
		p := filepath.Join(scriptsDir, sub)
		if err := e.loadDir(p); err != nil {
			vm.Close()
			return nil, fmt.Errorf("load %s scripts: %w", sub, err)
		}
	}

	return e, nil
}

// loadDir loads all .lua files in a directory.
func (e *Engine) loadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // skip missing dirs
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lua" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := e.vm.DoFile(path); err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		e.log.Debug("loaded lua script", zap.String("file", path))
	}
	return nil
}

// CombatContext holds pre-packed data for a melee attack calculation.
type CombatContext struct {
	AttackerLevel  int
	AttackerSTR    int
	AttackerDEX    int
	AttackerWeapon int // max weapon damage (0 = fist = 4)
	AttackerHitMod int // equipment hit modifier
	AttackerDmgMod int // equipment damage modifier
	TargetAC       int
	TargetLevel    int
	TargetMR       int
	TargetClassType int // 目標職業（-1=NPC, 0-7=玩家職業）— AC 防禦加成用
}

// RangedCombatContext holds pre-packed data for a ranged (bow) attack.
type RangedCombatContext struct {
	AttackerLevel     int
	AttackerSTR       int
	AttackerDEX       int
	AttackerBowDmg    int // bow weapon damage
	AttackerArrowDmg  int // arrow damage bonus
	AttackerBowHitMod int // bow hit modifier (equipment + buffs)
	AttackerBowDmgMod int // bow damage modifier (equipment + buffs)
	TargetAC          int
	TargetLevel       int
	TargetMR          int
	TargetClassType   int // 目標職業（-1=NPC, 0-7=玩家職業）
}

// CombatResult is returned by the Lua combat function.
type CombatResult struct {
	IsHit  bool
	Damage int
}

// CalcMeleeAttack calls the Lua calc_melee_attack function.
func (e *Engine) CalcMeleeAttack(ctx CombatContext) CombatResult {
	fn := e.vm.GetGlobal("calc_melee_attack")
	if fn == lua.LNil {
		e.log.Error("lua function calc_melee_attack not found")
		return CombatResult{IsHit: true, Damage: 1}
	}

	// Build context table
	t := e.vm.NewTable()

	atk := e.vm.NewTable()
	atk.RawSetString("level", lua.LNumber(ctx.AttackerLevel))
	atk.RawSetString("str", lua.LNumber(ctx.AttackerSTR))
	atk.RawSetString("dex", lua.LNumber(ctx.AttackerDEX))
	atk.RawSetString("weapon_dmg", lua.LNumber(ctx.AttackerWeapon))
	atk.RawSetString("hit_mod", lua.LNumber(ctx.AttackerHitMod))
	atk.RawSetString("dmg_mod", lua.LNumber(ctx.AttackerDmgMod))
	t.RawSetString("attacker", atk)

	tgt := e.vm.NewTable()
	tgt.RawSetString("ac", lua.LNumber(ctx.TargetAC))
	tgt.RawSetString("level", lua.LNumber(ctx.TargetLevel))
	tgt.RawSetString("mr", lua.LNumber(ctx.TargetMR))
	tgt.RawSetString("class_type", lua.LNumber(ctx.TargetClassType))
	t.RawSetString("target", tgt)

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_melee_attack error", zap.Error(err))
		return CombatResult{IsHit: true, Damage: 1}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		e.log.Error("lua calc_melee_attack returned non-table")
		return CombatResult{IsHit: true, Damage: 1}
	}

	return CombatResult{
		IsHit:  rt.RawGetString("is_hit") == lua.LTrue,
		Damage: int(lua.LVAsNumber(rt.RawGetString("damage"))),
	}
}

// CalcRangedAttack calls the Lua calc_ranged_attack function.
func (e *Engine) CalcRangedAttack(ctx RangedCombatContext) CombatResult {
	fn := e.vm.GetGlobal("calc_ranged_attack")
	if fn == lua.LNil {
		e.log.Error("lua function calc_ranged_attack not found")
		return CombatResult{IsHit: true, Damage: 1}
	}

	t := e.vm.NewTable()

	atk := e.vm.NewTable()
	atk.RawSetString("level", lua.LNumber(ctx.AttackerLevel))
	atk.RawSetString("str", lua.LNumber(ctx.AttackerSTR))
	atk.RawSetString("dex", lua.LNumber(ctx.AttackerDEX))
	atk.RawSetString("bow_dmg", lua.LNumber(ctx.AttackerBowDmg))
	atk.RawSetString("arrow_dmg", lua.LNumber(ctx.AttackerArrowDmg))
	atk.RawSetString("bow_hit_mod", lua.LNumber(ctx.AttackerBowHitMod))
	atk.RawSetString("bow_dmg_mod", lua.LNumber(ctx.AttackerBowDmgMod))
	t.RawSetString("attacker", atk)

	tgt := e.vm.NewTable()
	tgt.RawSetString("ac", lua.LNumber(ctx.TargetAC))
	tgt.RawSetString("level", lua.LNumber(ctx.TargetLevel))
	tgt.RawSetString("mr", lua.LNumber(ctx.TargetMR))
	tgt.RawSetString("class_type", lua.LNumber(ctx.TargetClassType))
	t.RawSetString("target", tgt)

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_ranged_attack error", zap.Error(err))
		return CombatResult{IsHit: true, Damage: 1}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		e.log.Error("lua calc_ranged_attack returned non-table")
		return CombatResult{IsHit: true, Damage: 1}
	}

	return CombatResult{
		IsHit:  rt.RawGetString("is_hit") == lua.LTrue,
		Damage: int(lua.LVAsNumber(rt.RawGetString("damage"))),
	}
}

// SkillDamageContext holds pre-packed data for skill damage calculation.
type SkillDamageContext struct {
	// Skill
	SkillID         int
	DamageValue     int
	DamageDice      int
	DamageDiceCount int
	SkillLevel      int
	Attr            int // element: 0=none, 1=earth, 2=fire, 4=water, 8=wind, 16=light

	// Attacker
	AttackerLevel  int
	AttackerSTR    int
	AttackerDEX    int
	AttackerINT    int
	AttackerWIS    int
	AttackerSP     int
	AttackerDmgMod int
	AttackerHitMod int
	AttackerWeapon int // weapon max damage (0 = fist)
	AttackerHP         int
	AttackerMaxHP      int
	AttackerMagicLevel int // 職業魔法等級（get_magic_level 計算結果）

	// Target
	TargetAC       int
	TargetLevel    int
	TargetMR       int
	TargetFireRes  int
	TargetWaterRes int
	TargetWindRes  int
	TargetEarthRes int
	TargetMP       int
}

// SkillDamageResult is returned by the Lua skill damage function.
type SkillDamageResult struct {
	Damage   int
	DrainMP  int // MP drained from target (Mind Break)
	HitCount int // number of hits (Triple Arrow = 3, default = 1)
}

// CalcSkillDamage calls the Lua calc_skill_damage function.
func (e *Engine) CalcSkillDamage(ctx SkillDamageContext) SkillDamageResult {
	fn := e.vm.GetGlobal("calc_skill_damage")
	if fn == lua.LNil {
		e.log.Error("lua function calc_skill_damage not found")
		return SkillDamageResult{Damage: 1, HitCount: 1}
	}

	t := e.vm.NewTable()

	sk := e.vm.NewTable()
	sk.RawSetString("id", lua.LNumber(ctx.SkillID))
	sk.RawSetString("damage_value", lua.LNumber(ctx.DamageValue))
	sk.RawSetString("damage_dice", lua.LNumber(ctx.DamageDice))
	sk.RawSetString("damage_dice_count", lua.LNumber(ctx.DamageDiceCount))
	sk.RawSetString("skill_level", lua.LNumber(ctx.SkillLevel))
	sk.RawSetString("attr", lua.LNumber(ctx.Attr))
	t.RawSetString("skill", sk)

	atk := e.vm.NewTable()
	atk.RawSetString("level", lua.LNumber(ctx.AttackerLevel))
	atk.RawSetString("str", lua.LNumber(ctx.AttackerSTR))
	atk.RawSetString("dex", lua.LNumber(ctx.AttackerDEX))
	atk.RawSetString("intel", lua.LNumber(ctx.AttackerINT))
	atk.RawSetString("wis", lua.LNumber(ctx.AttackerWIS))
	atk.RawSetString("sp", lua.LNumber(ctx.AttackerSP))
	atk.RawSetString("dmg_mod", lua.LNumber(ctx.AttackerDmgMod))
	atk.RawSetString("hit_mod", lua.LNumber(ctx.AttackerHitMod))
	atk.RawSetString("weapon_dmg", lua.LNumber(ctx.AttackerWeapon))
	atk.RawSetString("hp", lua.LNumber(ctx.AttackerHP))
	atk.RawSetString("max_hp", lua.LNumber(ctx.AttackerMaxHP))
	atk.RawSetString("magic_level", lua.LNumber(ctx.AttackerMagicLevel))
	t.RawSetString("attacker", atk)

	tgt := e.vm.NewTable()
	tgt.RawSetString("ac", lua.LNumber(ctx.TargetAC))
	tgt.RawSetString("level", lua.LNumber(ctx.TargetLevel))
	tgt.RawSetString("mr", lua.LNumber(ctx.TargetMR))
	tgt.RawSetString("fire_res", lua.LNumber(ctx.TargetFireRes))
	tgt.RawSetString("water_res", lua.LNumber(ctx.TargetWaterRes))
	tgt.RawSetString("wind_res", lua.LNumber(ctx.TargetWindRes))
	tgt.RawSetString("earth_res", lua.LNumber(ctx.TargetEarthRes))
	tgt.RawSetString("mp", lua.LNumber(ctx.TargetMP))
	t.RawSetString("target", tgt)

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_skill_damage error", zap.Error(err))
		return SkillDamageResult{Damage: 1, HitCount: 1}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		e.log.Error("lua calc_skill_damage returned non-table")
		return SkillDamageResult{Damage: 1, HitCount: 1}
	}

	hitCount := int(lua.LVAsNumber(rt.RawGetString("hit_count")))
	if hitCount < 1 {
		hitCount = 1
	}

	return SkillDamageResult{
		Damage:   int(lua.LVAsNumber(rt.RawGetString("damage"))),
		DrainMP:  int(lua.LVAsNumber(rt.RawGetString("drain_mp"))),
		HitCount: hitCount,
	}
}

// LevelFromExp calls Lua level_from_exp(exp).
func (e *Engine) LevelFromExp(exp int) int {
	return e.callIntFunc("level_from_exp", exp)
}

// ExpForLevel calls Lua exp_for_level(level).
func (e *Engine) ExpForLevel(level int) int {
	return e.callIntFunc("exp_for_level", level)
}

// --- Buff System Bridge ---

// BuffEffect holds stat deltas and flags returned by Lua get_buff_effect().
type BuffEffect struct {
	AC, Str, Dex, Con, Wis, Intel, Cha int
	MaxHP, MaxMP                        int
	HitMod, DmgMod                      int
	SP, MR                              int
	HPR, MPR                            int
	BowHit, BowDmg                      int
	Dodge                               int
	FireRes, WaterRes, WindRes, EarthRes int
	Exclusions                          []int
	MoveSpeed                           int  // 1=haste, 2=slow
	BraveSpeed                          int  // 4=brave/holy walk
	Invisible                           bool
	Paralyzed                           bool
	Sleeped                             bool
}

// GetBuffEffect calls Lua get_buff_effect(skill_id, target_level).
// Returns nil if no definition exists (generic timer buff).
func (e *Engine) GetBuffEffect(skillID, targetLevel int) *BuffEffect {
	fn := e.vm.GetGlobal("get_buff_effect")
	if fn == lua.LNil {
		return nil
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lua.LNumber(skillID), lua.LNumber(targetLevel)); err != nil {
		e.log.Error("lua get_buff_effect error", zap.Error(err), zap.Int("skill_id", skillID))
		return nil
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	if result == lua.LNil {
		return nil
	}

	rt, ok := result.(*lua.LTable)
	if !ok {
		return nil
	}

	eff := &BuffEffect{
		AC:         lInt(rt, "ac"),
		Str:        lInt(rt, "str"),
		Dex:        lInt(rt, "dex"),
		Con:        lInt(rt, "con"),
		Wis:        lInt(rt, "wis"),
		Intel:      lInt(rt, "intel"),
		Cha:        lInt(rt, "cha"),
		MaxHP:      lInt(rt, "max_hp"),
		MaxMP:      lInt(rt, "max_mp"),
		HitMod:     lInt(rt, "hit_mod"),
		DmgMod:     lInt(rt, "dmg_mod"),
		SP:         lInt(rt, "sp"),
		MR:         lInt(rt, "mr"),
		HPR:        lInt(rt, "hpr"),
		MPR:        lInt(rt, "mpr"),
		BowHit:     lInt(rt, "bow_hit"),
		BowDmg:     lInt(rt, "bow_dmg"),
		Dodge:      lInt(rt, "dodge"),
		FireRes:    lInt(rt, "fire_res"),
		WaterRes:   lInt(rt, "water_res"),
		WindRes:    lInt(rt, "wind_res"),
		EarthRes:   lInt(rt, "earth_res"),
		MoveSpeed:  lInt(rt, "move_speed"),
		BraveSpeed: lInt(rt, "brave_speed"),
		Invisible:  rt.RawGetString("invisible") == lua.LTrue,
		Paralyzed:  rt.RawGetString("paralyzed") == lua.LTrue,
		Sleeped:    rt.RawGetString("sleeped") == lua.LTrue,
	}

	// Parse exclusions array
	excVal := rt.RawGetString("exclusions")
	if excTbl, ok := excVal.(*lua.LTable); ok {
		excTbl.ForEach(func(_, v lua.LValue) {
			eff.Exclusions = append(eff.Exclusions, int(lua.LVAsNumber(v)))
		})
	}

	return eff
}

// IsNonCancellable calls Lua is_non_cancellable(skill_id).
func (e *Engine) IsNonCancellable(skillID int) bool {
	fn := e.vm.GetGlobal("is_non_cancellable")
	if fn == lua.LNil {
		return false
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lua.LNumber(skillID)); err != nil {
		return false
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)
	return result == lua.LTrue
}

// --- Level Up Bridge ---

// LevelUpResult holds HP/MP gains from Lua.
type LevelUpResult struct {
	HP int
	MP int
}

// CalcLevelUp calls Lua calc_level_up_hp and calc_level_up_mp.
func (e *Engine) CalcLevelUp(classType, con, wis int) LevelUpResult {
	return LevelUpResult{
		HP: e.callIntFunc("calc_level_up_hp", classType, con),
		MP: e.callIntFunc("calc_level_up_mp", classType, wis),
	}
}

// --- Potion Bridge ---

// PotionEffect holds potion data returned by Lua.
type PotionEffect struct {
	Type          string // "heal", "mana", "haste", "brave", "wisdom", "blue_potion", "cure_poison", "eva_breath", "third_speed", "blind"
	Amount        int    // heal/mana base amount
	Range         int    // mana: if > 0, actual = amount + rand(range)
	Duration      int    // buff duration in seconds
	BraveType     int    // brave sub-type (1=brave, 3=elf brave, 5=ribrave)
	GfxID         int    // visual effect GFX
	SP            int    // wisdom potion: SP bonus to add
	ClassRestrict string // brave class restriction: "knight","elf","crown","notDKIL","DKIL",""
}

// GetPotionEffect calls Lua get_potion_effect(item_id).
func (e *Engine) GetPotionEffect(itemID int) *PotionEffect {
	fn := e.vm.GetGlobal("get_potion_effect")
	if fn == lua.LNil {
		return nil
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lua.LNumber(itemID)); err != nil {
		e.log.Error("lua get_potion_effect error", zap.Error(err))
		return nil
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	if result == lua.LNil {
		return nil
	}

	rt, ok := result.(*lua.LTable)
	if !ok {
		return nil
	}

	return &PotionEffect{
		Type:          lStr(rt, "type"),
		Amount:        lInt(rt, "amount"),
		Range:         lInt(rt, "range"),
		Duration:      lInt(rt, "duration"),
		BraveType:     lInt(rt, "brave_type"),
		GfxID:         lInt(rt, "gfx"),
		SP:            lInt(rt, "sp"),
		ClassRestrict: lStr(rt, "class_restrict"),
	}
}

// --- Heal Formula Bridge ---

// CalcHeal calls Lua calc_heal_amount(skill_data, caster_data).
func (e *Engine) CalcHeal(damageValue, damageDice, damageDiceCount, intel, sp int) int {
	return e.callIntFunc("calc_heal_amount", damageValue, damageDice, damageDiceCount, intel, sp)
}

// --- Character Creation Bridge ---

// CharCreateData holds class creation data from Lua.
type CharCreateData struct {
	BaseSTR, BaseDEX, BaseCON, BaseWIS, BaseCHA, BaseINT int
	BonusAmount                                           int
	BaseHP, BaseMP                                        int
	MaleGFX, FemaleGFX                                    int
	InitialSpells                                         []int
}

// GetCharCreateData calls Lua get_char_create_data(class_type).
func (e *Engine) GetCharCreateData(classType int) *CharCreateData {
	fn := e.vm.GetGlobal("get_char_create_data")
	if fn == lua.LNil {
		return nil
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lua.LNumber(classType)); err != nil {
		e.log.Error("lua get_char_create_data error", zap.Error(err))
		return nil
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	if result == lua.LNil {
		return nil
	}

	rt, ok := result.(*lua.LTable)
	if !ok {
		return nil
	}

	data := &CharCreateData{
		BaseSTR:     lInt(rt, "str"),
		BaseDEX:     lInt(rt, "dex"),
		BaseCON:     lInt(rt, "con"),
		BaseWIS:     lInt(rt, "wis"),
		BaseCHA:     lInt(rt, "cha"),
		BaseINT:     lInt(rt, "intel"),
		BonusAmount: lInt(rt, "bonus"),
		BaseHP:      lInt(rt, "base_hp"),
		BaseMP:      lInt(rt, "base_mp"),
		MaleGFX:     lInt(rt, "male_gfx"),
		FemaleGFX:   lInt(rt, "female_gfx"),
	}

	spellsVal := rt.RawGetString("initial_spells")
	if spellsTbl, ok := spellsVal.(*lua.LTable); ok {
		spellsTbl.ForEach(func(_, v lua.LValue) {
			data.InitialSpells = append(data.InitialSpells, int(lua.LVAsNumber(v)))
		})
	}

	return data
}

// CalcInitHP calls Lua calc_init_hp(class_type, con).
func (e *Engine) CalcInitHP(classType, con int) int {
	return e.callIntFunc("calc_init_hp", classType, con)
}

// CalcInitMP calls Lua calc_init_mp(class_type, wis).
func (e *Engine) CalcInitMP(classType, wis int) int {
	return e.callIntFunc("calc_init_mp", classType, wis)
}

// --- Resurrection Bridge ---

// ResurrectResult holds resurrection effect data.
type ResurrectResult struct {
	HPRatio  float64 // 0.0-1.0 ratio of MaxHP to restore, or 0 for fixed amount
	MPRatio  float64
	FixedHP  int     // fixed HP amount (for skill 18: caster's level)
}

// GetResurrectEffect calls Lua get_resurrect_effect(skill_id).
func (e *Engine) GetResurrectEffect(skillID int) *ResurrectResult {
	fn := e.vm.GetGlobal("get_resurrect_effect")
	if fn == lua.LNil {
		return nil
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lua.LNumber(skillID)); err != nil {
		e.log.Error("lua get_resurrect_effect error", zap.Error(err))
		return nil
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	if result == lua.LNil {
		return nil
	}

	rt, ok := result.(*lua.LTable)
	if !ok {
		return nil
	}

	return &ResurrectResult{
		HPRatio: float64(lua.LVAsNumber(rt.RawGetString("hp_ratio"))),
		MPRatio: float64(lua.LVAsNumber(rt.RawGetString("mp_ratio"))),
		FixedHP: lInt(rt, "fixed_hp"),
	}
}

// --- Spell Shop Bridge ---

// SpellTierInfo holds one tier of spell shop data.
type SpellTierInfo struct {
	MinSkillLevel int
	MaxSkillLevel int
	MinCharLevel  int
	Cost          int
}

// GetSpellTiers calls Lua get_spell_tiers(class_type).
func (e *Engine) GetSpellTiers(classType int) []SpellTierInfo {
	fn := e.vm.GetGlobal("get_spell_tiers")
	if fn == lua.LNil {
		return nil
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lua.LNumber(classType)); err != nil {
		e.log.Error("lua get_spell_tiers error", zap.Error(err))
		return nil
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	if result == lua.LNil {
		return nil
	}

	rt, ok := result.(*lua.LTable)
	if !ok {
		return nil
	}

	var tiers []SpellTierInfo
	rt.ForEach(func(_, v lua.LValue) {
		if row, ok := v.(*lua.LTable); ok {
			tiers = append(tiers, SpellTierInfo{
				MinSkillLevel: lInt(row, "min_skill_level"),
				MaxSkillLevel: lInt(row, "max_skill_level"),
				MinCharLevel:  lInt(row, "min_char_level"),
				Cost:          lInt(row, "cost"),
			})
		}
	})
	return tiers
}

// --- Death/Respawn Bridge ---

// RespawnLocation holds a respawn coordinate.
type RespawnLocation struct {
	X, Y int
	Map  int
}

// GetRespawnLocation calls Lua get_respawn_location(map_id).
func (e *Engine) GetRespawnLocation(mapID int) *RespawnLocation {
	fn := e.vm.GetGlobal("get_respawn_location")
	if fn == lua.LNil {
		return nil
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lua.LNumber(mapID)); err != nil {
		e.log.Error("lua get_respawn_location error", zap.Error(err))
		return nil
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	if result == lua.LNil {
		return nil
	}

	rt, ok := result.(*lua.LTable)
	if !ok {
		return nil
	}

	return &RespawnLocation{
		X:   lInt(rt, "x"),
		Y:   lInt(rt, "y"),
		Map: lInt(rt, "map"),
	}
}

// CalcDeathExpPenalty calls Lua calc_death_exp_penalty(level, exp).
func (e *Engine) CalcDeathExpPenalty(level, exp int) int {
	return e.callIntFunc("calc_death_exp_penalty", level, exp)
}

// --- Enchant Bridge ---

// EnchantContext holds data for enchant scroll calculation.
type EnchantContext struct {
	ScrollBless  int     // 0=normal, 1=blessed, 2=cursed
	EnchantLvl   int     // current enchant level
	SafeEnchant  int     // safe enchant threshold
	Category     int     // 1=weapon, 2=armor
	WeaponChance float64 // config success rate for weapons
	ArmorChance  float64 // config success rate for armor
}

// EnchantResult is returned by the Lua enchant function.
type EnchantResult struct {
	Result string // "success", "fail", "break", "minus"
	Amount int    // enchant delta
}

// CalcEnchant calls Lua calc_enchant(ctx).
func (e *Engine) CalcEnchant(ctx EnchantContext) EnchantResult {
	fn := e.vm.GetGlobal("calc_enchant")
	if fn == lua.LNil {
		e.log.Error("lua function calc_enchant not found")
		return EnchantResult{Result: "fail"}
	}

	t := e.vm.NewTable()
	t.RawSetString("scroll_bless", lua.LNumber(ctx.ScrollBless))
	t.RawSetString("enchant_lvl", lua.LNumber(ctx.EnchantLvl))
	t.RawSetString("safe_enchant", lua.LNumber(ctx.SafeEnchant))
	t.RawSetString("category", lua.LNumber(ctx.Category))
	t.RawSetString("weapon_chance", lua.LNumber(ctx.WeaponChance))
	t.RawSetString("armor_chance", lua.LNumber(ctx.ArmorChance))

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_enchant error", zap.Error(err))
		return EnchantResult{Result: "fail"}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		e.log.Error("lua calc_enchant returned non-table")
		return EnchantResult{Result: "fail"}
	}

	return EnchantResult{
		Result: lStr(rt, "result"),
		Amount: lInt(rt, "amount"),
	}
}

// --- NPC AI Bridge ---

// MobSkillEntry holds a single mob skill passed into AI context.
type MobSkillEntry struct {
	SkillID       int
	MpConsume     int
	TriggerRandom int
	TriggerHP     int
	TriggerRange  int
	ActID         int
	GfxID         int // mob-specific override for spell effect (0 = use skill's CastGfx)
}

// AIContext holds pre-packed data for NPC AI decisions.
type AIContext struct {
	NpcID      int
	X, Y       int
	MapID      int
	HP, MaxHP  int
	MP, MaxMP  int
	Level      int
	AtkDmg     int
	AtkSpeed   int // ms
	MoveSpeed  int // ms
	Ranged     int // 1=melee, >1=ranged range
	Agro       bool

	// Target (detected by Go; 0 = no target)
	TargetID     int
	TargetX      int
	TargetY      int
	TargetDist   int // Chebyshev distance
	TargetAC     int
	TargetLevel  int

	// Cooldown state
	CanAttack bool
	CanMove   bool

	// Mob skills
	Skills []MobSkillEntry

	// Wander state
	WanderDist int
	SpawnDist  int // distance from spawn point
}

// AICommand is a single action returned by Lua AI.
type AICommand struct {
	Type    string // "attack", "ranged_attack", "skill", "move_toward", "wander", "lose_aggro", "idle"
	SkillID int
	ActID   int
	GfxID   int // mob-specific spell effect override (0 = use skill's CastGfx)
	Dir     int // heading 0-7 for wander (-1 = continue current)
}

// RunNpcAI calls Lua npc_ai(ctx) and returns a list of commands.
func (e *Engine) RunNpcAI(ctx AIContext) []AICommand {
	fn := e.vm.GetGlobal("npc_ai")
	if fn == lua.LNil {
		return nil
	}

	// Build context table
	t := e.vm.NewTable()
	t.RawSetString("npc_id", lua.LNumber(ctx.NpcID))
	t.RawSetString("x", lua.LNumber(ctx.X))
	t.RawSetString("y", lua.LNumber(ctx.Y))
	t.RawSetString("map_id", lua.LNumber(ctx.MapID))
	t.RawSetString("hp", lua.LNumber(ctx.HP))
	t.RawSetString("max_hp", lua.LNumber(ctx.MaxHP))
	t.RawSetString("mp", lua.LNumber(ctx.MP))
	t.RawSetString("max_mp", lua.LNumber(ctx.MaxMP))
	t.RawSetString("level", lua.LNumber(ctx.Level))
	t.RawSetString("atk_dmg", lua.LNumber(ctx.AtkDmg))
	t.RawSetString("atk_speed", lua.LNumber(ctx.AtkSpeed))
	t.RawSetString("move_speed", lua.LNumber(ctx.MoveSpeed))
	t.RawSetString("ranged", lua.LNumber(ctx.Ranged))
	if ctx.Agro {
		t.RawSetString("agro", lua.LTrue)
	} else {
		t.RawSetString("agro", lua.LFalse)
	}

	t.RawSetString("target_id", lua.LNumber(ctx.TargetID))
	t.RawSetString("target_x", lua.LNumber(ctx.TargetX))
	t.RawSetString("target_y", lua.LNumber(ctx.TargetY))
	t.RawSetString("target_dist", lua.LNumber(ctx.TargetDist))
	t.RawSetString("target_ac", lua.LNumber(ctx.TargetAC))
	t.RawSetString("target_level", lua.LNumber(ctx.TargetLevel))

	if ctx.CanAttack {
		t.RawSetString("can_attack", lua.LTrue)
	} else {
		t.RawSetString("can_attack", lua.LFalse)
	}
	if ctx.CanMove {
		t.RawSetString("can_move", lua.LTrue)
	} else {
		t.RawSetString("can_move", lua.LFalse)
	}

	t.RawSetString("wander_dist", lua.LNumber(ctx.WanderDist))
	t.RawSetString("spawn_dist", lua.LNumber(ctx.SpawnDist))

	// Build skills array
	skillsTbl := e.vm.NewTable()
	for i, sk := range ctx.Skills {
		row := e.vm.NewTable()
		row.RawSetString("skill_id", lua.LNumber(sk.SkillID))
		row.RawSetString("mp_consume", lua.LNumber(sk.MpConsume))
		row.RawSetString("trigger_random", lua.LNumber(sk.TriggerRandom))
		row.RawSetString("trigger_hp", lua.LNumber(sk.TriggerHP))
		row.RawSetString("trigger_range", lua.LNumber(sk.TriggerRange))
		row.RawSetString("act_id", lua.LNumber(sk.ActID))
		row.RawSetString("gfx_id", lua.LNumber(sk.GfxID))
		skillsTbl.RawSetInt(i+1, row)
	}
	t.RawSetString("skills", skillsTbl)

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua npc_ai error", zap.Error(err), zap.Int("npc_id", ctx.NpcID))
		return nil
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return nil
	}

	// Parse commands array
	var cmds []AICommand
	rt.ForEach(func(_, v lua.LValue) {
		if row, ok := v.(*lua.LTable); ok {
			cmds = append(cmds, AICommand{
				Type:    lStr(row, "type"),
				SkillID: lInt(row, "skill_id"),
				ActID:   lInt(row, "act_id"),
				GfxID:   lInt(row, "gfx_id"),
				Dir:     lInt(row, "dir"),
			})
		}
	})
	return cmds
}

// CalcNpcMelee calls Lua calc_npc_melee(ctx) for NPC melee attack damage.
func (e *Engine) CalcNpcMelee(ctx CombatContext) CombatResult {
	fn := e.vm.GetGlobal("calc_npc_melee")
	if fn == lua.LNil {
		return CombatResult{IsHit: true, Damage: 1}
	}

	t := e.vm.NewTable()

	atk := e.vm.NewTable()
	atk.RawSetString("level", lua.LNumber(ctx.AttackerLevel))
	atk.RawSetString("str", lua.LNumber(ctx.AttackerSTR))
	atk.RawSetString("dex", lua.LNumber(ctx.AttackerDEX))
	atk.RawSetString("weapon_dmg", lua.LNumber(ctx.AttackerWeapon))
	atk.RawSetString("hit_mod", lua.LNumber(ctx.AttackerHitMod))
	atk.RawSetString("dmg_mod", lua.LNumber(ctx.AttackerDmgMod))
	t.RawSetString("attacker", atk)

	tgt := e.vm.NewTable()
	tgt.RawSetString("ac", lua.LNumber(ctx.TargetAC))
	tgt.RawSetString("level", lua.LNumber(ctx.TargetLevel))
	tgt.RawSetString("mr", lua.LNumber(ctx.TargetMR))
	t.RawSetString("target", tgt)

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_npc_melee error", zap.Error(err))
		return CombatResult{IsHit: true, Damage: 1}
	}

	res := e.vm.Get(-1)
	e.vm.Pop(1)

	rt2, ok := res.(*lua.LTable)
	if !ok {
		return CombatResult{IsHit: true, Damage: 1}
	}

	return CombatResult{
		IsHit:  rt2.RawGetString("is_hit") == lua.LTrue,
		Damage: int(lua.LVAsNumber(rt2.RawGetString("damage"))),
	}
}

// CalcNpcRanged calls Lua calc_npc_ranged(ctx) for NPC ranged attack damage.
func (e *Engine) CalcNpcRanged(ctx CombatContext) CombatResult {
	fn := e.vm.GetGlobal("calc_npc_ranged")
	if fn == lua.LNil {
		return CombatResult{IsHit: true, Damage: 1}
	}

	t := e.vm.NewTable()

	atk := e.vm.NewTable()
	atk.RawSetString("level", lua.LNumber(ctx.AttackerLevel))
	atk.RawSetString("str", lua.LNumber(ctx.AttackerSTR))
	atk.RawSetString("dex", lua.LNumber(ctx.AttackerDEX))
	atk.RawSetString("weapon_dmg", lua.LNumber(ctx.AttackerWeapon))
	atk.RawSetString("hit_mod", lua.LNumber(ctx.AttackerHitMod))
	atk.RawSetString("dmg_mod", lua.LNumber(ctx.AttackerDmgMod))
	t.RawSetString("attacker", atk)

	tgt := e.vm.NewTable()
	tgt.RawSetString("ac", lua.LNumber(ctx.TargetAC))
	tgt.RawSetString("level", lua.LNumber(ctx.TargetLevel))
	tgt.RawSetString("mr", lua.LNumber(ctx.TargetMR))
	t.RawSetString("target", tgt)

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_npc_ranged error", zap.Error(err))
		return CombatResult{IsHit: true, Damage: 1}
	}

	res := e.vm.Get(-1)
	e.vm.Pop(1)

	rt2, ok := res.(*lua.LTable)
	if !ok {
		return CombatResult{IsHit: true, Damage: 1}
	}

	return CombatResult{
		IsHit:  rt2.RawGetString("is_hit") == lua.LTrue,
		Damage: int(lua.LVAsNumber(rt2.RawGetString("damage"))),
	}
}

// --- PK System Bridge ---

// PKLawfulResult holds the calculated new lawful value after a PK kill.
type PKLawfulResult struct {
	NewLawful int32
}

// CalcPKLawfulPenalty calls Lua calc_pk_lawful_penalty(ctx).
func (e *Engine) CalcPKLawfulPenalty(killerLevel int, killerLawful int32) PKLawfulResult {
	fn := e.vm.GetGlobal("calc_pk_lawful_penalty")
	if fn == lua.LNil {
		e.log.Error("lua function calc_pk_lawful_penalty not found")
		return PKLawfulResult{NewLawful: killerLawful - 1000}
	}

	t := e.vm.NewTable()
	t.RawSetString("killer_level", lua.LNumber(killerLevel))
	t.RawSetString("killer_lawful", lua.LNumber(killerLawful))

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_pk_lawful_penalty error", zap.Error(err))
		return PKLawfulResult{NewLawful: killerLawful - 1000}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return PKLawfulResult{NewLawful: killerLawful - 1000}
	}

	return PKLawfulResult{
		NewLawful: int32(lua.LVAsNumber(rt.RawGetString("new_lawful"))),
	}
}

// PKItemDropResult holds the item drop decision from Lua.
type PKItemDropResult struct {
	ShouldDrop bool
	Count      int
}

// CalcPKItemDrop calls Lua calc_pk_item_drop(ctx).
func (e *Engine) CalcPKItemDrop(victimLawful int32) PKItemDropResult {
	fn := e.vm.GetGlobal("calc_pk_item_drop")
	if fn == lua.LNil {
		return PKItemDropResult{}
	}

	t := e.vm.NewTable()
	t.RawSetString("victim_lawful", lua.LNumber(victimLawful))

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_pk_item_drop error", zap.Error(err))
		return PKItemDropResult{}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return PKItemDropResult{}
	}

	return PKItemDropResult{
		ShouldDrop: rt.RawGetString("should_drop") == lua.LTrue,
		Count:      lInt(rt, "count"),
	}
}

// PKTimers holds PK-related timer durations from Lua.
type PKTimers struct {
	PinkNameTicks int
	WantedTicks   int
}

// GetPKTimers calls Lua get_pk_timers().
func (e *Engine) GetPKTimers() PKTimers {
	fn := e.vm.GetGlobal("get_pk_timers")
	if fn == lua.LNil {
		return PKTimers{PinkNameTicks: 900, WantedTicks: 432000}
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}); err != nil {
		e.log.Error("lua get_pk_timers error", zap.Error(err))
		return PKTimers{PinkNameTicks: 900, WantedTicks: 432000}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return PKTimers{PinkNameTicks: 900, WantedTicks: 432000}
	}

	return PKTimers{
		PinkNameTicks: int(lua.LVAsNumber(rt.RawGetString("pink_name_ticks"))),
		WantedTicks:   int(lua.LVAsNumber(rt.RawGetString("wanted_ticks"))),
	}
}

// PKThresholds holds PK count thresholds from Lua.
type PKThresholds struct {
	Warning int32 // matches PKCount type (int32)
	Punish  int32
}

// GetPKThresholds calls Lua get_pk_thresholds().
func (e *Engine) GetPKThresholds() PKThresholds {
	fn := e.vm.GetGlobal("get_pk_thresholds")
	if fn == lua.LNil {
		return PKThresholds{Warning: 5, Punish: 10}
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}); err != nil {
		e.log.Error("lua get_pk_thresholds error", zap.Error(err))
		return PKThresholds{Warning: 5, Punish: 10}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return PKThresholds{Warning: 5, Punish: 10}
	}

	return PKThresholds{
		Warning: int32(lua.LVAsNumber(rt.RawGetString("warning"))),
		Punish:  int32(lua.LVAsNumber(rt.RawGetString("punish"))),
	}
}

// --- Durability Bridge ---

// DurabilityContext holds data for weapon durability damage calculation.
type DurabilityContext struct {
	EnchantLvl        int
	Bless             int
	CurrentDurability int
}

// DurabilityResult holds the result of durability damage calculation.
type DurabilityResult struct {
	ShouldDamage  bool
	MaxDurability int
}

// CalcDurabilityDamage calls Lua calc_durability_damage(ctx).
func (e *Engine) CalcDurabilityDamage(ctx DurabilityContext) DurabilityResult {
	fn := e.vm.GetGlobal("calc_durability_damage")
	if fn == lua.LNil {
		return DurabilityResult{ShouldDamage: false, MaxDurability: ctx.EnchantLvl + 5}
	}

	t := e.vm.NewTable()
	t.RawSetString("enchant_lvl", lua.LNumber(ctx.EnchantLvl))
	t.RawSetString("bless", lua.LNumber(ctx.Bless))
	t.RawSetString("current_durability", lua.LNumber(ctx.CurrentDurability))

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_durability_damage error", zap.Error(err))
		return DurabilityResult{ShouldDamage: false, MaxDurability: ctx.EnchantLvl + 5}
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return DurabilityResult{ShouldDamage: false, MaxDurability: ctx.EnchantLvl + 5}
	}

	return DurabilityResult{
		ShouldDamage:  rt.RawGetString("should_damage") == lua.LTrue,
		MaxDurability: lInt(rt, "max_durability"),
	}
}

// --- Regen Bridge ---

// GetHPRegenInterval calls Lua get_hp_regen_interval(level, class_type).
// Returns seconds between HP regen events.
func (e *Engine) GetHPRegenInterval(level, classType int) int {
	return e.callIntFunc("get_hp_regen_interval", level, classType)
}

// HPRegenContext holds data for HP regen calculation.
type HPRegenContext struct {
	Level               int
	Con                 int
	HPR                 int
	Food                int
	WeightPct           int
	HasExoticVitalize   bool
	HasAdditionalFire   bool
}

// CalcHPRegenAmount calls Lua calc_hp_regen_amount(ctx).
func (e *Engine) CalcHPRegenAmount(ctx HPRegenContext) int {
	fn := e.vm.GetGlobal("calc_hp_regen_amount")
	if fn == lua.LNil {
		return 1
	}

	t := e.vm.NewTable()
	t.RawSetString("level", lua.LNumber(ctx.Level))
	t.RawSetString("con", lua.LNumber(ctx.Con))
	t.RawSetString("hpr", lua.LNumber(ctx.HPR))
	t.RawSetString("food", lua.LNumber(ctx.Food))
	t.RawSetString("weight_pct", lua.LNumber(ctx.WeightPct))
	if ctx.HasExoticVitalize {
		t.RawSetString("has_exotic_vitalize", lua.LTrue)
	} else {
		t.RawSetString("has_exotic_vitalize", lua.LFalse)
	}
	if ctx.HasAdditionalFire {
		t.RawSetString("has_additional_fire", lua.LTrue)
	} else {
		t.RawSetString("has_additional_fire", lua.LFalse)
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_hp_regen_amount error", zap.Error(err))
		return 1
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return 1
	}

	return lInt(rt, "amount")
}

// MPRegenContext holds data for MP regen calculation.
type MPRegenContext struct {
	Wis                 int
	MPR                 int
	Food                int
	WeightPct           int
	HasExoticVitalize   bool
	HasAdditionalFire   bool
	HasBluePotion       bool
}

// CalcMPRegenAmount calls Lua calc_mp_regen_amount(ctx).
func (e *Engine) CalcMPRegenAmount(ctx MPRegenContext) int {
	fn := e.vm.GetGlobal("calc_mp_regen_amount")
	if fn == lua.LNil {
		return 1
	}

	t := e.vm.NewTable()
	t.RawSetString("wis", lua.LNumber(ctx.Wis))
	t.RawSetString("mpr", lua.LNumber(ctx.MPR))
	t.RawSetString("food", lua.LNumber(ctx.Food))
	t.RawSetString("weight_pct", lua.LNumber(ctx.WeightPct))
	if ctx.HasExoticVitalize {
		t.RawSetString("has_exotic_vitalize", lua.LTrue)
	} else {
		t.RawSetString("has_exotic_vitalize", lua.LFalse)
	}
	if ctx.HasAdditionalFire {
		t.RawSetString("has_additional_fire", lua.LTrue)
	} else {
		t.RawSetString("has_additional_fire", lua.LFalse)
	}
	if ctx.HasBluePotion {
		t.RawSetString("has_blue_potion", lua.LTrue)
	} else {
		t.RawSetString("has_blue_potion", lua.LFalse)
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, t); err != nil {
		e.log.Error("lua calc_mp_regen_amount error", zap.Error(err))
		return 1
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)

	rt, ok := result.(*lua.LTable)
	if !ok {
		return 1
	}

	return lInt(rt, "amount")
}

// --- Lua helpers ---

// lInt reads an integer field from a Lua table.
func lInt(t *lua.LTable, key string) int {
	return int(lua.LVAsNumber(t.RawGetString(key)))
}

// lStr reads a string field from a Lua table.
func lStr(t *lua.LTable, key string) string {
	return lua.LVAsString(t.RawGetString(key))
}

// callIntFunc calls a Lua function with int args and returns an int result.
func (e *Engine) callIntFunc(name string, args ...int) int {
	fn := e.vm.GetGlobal(name)
	if fn == lua.LNil {
		e.log.Error("lua function not found", zap.String("name", name))
		return 0
	}

	lArgs := make([]lua.LValue, len(args))
	for i, a := range args {
		lArgs[i] = lua.LNumber(a)
	}

	if err := e.vm.CallByParam(lua.P{
		Fn:      fn,
		NRet:    1,
		Protect: true,
	}, lArgs...); err != nil {
		e.log.Error("lua call error", zap.String("func", name), zap.Error(err))
		return 0
	}

	result := e.vm.Get(-1)
	e.vm.Pop(1)
	return int(lua.LVAsNumber(result))
}

// Close shuts down the Lua VM.
func (e *Engine) Close() {
	e.vm.Close()
}
