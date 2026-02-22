package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ItemCategory distinguishes weapon/armor/etcitem for game logic.
type ItemCategory int

const (
	CategoryEtcItem ItemCategory = 0
	CategoryWeapon  ItemCategory = 1
	CategoryArmor   ItemCategory = 2
)

// useTypeMap maps YAML use_type / armor type strings to the integer values
// the 3.80C client expects in inventory packets. Java: ItemTable._useTypes
var useTypeMap = map[string]byte{
	"none":        0, // Java uses -1 but client byte is unsigned; 0 = unusable
	"weapon":      1,
	"armor":       2,
	"spell_long":  5,
	"ntele":       6,
	"identify":    7,
	"res":         8,
	"letter":      12,
	"letter_w":    13,
	"choice":      14,
	"instrument":  15,
	"sosc":        16,
	"spell_short": 17,
	"T":           18,
	"cloak":       19,
	"glove":       20,
	"boots":       21,
	"helm":        22,
	"amulet":      24,
	"shield":      25,
	"guarder":     25,
	"dai":         26,
	"zel":         27,
	"blank":       28,
	"btele":       29,
	"spell_buff":  30,
	"ccard":       31,
	"ccard_w":     32,
	"vcard":       33,
	"vcard_w":     34,
	"wcard":       35,
	"wcard_w":     36,
	"belt":        37,
	"earring":     40,
	"fishing_rod": 42,
	"rune":        43,
	"rune2":       44,
	"rune3":       45,
	"del":         46,
	"normal":      51,
	"ring":        57,
}

// UseTypeToID converts a YAML use_type string to the client integer byte.
func UseTypeToID(s string) byte {
	if v, ok := useTypeMap[s]; ok {
		return v
	}
	return 0
}

// materialMap maps YAML material strings to the integer values
// the 3.80C client expects in item status bytes. Java: ItemTable._materialTypes
var materialMap = map[string]byte{
	"none":         0,
	"liquid":       1,
	"web":          2,
	"vegetation":   3,
	"animite":      4,
	"paper":        5,
	"cloth":        6,
	"leather":      7,
	"wood":         8,
	"bone":         9,
	"dragonscale":  10,
	"iron":         11,
	"steel":        12,
	"copper":       13,
	"silver":       14,
	"gold":         15,
	"platinum":     16,
	"mithril":      17,
	"blackmithril": 18,
	"glass":        19,
	"gemstone":     20,
	"mineral":      21,
	"oriharukon":   22,
}

// MaterialToID converts a YAML material string to the client integer byte.
func MaterialToID(s string) byte {
	if v, ok := materialMap[s]; ok {
		return v
	}
	return 0
}

// ItemInfo holds item template data needed for game logic.
// Flat struct — fields that don't apply to a category are zero-valued.
type ItemInfo struct {
	ItemID   int32
	Name     string
	InvGfx   int32
	GrdGfx   int32
	Weight   int32
	Category ItemCategory
	Type     string // weapon: sword/dagger/bow/… armor: helm/armor/shield/… etc: arrow/potion/…
	Material string

	// Combat stats (weapons)
	DmgSmall int
	DmgLarge int
	Range    int
	HitMod   int
	DmgMod   int

	// Defense (armor)
	AC int

	// Bow modifiers (armor — Java: bowHitModifierByArmor / bowDmgModifierByArmor)
	BowHitMod int
	BowDmgMod int

	// Stat bonuses (weapon + armor)
	AddStr int
	AddCon int
	AddDex int
	AddInt int
	AddWis int
	AddCha int
	AddHP  int
	AddMP  int
	AddHPR int
	AddMPR int
	AddSP  int
	MDef   int

	// Meta
	SafeEnchant int
	Bless       int
	Tradeable   bool
	MinLevel    int
	MaxLevel    int

	// Class restrictions
	UseRoyal       bool
	UseKnight      bool
	UseMage        bool
	UseElf         bool
	UseDarkElf     bool
	UseDragonKnight bool
	UseIllusionist bool

	// Etcitem specific
	Stackable      bool
	UseType        string
	ItemType       string
	MaxChargeCount int
	FoodVolume     int
	DelayID        int
	DelayTime      int

	// Client use_type byte (integer mapping of UseType string).
	// Sent in S_ADD_INVENTORY_BATCH / S_ADD_ITEM packets.
	// Determines client-side interaction behavior (e.g. 7=show target cursor for identify).
	UseTypeID byte

	// Identify description
	ItemDescID int // itemdesc_id for S_IdentifyDesc packet
}

// ItemTable holds all item templates indexed by ItemID.
// Merges weapon, armor, and etcitem data into one flat lookup.
type ItemTable struct {
	items map[int32]*ItemInfo
}

// Get returns an item by ID, or nil if not found.
func (t *ItemTable) Get(itemID int32) *ItemInfo {
	return t.items[itemID]
}

// Count returns total loaded items.
func (t *ItemTable) Count() int {
	return len(t.items)
}

// LoadItemTable loads weapon, armor, and etcitem YAML files into a single table.
func LoadItemTable(weaponPath, armorPath, etcitemPath string) (*ItemTable, error) {
	t := &ItemTable{items: make(map[int32]*ItemInfo, 4096)}

	if err := loadWeapons(t, weaponPath); err != nil {
		return nil, err
	}
	if err := loadArmors(t, armorPath); err != nil {
		return nil, err
	}
	if err := loadEtcItems(t, etcitemPath); err != nil {
		return nil, err
	}
	return t, nil
}

// --- weapon loading ---

type weaponEntry struct {
	ItemID          int32  `yaml:"item_id"`
	Name            string `yaml:"name"`
	Type            string `yaml:"type"`
	Material        string `yaml:"material"`
	Weight          int32  `yaml:"weight"`
	InvGfx          int32  `yaml:"inv_gfx"`
	GrdGfx          int32  `yaml:"grd_gfx"`
	ItemDescID      int    `yaml:"itemdesc_id"`
	DmgSmall        int    `yaml:"dmg_small"`
	DmgLarge        int    `yaml:"dmg_large"`
	Range           int    `yaml:"range"`
	SafeEnchant     int    `yaml:"safe_enchant"`
	UseRoyal        bool   `yaml:"use_royal"`
	UseKnight       bool   `yaml:"use_knight"`
	UseMage         bool   `yaml:"use_mage"`
	UseElf          bool   `yaml:"use_elf"`
	UseDarkElf      bool   `yaml:"use_darkelf"`
	UseDragonKnight bool   `yaml:"use_dragonknight"`
	UseIllusionist  bool   `yaml:"use_illusionist"`
	HitModifier     int    `yaml:"hit_modifier"`
	DmgModifier     int    `yaml:"dmg_modifier"`
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
	MDef            int    `yaml:"m_def"`
	Bless           int    `yaml:"bless"`
	Tradeable       bool   `yaml:"tradeable"`
	MinLevel        int    `yaml:"min_level"`
	MaxLevel        int    `yaml:"max_level"`
}

type weaponListFile struct {
	Weapons []weaponEntry `yaml:"weapons"`
}

func loadWeapons(t *ItemTable, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read weapons: %w", err)
	}
	var f weaponListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("parse weapons: %w", err)
	}
	for i := range f.Weapons {
		w := &f.Weapons[i]
		t.items[w.ItemID] = &ItemInfo{
			ItemID:          w.ItemID,
			Name:            w.Name,
			InvGfx:          w.InvGfx,
			GrdGfx:          w.GrdGfx,
			Weight:          w.Weight,
			Category:        CategoryWeapon,
			Type:            w.Type,
			Material:        w.Material,
			UseTypeID:       1, // Java: weapon.setUseType(1) — hardcoded
			ItemDescID:      w.ItemDescID,
			DmgSmall:        w.DmgSmall,
			DmgLarge:        w.DmgLarge,
			Range:           w.Range,
			HitMod:          w.HitModifier,
			DmgMod:          w.DmgModifier,
			SafeEnchant:     w.SafeEnchant,
			Bless:           w.Bless,
			Tradeable:       w.Tradeable,
			MinLevel:        w.MinLevel,
			MaxLevel:        w.MaxLevel,
			UseRoyal:        w.UseRoyal,
			UseKnight:       w.UseKnight,
			UseMage:         w.UseMage,
			UseElf:          w.UseElf,
			UseDarkElf:      w.UseDarkElf,
			UseDragonKnight: w.UseDragonKnight,
			UseIllusionist:  w.UseIllusionist,
			AddStr:          w.AddStr,
			AddCon:          w.AddCon,
			AddDex:          w.AddDex,
			AddInt:          w.AddInt,
			AddWis:          w.AddWis,
			AddCha:          w.AddCha,
			AddHP:           w.AddHP,
			AddMP:           w.AddMP,
			AddHPR:          w.AddHPR,
			AddMPR:          w.AddMPR,
			AddSP:           w.AddSP,
			MDef:            w.MDef,
		}
	}
	return nil
}

// --- armor loading ---

type armorEntry struct {
	ItemID          int32  `yaml:"item_id"`
	Name            string `yaml:"name"`
	Type            string `yaml:"type"`
	Material        string `yaml:"material"`
	Weight          int32  `yaml:"weight"`
	InvGfx          int32  `yaml:"inv_gfx"`
	GrdGfx          int32  `yaml:"grd_gfx"`
	ItemDescID      int    `yaml:"itemdesc_id"`
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
	MDef            int    `yaml:"m_def"`
	HitModifier     int    `yaml:"hit_modifier"`
	DmgModifier     int    `yaml:"dmg_modifier"`
	BowHitModifier  int    `yaml:"bow_hit_modifier"`
	BowDmgModifier  int    `yaml:"bow_dmg_modifier"`
	Bless           int    `yaml:"bless"`
	Tradeable       bool   `yaml:"tradeable"`
	MinLevel        int    `yaml:"min_level"`
	MaxLevel        int    `yaml:"max_level"`
}

type armorListFile struct {
	Armors []armorEntry `yaml:"armors"`
}

func loadArmors(t *ItemTable, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read armors: %w", err)
	}
	var f armorListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("parse armors: %w", err)
	}
	for i := range f.Armors {
		a := &f.Armors[i]
		t.items[a.ItemID] = &ItemInfo{
			ItemID:          a.ItemID,
			Name:            a.Name,
			InvGfx:          a.InvGfx,
			GrdGfx:          a.GrdGfx,
			Weight:          a.Weight,
			Category:        CategoryArmor,
			Type:            a.Type,
			Material:        a.Material,
			UseTypeID:       UseTypeToID(a.Type), // Java: armor.setUseType(_useTypes.get(type))
			ItemDescID:      a.ItemDescID,
			AC:              a.AC,
			HitMod:          a.HitModifier,
			DmgMod:          a.DmgModifier,
			BowHitMod:       a.BowHitModifier,
			BowDmgMod:       a.BowDmgModifier,
			SafeEnchant:     a.SafeEnchant,
			Bless:           a.Bless,
			Tradeable:       a.Tradeable,
			MinLevel:        a.MinLevel,
			MaxLevel:        a.MaxLevel,
			UseRoyal:        a.UseRoyal,
			UseKnight:       a.UseKnight,
			UseMage:         a.UseMage,
			UseElf:          a.UseElf,
			UseDarkElf:      a.UseDarkElf,
			UseDragonKnight: a.UseDragonKnight,
			UseIllusionist:  a.UseIllusionist,
			AddStr:          a.AddStr,
			AddCon:          a.AddCon,
			AddDex:          a.AddDex,
			AddInt:          a.AddInt,
			AddWis:          a.AddWis,
			AddCha:          a.AddCha,
			AddHP:           a.AddHP,
			AddMP:           a.AddMP,
			AddHPR:          a.AddHPR,
			AddMPR:          a.AddMPR,
			AddSP:           a.AddSP,
			MDef:            a.MDef,
		}
	}
	return nil
}

// --- etcitem loading ---

type etcItemEntry struct {
	ItemID         int32  `yaml:"item_id"`
	Name           string `yaml:"name"`
	ItemType       string `yaml:"item_type"`
	UseType        string `yaml:"use_type"`
	Material       string `yaml:"material"`
	Weight         int32  `yaml:"weight"`
	InvGfx         int32  `yaml:"inv_gfx"`
	GrdGfx         int32  `yaml:"grd_gfx"`
	ItemDescID     int    `yaml:"itemdesc_id"`
	Stackable      bool   `yaml:"stackable"`
	MaxChargeCount int    `yaml:"max_charge_count"`
	DmgSmall       int    `yaml:"dmg_small"`
	DmgLarge       int    `yaml:"dmg_large"`
	MinLevel       int    `yaml:"min_level"`
	MaxLevel       int    `yaml:"max_level"`
	Bless          int    `yaml:"bless"`
	Tradeable      bool   `yaml:"tradeable"`
	DelayID        int    `yaml:"delay_id"`
	DelayTime      int    `yaml:"delay_time"`
	FoodVolume     int    `yaml:"food_volume"`
}

type etcItemListFile struct {
	Items []etcItemEntry `yaml:"items"`
}

func loadEtcItems(t *ItemTable, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read etcitems: %w", err)
	}
	var f etcItemListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("parse etcitems: %w", err)
	}
	for i := range f.Items {
		e := &f.Items[i]
		t.items[e.ItemID] = &ItemInfo{
			ItemID:         e.ItemID,
			Name:           e.Name,
			InvGfx:         e.InvGfx,
			GrdGfx:         e.GrdGfx,
			Weight:         e.Weight,
			Category:       CategoryEtcItem,
			Type:           e.ItemType,
			UseType:        e.UseType,
			UseTypeID:      UseTypeToID(e.UseType), // Java: item.setUseType(_useTypes.get(use_type))
			Material:       e.Material,
			ItemType:       e.ItemType,
			ItemDescID:     e.ItemDescID,
			DmgSmall:       e.DmgSmall,
			DmgLarge:       e.DmgLarge,
			Stackable:      e.Stackable,
			MaxChargeCount: e.MaxChargeCount,
			Bless:          e.Bless,
			Tradeable:      e.Tradeable,
			MinLevel:       e.MinLevel,
			MaxLevel:       e.MaxLevel,
			FoodVolume:     e.FoodVolume,
			DelayID:        e.DelayID,
			DelayTime:      e.DelayTime,
		}
	}
	return nil
}
