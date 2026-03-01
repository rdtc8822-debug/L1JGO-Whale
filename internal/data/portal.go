package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PortalEntry defines a portal (dungeon entrance/exit) with source and destination.
type PortalEntry struct {
	SrcX       int32  `yaml:"src_x"`
	SrcY       int32  `yaml:"src_y"`
	SrcMapID   int16  `yaml:"src_map_id"`
	DstX       int32  `yaml:"dst_x"`
	DstY       int32  `yaml:"dst_y"`
	DstMapID   int16  `yaml:"dst_map_id"`
	DstHeading int16  `yaml:"dst_heading"`
	Note       string `yaml:"note"`
}

type portalKey struct {
	x     int32
	y     int32
	mapID int16
}

// PortalTable provides fast lookup of portal destinations by source coordinates.
type PortalTable struct {
	portals map[portalKey]*PortalEntry
}

// LoadPortalTable loads portal_list.yaml.
func LoadPortalTable(path string) (*PortalTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read portal list: %w", err)
	}
	var entries []PortalEntry
	if err := yaml.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse portal list: %w", err)
	}
	t := &PortalTable{
		portals: make(map[portalKey]*PortalEntry, len(entries)),
	}
	for i := range entries {
		e := &entries[i]
		key := portalKey{x: e.SrcX, y: e.SrcY, mapID: e.SrcMapID}
		t.portals[key] = e
	}
	return t, nil
}

// Get returns the portal at the given source coordinates, or nil if none.
func (t *PortalTable) Get(x, y int32, mapID int16) *PortalEntry {
	return t.portals[portalKey{x: x, y: y, mapID: mapID}]
}

// Count returns the total number of portals loaded.
func (t *PortalTable) Count() int {
	return len(t.portals)
}

// ── 隨機傳送門（DungeonRTable）──

// RandomDest 隨機傳送門的一個目標位置。
type RandomDest struct {
	X     int32 `yaml:"x"`
	Y     int32 `yaml:"y"`
	MapID int16 `yaml:"map_id"`
}

// RandomPortalEntry 定義一個隨機傳送門（一個源座標對應多個目標，隨機選一個）。
// Java: DungeonRTable.java — dungeon_random 表。
type RandomPortalEntry struct {
	SrcX         int32        `yaml:"src_x"`
	SrcY         int32        `yaml:"src_y"`
	SrcMapID     int16        `yaml:"src_map_id"`
	DstHeading   int16        `yaml:"dst_heading"`
	Note         string       `yaml:"note"`
	Destinations []RandomDest `yaml:"destinations"`
}

// RandomPortalTable 提供按源座標查詢隨機傳送門。
type RandomPortalTable struct {
	portals map[portalKey]*RandomPortalEntry
}

// LoadRandomPortalTable 載入 portal_random_list.yaml。
func LoadRandomPortalTable(path string) (*RandomPortalTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read random portal list: %w", err)
	}
	var entries []RandomPortalEntry
	if err := yaml.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse random portal list: %w", err)
	}
	t := &RandomPortalTable{
		portals: make(map[portalKey]*RandomPortalEntry, len(entries)),
	}
	for i := range entries {
		e := &entries[i]
		key := portalKey{x: e.SrcX, y: e.SrcY, mapID: e.SrcMapID}
		t.portals[key] = e
	}
	return t, nil
}

// Get 返回指定源座標的隨機傳送門，不存在則返回 nil。
func (t *RandomPortalTable) Get(x, y int32, mapID int16) *RandomPortalEntry {
	return t.portals[portalKey{x: x, y: y, mapID: mapID}]
}

// Count 返回載入的隨機傳送門數量。
func (t *RandomPortalTable) Count() int {
	return len(t.portals)
}
