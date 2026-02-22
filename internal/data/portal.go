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
