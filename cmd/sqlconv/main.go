// sqlconv converts L1JTW MySQL SQL dump files to Whale YAML format.
//
// Usage:
//
//	go run ./cmd/sqlconv <command> [-sqldir path] [-outdir path]
//
// Commands: npc, spawn, drop, shop, mapids, skills, items, mobskill, all
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// YAML output structs
// ---------------------------------------------------------------------------

// --- NPC ---
type npcListYAML struct {
	Npcs []npcEntryYAML `yaml:"npcs"`
}
type npcEntryYAML struct {
	NpcID        int32  `yaml:"npc_id"`
	Name         string `yaml:"name"`
	NameID       string `yaml:"nameid"`
	Impl         string `yaml:"impl"`
	GfxID        int32  `yaml:"gfx_id"`
	Level        int16  `yaml:"level"`
	HP           int32  `yaml:"hp"`
	MP           int32  `yaml:"mp"`
	AC           int16  `yaml:"ac"`
	STR          int16  `yaml:"str"`
	DEX          int16  `yaml:"dex"`
	CON          int16  `yaml:"con"`
	WIS          int16  `yaml:"wis"`
	Intel        int16  `yaml:"intel"`
	MR           int16  `yaml:"mr"`
	Exp          int32  `yaml:"exp"`
	Lawful       int32  `yaml:"lawful"`
	Size         string `yaml:"size"`
	Ranged       int16  `yaml:"ranged"`
	AtkSpeed     int16  `yaml:"atk_speed"`
	PassiveSpeed int16  `yaml:"passive_speed"`
	Undead       bool   `yaml:"undead"`
	Agro         bool   `yaml:"agro"`
	Tameable     bool   `yaml:"tameable"`
}

// --- Spawn ---
type spawnListYAML struct {
	Spawns []spawnEntryYAML `yaml:"spawns"`
}
type spawnEntryYAML struct {
	NpcID        int32 `yaml:"npc_id"`
	MapID        int16 `yaml:"map_id"`
	X            int32 `yaml:"x"`
	Y            int32 `yaml:"y"`
	Count        int   `yaml:"count"`
	RandomX      int32 `yaml:"randomx"`
	RandomY      int32 `yaml:"randomy"`
	Heading      int16 `yaml:"heading"`
	RespawnDelay int   `yaml:"respawn_delay"`
}

// --- Drop ---
type dropListYAML struct {
	Drops []mobDropYAML `yaml:"drops"`
}
type mobDropYAML struct {
	MobID int32          `yaml:"mob_id"`
	Items []dropItemYAML `yaml:"items"`
}
type dropItemYAML struct {
	ItemID       int32 `yaml:"item_id"`
	Min          int   `yaml:"min"`
	Max          int   `yaml:"max"`
	Chance       int   `yaml:"chance"`
	EnchantLevel int   `yaml:"enchant_level"`
}

// --- Shop ---
type shopListYAML struct {
	Shops []npcShopYAML `yaml:"shops"`
}
type npcShopYAML struct {
	NpcID int32          `yaml:"npc_id"`
	Items []shopItemYAML `yaml:"items"`
}
type shopItemYAML struct {
	ItemID          int32 `yaml:"item_id"`
	Order           int   `yaml:"order"`
	SellingPrice    int   `yaml:"selling_price"`
	PackCount       int   `yaml:"pack_count"`
	PurchasingPrice int   `yaml:"purchasing_price"`
}

// --- MapIDs ---
type mapListYAML struct {
	Maps []mapEntryYAML `yaml:"maps"`
}
type mapEntryYAML struct {
	MapID         int32   `yaml:"map_id"`
	Name          string  `yaml:"name"`
	StartX        int32   `yaml:"start_x"`
	EndX          int32   `yaml:"end_x"`
	StartY        int32   `yaml:"start_y"`
	EndY          int32   `yaml:"end_y"`
	MonsterAmount float64 `yaml:"monster_amount"`
	DropRate      float64 `yaml:"drop_rate"`
	Underwater    bool    `yaml:"underwater"`
	Markable      bool    `yaml:"markable"`
	Teleportable  bool    `yaml:"teleportable"`
	Escapable     bool    `yaml:"escapable"`
	Resurrection  bool    `yaml:"resurrection"`
	Painwand      bool    `yaml:"painwand"`
	Penalty       bool    `yaml:"penalty"`
	TakePets      bool    `yaml:"take_pets"`
	RecallPets    bool    `yaml:"recall_pets"`
	UsableItem    bool    `yaml:"usable_item"`
	UsableSkill   bool    `yaml:"usable_skill"`
}

// --- Skills ---
type skillListYAML struct {
	Skills []skillEntryYAML `yaml:"skills"`
}
type skillEntryYAML struct {
	SkillID          int32  `yaml:"skill_id"`
	Name             string `yaml:"name"`
	SkillLevel       int    `yaml:"skill_level"`
	SkillNumber      int    `yaml:"skill_number"`
	MpConsume        int    `yaml:"mp_consume"`
	HpConsume        int    `yaml:"hp_consume"`
	ItemConsumeID    int32  `yaml:"item_consume_id"`
	ItemConsumeCount int    `yaml:"item_consume_count"`
	ReuseDelay       int    `yaml:"reuse_delay"`
	BuffDuration     int    `yaml:"buff_duration"`
	Target           string `yaml:"target"`
	TargetTo         int    `yaml:"target_to"`
	DamageValue      int    `yaml:"damage_value"`
	DamageDice       int    `yaml:"damage_dice"`
	DamageDiceCount  int    `yaml:"damage_dice_count"`
	ProbabilityValue int    `yaml:"probability_value"`
	ProbabilityDice  int    `yaml:"probability_dice"`
	Attr             int    `yaml:"attr"`
	Type             int    `yaml:"type"`
	Lawful           int    `yaml:"lawful"`
	Ranged           int    `yaml:"ranged"`
	Area             int    `yaml:"area"`
	Through          int    `yaml:"through"`
	ID               int32  `yaml:"id"`
	NameID           string `yaml:"name_id"`
	ActionID         int    `yaml:"action_id"`
	CastGfx          int32  `yaml:"cast_gfx"`
	CastGfx2         int32  `yaml:"cast_gfx2"`
	SysMsgHappen     int32  `yaml:"sys_msg_happen"`
	SysMsgStop       int32  `yaml:"sys_msg_stop"`
	SysMsgFail       int32  `yaml:"sys_msg_fail"`
}

// --- Weapon ---
type weaponListYAML struct {
	Weapons []weaponEntryYAML `yaml:"weapons"`
}
type weaponEntryYAML struct {
	ItemID           int32  `yaml:"item_id"`
	Name             string `yaml:"name"`
	Type             string `yaml:"type"`
	Material         string `yaml:"material"`
	Weight           int32  `yaml:"weight"`
	InvGfx           int32  `yaml:"inv_gfx"`
	GrdGfx           int32  `yaml:"grd_gfx"`
	ItemDescID       int32  `yaml:"itemdesc_id"`
	DmgSmall         int    `yaml:"dmg_small"`
	DmgLarge         int    `yaml:"dmg_large"`
	Range            int    `yaml:"range"`
	SafeEnchant      int    `yaml:"safe_enchant"`
	UseRoyal         bool   `yaml:"use_royal"`
	UseKnight        bool   `yaml:"use_knight"`
	UseMage          bool   `yaml:"use_mage"`
	UseElf           bool   `yaml:"use_elf"`
	UseDarkElf       bool   `yaml:"use_darkelf"`
	UseDragonKnight  bool   `yaml:"use_dragonknight"`
	UseIllusionist   bool   `yaml:"use_illusionist"`
	HitModifier      int    `yaml:"hit_modifier"`
	DmgModifier      int    `yaml:"dmg_modifier"`
	AddStr           int    `yaml:"add_str"`
	AddCon           int    `yaml:"add_con"`
	AddDex           int    `yaml:"add_dex"`
	AddInt           int    `yaml:"add_int"`
	AddWis           int    `yaml:"add_wis"`
	AddCha           int    `yaml:"add_cha"`
	AddHP            int    `yaml:"add_hp"`
	AddMP            int    `yaml:"add_mp"`
	AddHPR           int    `yaml:"add_hpr"`
	AddMPR           int    `yaml:"add_mpr"`
	AddSP            int    `yaml:"add_sp"`
	MDef             int    `yaml:"m_def"`
	HasteItem        int    `yaml:"haste_item"`
	DoubleDmgChance  int    `yaml:"double_dmg_chance"`
	MagicDmgModifier int    `yaml:"magic_dmg_modifier"`
	CanBeDmg         int    `yaml:"can_be_dmg"`
	MinLevel         int    `yaml:"min_level"`
	MaxLevel         int    `yaml:"max_level"`
	Bless            int    `yaml:"bless"`
	Tradeable        bool   `yaml:"tradeable"`
	CantDelete       bool   `yaml:"cant_delete"`
	MaxUseTime       int    `yaml:"max_use_time"`
}

// --- Armor ---
type armorListYAML struct {
	Armors []armorEntryYAML `yaml:"armors"`
}
type armorEntryYAML struct {
	ItemID          int32  `yaml:"item_id"`
	Name            string `yaml:"name"`
	Type            string `yaml:"type"`
	Material        string `yaml:"material"`
	Weight          int32  `yaml:"weight"`
	InvGfx          int32  `yaml:"inv_gfx"`
	GrdGfx          int32  `yaml:"grd_gfx"`
	ItemDescID      int32  `yaml:"itemdesc_id"`
	AC              int    `yaml:"ac"`
	SafeEnchant     int    `yaml:"safe_enchant"`
	UseRoyal        bool   `yaml:"use_royal"`
	UseKnight       bool   `yaml:"use_knight"`
	UseMage         bool   `yaml:"use_mage"`
	UseElf          bool   `yaml:"use_elf"`
	UseDarkElf      bool   `yaml:"use_darkelf"`
	UseDragonKnight bool   `yaml:"use_dragonknight"`
	UseIllusionist  bool   `yaml:"use_illusionist"`
	AddStr          int    `yaml:"add_str"`
	AddCon          int    `yaml:"add_con"`
	AddDex          int    `yaml:"add_dex"`
	AddInt          int    `yaml:"add_int"`
	AddWis          int    `yaml:"add_wis"`
	AddCha          int    `yaml:"add_cha"`
	AddHP           int    `yaml:"add_hp"`
	AddMP           int    `yaml:"add_mp"`
	AddHPR          int    `yaml:"add_hpr"`
	AddMPR          int    `yaml:"add_mpr"`
	AddSP           int    `yaml:"add_sp"`
	MinLevel        int    `yaml:"min_level"`
	MaxLevel        int    `yaml:"max_level"`
	MDef            int    `yaml:"m_def"`
	HasteItem       int    `yaml:"haste_item"`
	DamageReduction int    `yaml:"damage_reduction"`
	WeightReduction int    `yaml:"weight_reduction"`
	HitModifier     int    `yaml:"hit_modifier"`
	DmgModifier     int    `yaml:"dmg_modifier"`
	BowHitModifier  int    `yaml:"bow_hit_modifier"`
	BowDmgModifier  int    `yaml:"bow_dmg_modifier"`
	Bless           int    `yaml:"bless"`
	Tradeable       bool   `yaml:"tradeable"`
	CantDelete      bool   `yaml:"cant_delete"`
	MaxUseTime      int    `yaml:"max_use_time"`
	DefenseWater    int    `yaml:"defense_water"`
	DefenseWind     int    `yaml:"defense_wind"`
	DefenseFire     int    `yaml:"defense_fire"`
	DefenseEarth    int    `yaml:"defense_earth"`
	RegistStun      int    `yaml:"regist_stun"`
	RegistStone     int    `yaml:"regist_stone"`
	RegistSleep     int    `yaml:"regist_sleep"`
	RegistFreeze    int    `yaml:"regist_freeze"`
	RegistSustain   int    `yaml:"regist_sustain"`
	RegistBlind     int    `yaml:"regist_blind"`
	Grade           int    `yaml:"grade"`
}

// --- EtcItem ---
type etcItemListYAML struct {
	Items []etcItemEntryYAML `yaml:"items"`
}
type etcItemEntryYAML struct {
	ItemID         int32  `yaml:"item_id"`
	Name           string `yaml:"name"`
	ItemType       string `yaml:"item_type"`
	UseType        string `yaml:"use_type"`
	Material       string `yaml:"material"`
	Weight         int32  `yaml:"weight"`
	InvGfx         int32  `yaml:"inv_gfx"`
	GrdGfx         int32  `yaml:"grd_gfx"`
	ItemDescID     int32  `yaml:"itemdesc_id"`
	Stackable      bool   `yaml:"stackable"`
	MaxChargeCount int    `yaml:"max_charge_count"`
	DmgSmall       int    `yaml:"dmg_small"`
	DmgLarge       int    `yaml:"dmg_large"`
	MinLevel       int    `yaml:"min_level"`
	MaxLevel       int    `yaml:"max_level"`
	LocX           int32  `yaml:"loc_x"`
	LocY           int32  `yaml:"loc_y"`
	MapID          int32  `yaml:"map_id"`
	Bless          int    `yaml:"bless"`
	Tradeable      bool   `yaml:"tradeable"`
	CantDelete     bool   `yaml:"cant_delete"`
	CanSeal        bool   `yaml:"can_seal"`
	DelayID        int    `yaml:"delay_id"`
	DelayTime      int    `yaml:"delay_time"`
	DelayEffect    int    `yaml:"delay_effect"`
	FoodVolume     int    `yaml:"food_volume"`
	SaveAtOnce     bool   `yaml:"save_at_once"`
}

// --- MobSkill ---
type mobSkillListYAML struct {
	MobSkills []mobSkillGroupYAML `yaml:"mob_skills"`
}
type mobSkillGroupYAML struct {
	MobID  int32               `yaml:"mob_id"`
	Skills []mobSkillEntryYAML `yaml:"skills"`
}
type mobSkillEntryYAML struct {
	ActNo              int    `yaml:"act_no"`
	Name               string `yaml:"name"`
	Type               int    `yaml:"type"`
	MpConsume          int    `yaml:"mp_consume"`
	TriggerRandom      int    `yaml:"trigger_random"`
	TriggerHP          int    `yaml:"trigger_hp"`
	TriggerCompanionHP int    `yaml:"trigger_companion_hp"`
	TriggerRange       int    `yaml:"trigger_range"`
	TriggerCount       int    `yaml:"trigger_count"`
	ChangeTarget       int    `yaml:"change_target"`
	Range              int    `yaml:"range"`
	AreaWidth          int    `yaml:"area_width"`
	AreaHeight         int    `yaml:"area_height"`
	Leverage           int    `yaml:"leverage"`
	SkillID            int32  `yaml:"skill_id"`
	SkillArea          int    `yaml:"skill_area"`
	GfxID              int32  `yaml:"gfx_id"`
	ActID              int    `yaml:"act_id"`
	SummonID           int32  `yaml:"summon_id"`
	SummonMin          int    `yaml:"summon_min"`
	SummonMax          int    `yaml:"summon_max"`
	PolyID             int32  `yaml:"poly_id"`
}

// --- NPC Action (dialog) ---
type npcActionListYAML struct {
	Actions []npcActionEntryYAML `yaml:"actions"`
}
type npcActionEntryYAML struct {
	NpcID        int32  `yaml:"npc_id"`
	NormalAction string `yaml:"normal_action"`
	CaoticAction string `yaml:"caotic_action"`
	TeleportURL  string `yaml:"teleport_url,omitempty"`
	TeleportURLA string `yaml:"teleport_urla,omitempty"`
}

// ---------------------------------------------------------------------------
// SQL parsing helpers
// ---------------------------------------------------------------------------

// parseValues extracts column values from a single INSERT INTO ... VALUES (...) line.
func parseValues(line string) []string {
	upper := strings.ToUpper(line)
	idx := strings.Index(upper, "VALUES")
	if idx == -1 {
		return nil
	}
	rest := line[idx+6:]
	start := strings.IndexByte(rest, '(')
	if start == -1 {
		return nil
	}
	end := strings.LastIndexByte(rest, ')')
	if end == -1 || end <= start {
		return nil
	}
	inner := rest[start+1 : end]

	var values []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if inQuote {
			if ch == '\'' {
				if i+1 < len(inner) && inner[i+1] == '\'' {
					cur.WriteByte('\'')
					i++
				} else {
					inQuote = false
				}
			} else {
				cur.WriteByte(ch)
			}
		} else {
			switch ch {
			case '\'':
				inQuote = true
			case ',':
				values = append(values, strings.TrimSpace(cur.String()))
				cur.Reset()
			default:
				cur.WriteByte(ch)
			}
		}
	}
	values = append(values, strings.TrimSpace(cur.String()))

	for i, v := range values {
		if strings.EqualFold(v, "null") {
			values[i] = ""
		}
	}
	return values
}

// parseAllInserts reads a SQL file and returns all parsed INSERT rows.
func parseAllInserts(path string) ([][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var rows [][]string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "INSERT INTO") {
			continue
		}
		vals := parseValues(line)
		if vals != nil {
			rows = append(rows, vals)
		}
	}
	return rows, nil
}

func parseInt(s string) int {
	if s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

func parseInt32(s string) int32 { return int32(parseInt(s)) }
func parseInt16(s string) int16 { return int16(parseInt(s)) }

func parseFloat64(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseBool01(s string) bool { return s != "" && s != "0" }

// ---------------------------------------------------------------------------
// YAML writer
// ---------------------------------------------------------------------------

func writeYAML(path string, data interface{}, comment string) error {
	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	if comment != "" {
		fmt.Fprintln(f, comment)
		fmt.Fprintln(f)
	}
	_, err = f.Write(out)
	return err
}

// ---------------------------------------------------------------------------
// Converters
// ---------------------------------------------------------------------------

func convertNpc(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "npc.sql"))
	if err != nil {
		return err
	}
	var npcs []npcEntryYAML
	for _, r := range rows {
		if len(r) < 31 {
			continue
		}
		npcs = append(npcs, npcEntryYAML{
			NpcID:        parseInt32(r[0]),
			Name:         r[1],
			NameID:       r[2],
			Impl:         r[4],
			GfxID:        parseInt32(r[5]),
			Level:        parseInt16(r[6]),
			HP:           parseInt32(r[7]),
			MP:           parseInt32(r[8]),
			AC:           parseInt16(r[9]),
			STR:          parseInt16(r[10]),
			DEX:          parseInt16(r[12]), // dex is column 12
			CON:          parseInt16(r[11]), // con is column 11
			WIS:          parseInt16(r[13]),
			Intel:        parseInt16(r[14]),
			MR:           parseInt16(r[15]),
			Exp:          parseInt32(r[16]),
			Lawful:       parseInt32(r[17]),
			Size:         r[18],
			Ranged:       parseInt16(r[20]),
			AtkSpeed:     parseInt16(r[23]),
			PassiveSpeed: parseInt16(r[22]),
			Undead:       parseBool01(r[27]),
			Agro:         parseBool01(r[30]),
			Tameable:     parseBool01(r[21]),
		})
	}
	sort.Slice(npcs, func(i, j int) bool { return npcs[i].NpcID < npcs[j].NpcID })
	fmt.Printf("  npc: %d entries (from %d total rows)\n", len(npcs), len(rows))
	return writeYAML(filepath.Join(outDir, "npc_list.yaml"),
		npcListYAML{Npcs: npcs},
		"# NPC templates - converted from L1JTW npc.sql (all types)")
}

func convertSpawn(sqlDir, outDir string) error {
	// --- Monster spawns from spawnlist.sql ---
	rows, err := parseAllInserts(filepath.Join(sqlDir, "spawnlist.sql"))
	if err != nil {
		return err
	}
	var spawns []spawnEntryYAML
	for _, r := range rows {
		if len(r) < 17 {
			continue
		}
		count := parseInt(r[2])
		if count == 0 {
			continue
		}
		minDelay := parseInt(r[14])
		maxDelay := parseInt(r[15])
		delay := maxDelay
		if minDelay > maxDelay {
			delay = minDelay
		}
		spawns = append(spawns, spawnEntryYAML{
			NpcID:        parseInt32(r[3]),
			MapID:        parseInt16(r[16]),
			X:            parseInt32(r[5]),
			Y:            parseInt32(r[6]),
			Count:        count,
			RandomX:      parseInt32(r[7]),
			RandomY:      parseInt32(r[8]),
			Heading:      parseInt16(r[13]),
			RespawnDelay: delay,
		})
	}
	monsterCount := len(spawns)
	fmt.Printf("  spawn (monsters): %d entries (from %d rows)\n", monsterCount, len(rows))

	// --- NPC spawns from spawnlist_npc.sql (merchants, guards, etc.) ---
	npcRows, err := parseAllInserts(filepath.Join(sqlDir, "spawnlist_npc.sql"))
	if err != nil {
		fmt.Printf("  spawn (npcs): skipped (file not found)\n")
	} else {
		npcCount := 0
		for _, r := range npcRows {
			if len(r) < 11 {
				continue
			}
			count := parseInt(r[2])
			if count == 0 {
				continue
			}
			spawns = append(spawns, spawnEntryYAML{
				NpcID:        parseInt32(r[3]),  // npc_templateid
				MapID:        parseInt16(r[10]), // mapid
				X:            parseInt32(r[4]),  // locx
				Y:            parseInt32(r[5]),  // locy
				Count:        count,
				RandomX:      parseInt32(r[6]),  // randomx
				RandomY:      parseInt32(r[7]),  // randomy
				Heading:      parseInt16(r[8]),  // heading
				RespawnDelay: parseInt(r[9]),    // respawn_delay
			})
			npcCount++
		}
		fmt.Printf("  spawn (npcs): %d entries (from %d rows)\n", npcCount, len(npcRows))
	}

	sort.Slice(spawns, func(i, j int) bool {
		if spawns[i].MapID != spawns[j].MapID {
			return spawns[i].MapID < spawns[j].MapID
		}
		return spawns[i].NpcID < spawns[j].NpcID
	})
	fmt.Printf("  spawn total: %d entries\n", len(spawns))
	return writeYAML(filepath.Join(outDir, "spawn_list.yaml"),
		spawnListYAML{Spawns: spawns},
		"# NPC spawn list - converted from L1JTW spawnlist.sql + spawnlist_npc.sql")
}

func convertDrop(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "droplist.sql"))
	if err != nil {
		return err
	}
	groups := make(map[int32][]dropItemYAML)
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		mobID := parseInt32(r[0])
		groups[mobID] = append(groups[mobID], dropItemYAML{
			ItemID:       parseInt32(r[1]),
			Min:          parseInt(r[2]),
			Max:          parseInt(r[3]),
			Chance:       parseInt(r[4]),
			EnchantLevel: parseInt(r[5]),
		})
	}
	var mobIDs []int32
	for id := range groups {
		mobIDs = append(mobIDs, id)
	}
	sort.Slice(mobIDs, func(i, j int) bool { return mobIDs[i] < mobIDs[j] })
	var drops []mobDropYAML
	for _, id := range mobIDs {
		drops = append(drops, mobDropYAML{MobID: id, Items: groups[id]})
	}
	fmt.Printf("  drop: %d mobs, %d total items\n", len(drops), len(rows))
	return writeYAML(filepath.Join(outDir, "drop_list.yaml"),
		dropListYAML{Drops: drops},
		"# Monster drop list - converted from L1JTW droplist.sql")
}

func convertShop(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "shop.sql"))
	if err != nil {
		return err
	}
	groups := make(map[int32][]shopItemYAML)
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		npcID := parseInt32(r[0])
		groups[npcID] = append(groups[npcID], shopItemYAML{
			ItemID:          parseInt32(r[1]),
			Order:           parseInt(r[2]),
			SellingPrice:    parseInt(r[3]),
			PackCount:       parseInt(r[4]),
			PurchasingPrice: parseInt(r[5]),
		})
	}
	var npcIDs []int32
	for id := range groups {
		npcIDs = append(npcIDs, id)
	}
	sort.Slice(npcIDs, func(i, j int) bool { return npcIDs[i] < npcIDs[j] })
	var shops []npcShopYAML
	for _, id := range npcIDs {
		shops = append(shops, npcShopYAML{NpcID: id, Items: groups[id]})
	}
	fmt.Printf("  shop: %d NPCs, %d total items\n", len(shops), len(rows))
	return writeYAML(filepath.Join(outDir, "shop_list.yaml"),
		shopListYAML{Shops: shops},
		"# NPC shop list - converted from L1JTW shop.sql")
}

func convertMapIDs(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "mapids.sql"))
	if err != nil {
		return err
	}
	var maps []mapEntryYAML
	for _, r := range rows {
		if len(r) < 19 {
			continue
		}
		maps = append(maps, mapEntryYAML{
			MapID:         parseInt32(r[0]),
			Name:          r[1],
			StartX:        parseInt32(r[2]),
			EndX:          parseInt32(r[3]),
			StartY:        parseInt32(r[4]),
			EndY:          parseInt32(r[5]),
			MonsterAmount: parseFloat64(r[6]),
			DropRate:      parseFloat64(r[7]),
			Underwater:    parseBool01(r[8]),
			Markable:      parseBool01(r[9]),
			Teleportable:  parseBool01(r[10]),
			Escapable:     parseBool01(r[11]),
			Resurrection:  parseBool01(r[12]),
			Painwand:      parseBool01(r[13]),
			Penalty:       parseBool01(r[14]),
			TakePets:      parseBool01(r[15]),
			RecallPets:    parseBool01(r[16]),
			UsableItem:    parseBool01(r[17]),
			UsableSkill:   parseBool01(r[18]),
		})
	}
	sort.Slice(maps, func(i, j int) bool { return maps[i].MapID < maps[j].MapID })
	fmt.Printf("  mapids: %d maps\n", len(maps))
	return writeYAML(filepath.Join(outDir, "map_list.yaml"),
		mapListYAML{Maps: maps},
		"# Map definitions - converted from L1JTW mapids.sql")
}

func convertSkills(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "skills.sql"))
	if err != nil {
		return err
	}
	var skills []skillEntryYAML
	for _, r := range rows {
		if len(r) < 31 {
			continue
		}
		skills = append(skills, skillEntryYAML{
			SkillID:          parseInt32(r[0]),
			Name:             r[1],
			SkillLevel:       parseInt(r[2]),
			SkillNumber:      parseInt(r[3]),
			MpConsume:        parseInt(r[4]),
			HpConsume:        parseInt(r[5]),
			ItemConsumeID:    parseInt32(r[6]),
			ItemConsumeCount: parseInt(r[7]),
			ReuseDelay:       parseInt(r[8]),
			BuffDuration:     parseInt(r[9]),
			Target:           r[10],
			TargetTo:         parseInt(r[11]),
			DamageValue:      parseInt(r[12]),
			DamageDice:       parseInt(r[13]),
			DamageDiceCount:  parseInt(r[14]),
			ProbabilityValue: parseInt(r[15]),
			ProbabilityDice:  parseInt(r[16]),
			Attr:             parseInt(r[17]),
			Type:             parseInt(r[18]),
			Lawful:           parseInt(r[19]),
			Ranged:           parseInt(r[20]),
			Area:             parseInt(r[21]),
			Through:          parseInt(r[22]),
			ID:               parseInt32(r[23]),
			NameID:           r[24],
			ActionID:         parseInt(r[25]),
			CastGfx:          parseInt32(r[26]),
			CastGfx2:         parseInt32(r[27]),
			SysMsgHappen:     parseInt32(r[28]),
			SysMsgStop:       parseInt32(r[29]),
			SysMsgFail:       parseInt32(r[30]),
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].SkillID < skills[j].SkillID })
	fmt.Printf("  skills: %d entries\n", len(skills))
	return writeYAML(filepath.Join(outDir, "skill_list.yaml"),
		skillListYAML{Skills: skills},
		"# Skill definitions - converted from L1JTW skills.sql")
}

func convertWeapons(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "weapon.sql"))
	if err != nil {
		return err
	}
	var weapons []weaponEntryYAML
	for _, r := range rows {
		if len(r) < 45 {
			continue
		}
		weapons = append(weapons, weaponEntryYAML{
			ItemID:           parseInt32(r[0]),
			Name:             r[1],
			Type:             r[4],  // type
			Material:         r[5],  // material
			Weight:           parseInt32(r[6]),
			InvGfx:           parseInt32(r[7]),
			GrdGfx:           parseInt32(r[8]),
			ItemDescID:       parseInt32(r[9]),
			DmgSmall:         parseInt(r[10]),
			DmgLarge:         parseInt(r[11]),
			Range:            parseInt(r[12]),
			SafeEnchant:      parseInt(r[13]),
			UseRoyal:         parseBool01(r[14]),
			UseKnight:        parseBool01(r[15]),
			UseMage:          parseBool01(r[16]),
			UseElf:           parseBool01(r[17]),
			UseDarkElf:       parseBool01(r[18]),
			UseDragonKnight:  parseBool01(r[19]),
			UseIllusionist:   parseBool01(r[20]),
			HitModifier:      parseInt(r[21]),
			DmgModifier:      parseInt(r[22]),
			AddStr:           parseInt(r[23]),
			AddCon:           parseInt(r[24]),
			AddDex:           parseInt(r[25]),
			AddInt:           parseInt(r[26]),
			AddWis:           parseInt(r[27]),
			AddCha:           parseInt(r[28]),
			AddHP:            parseInt(r[29]),
			AddMP:            parseInt(r[30]),
			AddHPR:           parseInt(r[31]),
			AddMPR:           parseInt(r[32]),
			AddSP:            parseInt(r[33]),
			MDef:             parseInt(r[34]),
			HasteItem:        parseInt(r[35]),
			DoubleDmgChance:  parseInt(r[36]),
			MagicDmgModifier: parseInt(r[37]),
			CanBeDmg:         parseInt(r[38]),
			MinLevel:         parseInt(r[39]),
			MaxLevel:         parseInt(r[40]),
			Bless:            parseInt(r[41]),
			Tradeable:        parseBool01(r[42]),
			CantDelete:       parseBool01(r[43]),
			MaxUseTime:       parseInt(r[44]),
		})
	}
	sort.Slice(weapons, func(i, j int) bool { return weapons[i].ItemID < weapons[j].ItemID })
	fmt.Printf("  weapon: %d entries\n", len(weapons))
	return writeYAML(filepath.Join(outDir, "weapon_list.yaml"),
		weaponListYAML{Weapons: weapons},
		"# Weapon definitions - converted from L1JTW weapon.sql")
}

func convertArmors(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "armor.sql"))
	if err != nil {
		return err
	}
	// armor columns (0-indexed):
	// 0:item_id 1:name 2:unid_name 3:id_name 4:type 5:material
	// 6:weight 7:invgfx 8:grdgfx 9:itemdesc_id 10:ac 11:safenchant
	// 12:use_royal 13:use_knight 14:use_mage 15:use_elf 16:use_darkelf
	// 17:use_dragonknight 18:use_illusionist
	// 19:add_str 20:add_con 21:add_dex 22:add_int 23:add_wis 24:add_cha
	// 25:add_hp 26:add_mp 27:add_hpr 28:add_mpr 29:add_sp
	// 30:min_lvl 31:max_lvl 32:m_def 33:haste_item
	// 34:damage_reduction 35:weight_reduction 36:hit_modifier 37:dmg_modifier
	// 38:bow_hit_modifier 39:bow_dmg_modifier
	// 40:bless 41:trade 42:cant_delete 43:max_use_time
	// 44:defense_water 45:defense_wind 46:defense_fire 47:defense_earth
	// 48:regist_stun 49:regist_stone 50:regist_sleep 51:regist_freeze
	// 52:regist_sustain 53:regist_blind 54:grade
	var armors []armorEntryYAML
	for _, r := range rows {
		if len(r) < 55 {
			continue
		}
		armors = append(armors, armorEntryYAML{
			ItemID:          parseInt32(r[0]),
			Name:            r[1],
			Type:            r[4],
			Material:        r[5],
			Weight:          parseInt32(r[6]),
			InvGfx:          parseInt32(r[7]),
			GrdGfx:          parseInt32(r[8]),
			ItemDescID:      parseInt32(r[9]),
			AC:              parseInt(r[10]),
			SafeEnchant:     parseInt(r[11]),
			UseRoyal:        parseBool01(r[12]),
			UseKnight:       parseBool01(r[13]),
			UseMage:         parseBool01(r[14]),
			UseElf:          parseBool01(r[15]),
			UseDarkElf:      parseBool01(r[16]),
			UseDragonKnight: parseBool01(r[17]),
			UseIllusionist:  parseBool01(r[18]),
			AddStr:          parseInt(r[19]),
			AddCon:          parseInt(r[20]),
			AddDex:          parseInt(r[21]),
			AddInt:          parseInt(r[22]),
			AddWis:          parseInt(r[23]),
			AddCha:          parseInt(r[24]),
			AddHP:           parseInt(r[25]),
			AddMP:           parseInt(r[26]),
			AddHPR:          parseInt(r[27]),
			AddMPR:          parseInt(r[28]),
			AddSP:           parseInt(r[29]),
			MinLevel:        parseInt(r[30]),
			MaxLevel:        parseInt(r[31]),
			MDef:            parseInt(r[32]),
			HasteItem:       parseInt(r[33]),
			DamageReduction: parseInt(r[34]),
			WeightReduction: parseInt(r[35]),
			HitModifier:     parseInt(r[36]),
			DmgModifier:     parseInt(r[37]),
			BowHitModifier:  parseInt(r[38]),
			BowDmgModifier:  parseInt(r[39]),
			Bless:           parseInt(r[40]),
			Tradeable:       parseBool01(r[41]),
			CantDelete:      parseBool01(r[42]),
			MaxUseTime:      parseInt(r[43]),
			DefenseWater:    parseInt(r[44]),
			DefenseWind:     parseInt(r[45]),
			DefenseFire:     parseInt(r[46]),
			DefenseEarth:    parseInt(r[47]),
			RegistStun:      parseInt(r[48]),
			RegistStone:     parseInt(r[49]),
			RegistSleep:     parseInt(r[50]),
			RegistFreeze:    parseInt(r[51]),
			RegistSustain:   parseInt(r[52]),
			RegistBlind:     parseInt(r[53]),
			Grade:           parseInt(r[54]),
		})
	}
	sort.Slice(armors, func(i, j int) bool { return armors[i].ItemID < armors[j].ItemID })
	fmt.Printf("  armor: %d entries\n", len(armors))
	return writeYAML(filepath.Join(outDir, "armor_list.yaml"),
		armorListYAML{Armors: armors},
		"# Armor definitions - converted from L1JTW armor.sql")
}

func convertEtcItems(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "etcitem.sql"))
	if err != nil {
		return err
	}
	// etcitem columns (0-indexed):
	// 0:item_id 1:name 2:unid_name 3:id_name 4:item_type 5:use_type
	// 6:material 7:weight 8:invgfx 9:grdgfx 10:itemdesc_id
	// 11:stackable 12:max_charge_count 13:dmg_small 14:dmg_large
	// 15:min_lvl 16:max_lvl 17:locx 18:locy 19:mapid
	// 20:bless 21:trade 22:cant_delete 23:can_seal
	// 24:delay_id 25:delay_time 26:delay_effect 27:food_volume 28:save_at_once
	var items []etcItemEntryYAML
	for _, r := range rows {
		if len(r) < 29 {
			continue
		}
		items = append(items, etcItemEntryYAML{
			ItemID:         parseInt32(r[0]),
			Name:           r[1],
			ItemType:       r[4],
			UseType:        r[5],
			Material:       r[6],
			Weight:         parseInt32(r[7]),
			InvGfx:         parseInt32(r[8]),
			GrdGfx:         parseInt32(r[9]),
			ItemDescID:     parseInt32(r[10]),
			Stackable:      parseBool01(r[11]),
			MaxChargeCount: parseInt(r[12]),
			DmgSmall:       parseInt(r[13]),
			DmgLarge:       parseInt(r[14]),
			MinLevel:       parseInt(r[15]),
			MaxLevel:       parseInt(r[16]),
			LocX:           parseInt32(r[17]),
			LocY:           parseInt32(r[18]),
			MapID:          parseInt32(r[19]),
			Bless:          parseInt(r[20]),
			Tradeable:      parseBool01(r[21]),
			CantDelete:     parseBool01(r[22]),
			CanSeal:        parseBool01(r[23]),
			DelayID:        parseInt(r[24]),
			DelayTime:      parseInt(r[25]),
			DelayEffect:    parseInt(r[26]),
			FoodVolume:     parseInt(r[27]),
			SaveAtOnce:     parseBool01(r[28]),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ItemID < items[j].ItemID })
	fmt.Printf("  etcitem: %d entries\n", len(items))
	return writeYAML(filepath.Join(outDir, "etcitem_list.yaml"),
		etcItemListYAML{Items: items},
		"# EtcItem definitions - converted from L1JTW etcitem.sql")
}

func convertItems(sqlDir, outDir string) error {
	if err := convertWeapons(sqlDir, outDir); err != nil {
		return fmt.Errorf("weapon: %w", err)
	}
	if err := convertArmors(sqlDir, outDir); err != nil {
		return fmt.Errorf("armor: %w", err)
	}
	if err := convertEtcItems(sqlDir, outDir); err != nil {
		return fmt.Errorf("etcitem: %w", err)
	}
	return nil
}

func convertMobSkill(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "mobskill.sql"))
	if err != nil {
		return err
	}
	// mobskill columns (0-indexed):
	// 0:mobid 1:actNo 2:mobname 3:Type 4:mpConsume
	// 5:TriRnd 6:TriHp 7:TriCompanionHp 8:TriRange 9:TriCount
	// 10:ChangeTarget 11:Range 12:AreaWidth 13:AreaHeight 14:Leverage
	// 15:SkillId 16:SkillArea 17:Gfxid 18:ActId
	// 19:SummonId 20:SummonMin 21:SummonMax 22:PolyId
	groups := make(map[int32][]mobSkillEntryYAML)
	for _, r := range rows {
		if len(r) < 23 {
			continue
		}
		mobID := parseInt32(r[0])
		groups[mobID] = append(groups[mobID], mobSkillEntryYAML{
			ActNo:              parseInt(r[1]),
			Name:               r[2],
			Type:               parseInt(r[3]),
			MpConsume:          parseInt(r[4]),
			TriggerRandom:      parseInt(r[5]),
			TriggerHP:          parseInt(r[6]),
			TriggerCompanionHP: parseInt(r[7]),
			TriggerRange:       parseInt(r[8]),
			TriggerCount:       parseInt(r[9]),
			ChangeTarget:       parseInt(r[10]),
			Range:              parseInt(r[11]),
			AreaWidth:          parseInt(r[12]),
			AreaHeight:         parseInt(r[13]),
			Leverage:           parseInt(r[14]),
			SkillID:            parseInt32(r[15]),
			SkillArea:          parseInt(r[16]),
			GfxID:              parseInt32(r[17]),
			ActID:              parseInt(r[18]),
			SummonID:           parseInt32(r[19]),
			SummonMin:          parseInt(r[20]),
			SummonMax:          parseInt(r[21]),
			PolyID:             parseInt32(r[22]),
		})
	}
	var mobIDs []int32
	for id := range groups {
		mobIDs = append(mobIDs, id)
	}
	sort.Slice(mobIDs, func(i, j int) bool { return mobIDs[i] < mobIDs[j] })
	var result []mobSkillGroupYAML
	for _, id := range mobIDs {
		result = append(result, mobSkillGroupYAML{MobID: id, Skills: groups[id]})
	}
	fmt.Printf("  mobskill: %d mobs, %d total skills\n", len(result), len(rows))
	return writeYAML(filepath.Join(outDir, "mob_skill_list.yaml"),
		mobSkillListYAML{MobSkills: result},
		"# Monster skill list - converted from L1JTW mobskill.sql")
}

func convertNpcAction(sqlDir, outDir string) error {
	rows, err := parseAllInserts(filepath.Join(sqlDir, "npcaction.sql"))
	if err != nil {
		return err
	}
	// npcaction columns: 0:npcid 1:normal_action 2:caotic_action 3:teleport_url 4:teleport_urla
	var actions []npcActionEntryYAML
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		actions = append(actions, npcActionEntryYAML{
			NpcID:        parseInt32(r[0]),
			NormalAction: r[1],
			CaoticAction: r[2],
			TeleportURL:  r[3],
			TeleportURLA: r[4],
		})
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].NpcID < actions[j].NpcID })
	fmt.Printf("  npcaction: %d entries\n", len(actions))
	return writeYAML(filepath.Join(outDir, "npc_action_list.yaml"),
		npcActionListYAML{Actions: actions},
		"# NPC dialog actions - converted from L1JTW npcaction.sql")
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func printUsage() {
	fmt.Println("Usage: sqlconv <command> [-sqldir path] [-outdir path]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  npc       Convert npc.sql -> npc_list.yaml")
	fmt.Println("  spawn     Convert spawnlist.sql + spawnlist_npc.sql -> spawn_list.yaml")
	fmt.Println("  drop      Convert droplist.sql -> drop_list.yaml")
	fmt.Println("  shop      Convert shop.sql -> shop_list.yaml")
	fmt.Println("  mapids    Convert mapids.sql -> map_list.yaml")
	fmt.Println("  skills    Convert skills.sql -> skill_list.yaml")
	fmt.Println("  items     Convert weapon/armor/etcitem.sql -> 3 YAML files")
	fmt.Println("  mobskill  Convert mobskill.sql -> mob_skill_list.yaml")
	fmt.Println("  npcaction Convert npcaction.sql -> npc_action_list.yaml")
	fmt.Println("  all       Run all conversions")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		printUsage()
		return
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	sqlDir := fs.String("sqldir", filepath.Join("..", "..", "l1j_java", "db", "Taiwan"), "SQL source directory")
	outDir := fs.String("outdir", filepath.Join("..", "data", "yaml"), "YAML output directory")
	_ = fs.Parse(os.Args[2:])

	converters := map[string]func(string, string) error{
		"npc":       convertNpc,
		"spawn":     convertSpawn,
		"drop":      convertDrop,
		"shop":      convertShop,
		"mapids":    convertMapIDs,
		"skills":    convertSkills,
		"items":     convertItems,
		"mobskill":  convertMobSkill,
		"npcaction": convertNpcAction,
	}

	// Ordered list for "all" (deterministic output)
	allOrder := []string{"npc", "spawn", "drop", "shop", "mapids", "skills", "items", "mobskill", "npcaction"}

	if cmd == "all" {
		fmt.Println("Converting all SQL -> YAML...")
		for _, name := range allOrder {
			if err := converters[name](*sqlDir, *outDir); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR [%s]: %v\n", name, err)
				os.Exit(1)
			}
		}
		fmt.Println("Done!")
		return
	}

	fn, ok := converters[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
	if err := fn(*sqlDir, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Done!")
}
