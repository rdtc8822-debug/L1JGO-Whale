package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DropItem represents a single possible drop from a mob.
type DropItem struct {
	ItemID       int32 `yaml:"item_id"`
	Min          int   `yaml:"min"`
	Max          int   `yaml:"max"`
	Chance       int   `yaml:"chance"`       // out of 1,000,000 (100% = 1000000)
	EnchantLevel int   `yaml:"enchant_level"`
}

type mobDropEntry struct {
	MobID int32      `yaml:"mob_id"`
	Items []DropItem `yaml:"items"`
}

type dropListFile struct {
	Drops []mobDropEntry `yaml:"drops"`
}

// DropTable holds all mob drop data indexed by mob template ID.
type DropTable struct {
	drops map[int32][]DropItem
}

// Get returns the drop list for a mob, or nil if none defined.
func (t *DropTable) Get(mobID int32) []DropItem {
	return t.drops[mobID]
}

// Count returns the number of mobs with drop entries.
func (t *DropTable) Count() int {
	return len(t.drops)
}

// LoadDropTable loads mob drop data from a YAML file.
func LoadDropTable(path string) (*DropTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read drop_list: %w", err)
	}
	var f dropListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse drop_list: %w", err)
	}
	t := &DropTable{drops: make(map[int32][]DropItem, len(f.Drops))}
	for _, entry := range f.Drops {
		t.drops[entry.MobID] = entry.Items
	}
	return t, nil
}
