package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ArmorSet holds one armor set definition (ported from Java armor_set table).
// When all items in Items are equipped, the set effect is activated:
//   - PolyID > 0: polymorph transform (visual) + stat bonuses applied
//   - PolyID == 0: stat bonuses only
type ArmorSet struct {
	ID      int     `yaml:"id"`
	Name    string  `yaml:"name"`
	Items   []int32 `yaml:"items"`
	PolyID  int32   `yaml:"poly_id"`
	AC      int     `yaml:"ac"`
	HP      int     `yaml:"hp"`
	MP      int     `yaml:"mp"`
	HPR     int     `yaml:"hpr"`
	MPR     int     `yaml:"mpr"`
	MR      int     `yaml:"mr"`
	Str     int     `yaml:"str"`
	Dex     int     `yaml:"dex"`
	Con     int     `yaml:"con"`
	Wis     int     `yaml:"wis"`
	Cha     int     `yaml:"cha"`
	Intl    int     `yaml:"intl"`
	Hit     int     `yaml:"hit"`
	Dmg     int     `yaml:"dmg"`
	BowHit  int     `yaml:"bow_hit"`
	BowDmg  int     `yaml:"bow_dmg"`
	SP      int     `yaml:"sp"`
	DefWater int    `yaml:"def_water"`
	DefWind  int    `yaml:"def_wind"`
	DefFire  int    `yaml:"def_fire"`
	DefEarth int    `yaml:"def_earth"`
}

// HasStatBonus returns true if this set grants any stat bonuses beyond polymorph.
func (s *ArmorSet) HasStatBonus() bool {
	return s.AC != 0 || s.HP != 0 || s.MP != 0 || s.HPR != 0 || s.MPR != 0 ||
		s.MR != 0 || s.Str != 0 || s.Dex != 0 || s.Con != 0 || s.Wis != 0 ||
		s.Cha != 0 || s.Intl != 0 || s.Hit != 0 || s.Dmg != 0 ||
		s.BowHit != 0 || s.BowDmg != 0 || s.SP != 0 ||
		s.DefWater != 0 || s.DefWind != 0 || s.DefFire != 0 || s.DefEarth != 0
}

// ArmorSetTable indexes sets by ID and by individual item IDs.
type ArmorSetTable struct {
	byID   map[int]*ArmorSet
	byItem map[int32][]*ArmorSet // item ID → all sets that include it
}

// GetByID returns an ArmorSet by set ID, or nil if not found.
func (t *ArmorSetTable) GetByID(id int) *ArmorSet {
	return t.byID[id]
}

// GetSetsForItem returns all armor sets that include the given item ID.
func (t *ArmorSetTable) GetSetsForItem(itemID int32) []*ArmorSet {
	return t.byItem[itemID]
}

// Count returns the number of sets loaded.
func (t *ArmorSetTable) Count() int {
	return len(t.byID)
}

// --- YAML loading ---

type armorSetFile struct {
	Sets []ArmorSet `yaml:"armor_sets"`
}

// LoadArmorSetTable loads armor set definitions from YAML.
func LoadArmorSetTable(path string) (*ArmorSetTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("armorset: read %s: %w", path, err)
	}

	var f armorSetFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("armorset: parse %s: %w", path, err)
	}

	t := &ArmorSetTable{
		byID:   make(map[int]*ArmorSet, len(f.Sets)),
		byItem: make(map[int32][]*ArmorSet),
	}
	for i := range f.Sets {
		s := &f.Sets[i]
		t.byID[s.ID] = s
		for _, itemID := range s.Items {
			t.byItem[itemID] = append(t.byItem[itemID], s)
		}
	}
	return t, nil
}
