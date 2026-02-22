package data

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MapInfo holds metadata for a single map, loaded from map_list.yaml.
type MapInfo struct {
	MapID         int16   `yaml:"map_id"`
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

// mapEntry stores loaded tile data + metadata for one map.
type mapEntry struct {
	info   MapInfo
	tiles  []byte // flat array [x * height + y], row-major by X
	width  int32
	height int32
}

// MapDataTable provides map tile data and metadata lookups.
type MapDataTable struct {
	maps map[int16]*mapEntry
}

// Tile flag constants matching L1J L1V1Map.java
const (
	tilePassableEast  byte = 0x01 // bit 0
	tilePassableNorth byte = 0x02 // bit 1
	tileArrowEast     byte = 0x04 // bit 2
	tileArrowNorth    byte = 0x08 // bit 3
	tileZoneMask      byte = 0x30 // bits 4-5
	tileZoneNormal    byte = 0x00
	tileZoneSafety    byte = 0x10
	tileZoneCombat    byte = 0x20
	tileImpassable    byte = 0x80 // bit 7 — dynamic mob block
)

type mapListFile struct {
	Maps []MapInfo `yaml:"maps"`
}

// LoadMapData loads map metadata from YAML and tile data from text files.
// yamlPath: path to map_list.yaml
// tileDir: directory containing {mapid}.txt tile files
func LoadMapData(yamlPath, tileDir string) (*MapDataTable, error) {
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("read map list %s: %w", yamlPath, err)
	}
	var file mapListFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse map list: %w", err)
	}

	table := &MapDataTable{
		maps: make(map[int16]*mapEntry, len(file.Maps)),
	}

	for _, info := range file.Maps {
		width := info.EndX - info.StartX + 1
		height := info.EndY - info.StartY + 1
		if width <= 0 || height <= 0 {
			continue
		}

		tiles, err := loadTileFile(tileDir, int(info.MapID), int(width), int(height))
		if err != nil {
			// Map file missing is non-fatal — log and skip
			continue
		}

		entry := &mapEntry{
			info:   info,
			tiles:  tiles,
			width:  width,
			height: height,
		}
		table.maps[info.MapID] = entry
	}

	return table, nil
}

// loadTileFile reads a CSV tile file: each line is a row of comma-separated byte values.
// Layout matches Java TextMapReader: map[x][y], file rows = Y lines, columns = X values.
func loadTileFile(dir string, mapID, xSize, ySize int) ([]byte, error) {
	path := filepath.Join(dir, strconv.Itoa(mapID)+".txt")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Allocate flat array: tiles[x * ySize + y]
	tiles := make([]byte, xSize*ySize)

	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for large maps (map 4 is 1856 wide × 2 chars ≈ 5KB per line)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	y := 0
	for scanner.Scan() && y < ySize {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		x := 0
		for _, tok := range strings.Split(line, ",") {
			if x >= xSize {
				break
			}
			val, err := strconv.ParseInt(strings.TrimSpace(tok), 10, 16)
			if err != nil {
				val = 0
			}
			tiles[x*ySize+y] = byte(val)
			x++
		}
		y++
	}

	return tiles, scanner.Err()
}

// Count returns the number of maps loaded with tile data.
func (t *MapDataTable) Count() int {
	return len(t.maps)
}

// GetInfo returns metadata for a map, or nil if not found.
func (t *MapDataTable) GetInfo(mapID int16) *MapInfo {
	e := t.maps[mapID]
	if e == nil {
		return nil
	}
	return &e.info
}

// accessTile returns the tile byte at world coordinates, or 0 if out of bounds.
func (t *MapDataTable) accessTile(mapID int16, x, y int32) byte {
	e := t.maps[mapID]
	if e == nil {
		return 0
	}
	lx := x - e.info.StartX
	ly := y - e.info.StartY
	if lx < 0 || lx >= e.width || ly < 0 || ly >= e.height {
		return 0
	}
	return e.tiles[int(lx)*int(e.height)+int(ly)]
}

// accessOriginalTile returns tile without the dynamic impassable flag.
func (t *MapDataTable) accessOriginalTile(mapID int16, x, y int32) byte {
	return t.accessTile(mapID, x, y) & ^tileImpassable
}

// IsInMap checks if world coordinates are within the map bounds.
func (t *MapDataTable) IsInMap(mapID int16, x, y int32) bool {
	e := t.maps[mapID]
	if e == nil {
		return false
	}
	// Special case: map 4 (mainland) brown area exclusion, matching Java
	if mapID == 4 && (x < 32520 || y < 32070 || (y < 32190 && x < 33950)) {
		return false
	}
	return e.info.StartX <= x && x <= e.info.EndX &&
		e.info.StartY <= y && y <= e.info.EndY
}

// IsPassable checks if movement from (x,y) in the given heading direction is allowed.
// heading: 0=N, 1=NE, 2=E, 3=SE, 4=S, 5=SW, 6=W, 7=NW
// This is a direct port of Java L1V1Map.isPassable(x, y, heading).
func (t *MapDataTable) IsPassable(mapID int16, x, y int32, heading int) bool {
	if heading < 0 || heading > 7 {
		return false
	}

	// Current tile
	tile1 := t.accessTile(mapID, x, y)

	// Destination tile
	dx := headingDX[heading]
	dy := headingDY[heading]
	nx := x + dx
	ny := y + dy
	tile2 := t.accessTile(mapID, nx, ny)

	// Check destination not dynamically blocked
	if tile2&tileImpassable != 0 {
		return false
	}

	// Destination must have at least one passability bit
	if tile2&tilePassableEast == 0 && tile2&tilePassableNorth == 0 {
		return false
	}

	switch heading {
	case 0: // North
		return tile1&tilePassableNorth != 0
	case 1: // NE
		tile3 := t.accessTile(mapID, x, y-1)
		tile4 := t.accessTile(mapID, x+1, y)
		return (tile3&tilePassableEast != 0) || (tile4&tilePassableNorth != 0)
	case 2: // East
		return tile1&tilePassableEast != 0
	case 3: // SE
		tile3 := t.accessTile(mapID, x, y+1)
		return tile3&tilePassableEast != 0
	case 4: // South
		return tile2&tilePassableNorth != 0
	case 5: // SW
		return (tile2&tilePassableEast != 0) || (tile2&tilePassableNorth != 0)
	case 6: // West
		return tile2&tilePassableEast != 0
	case 7: // NW
		tile3 := t.accessTile(mapID, x-1, y)
		return tile3&tilePassableNorth != 0
	}
	return false
}

// IsPassablePoint checks if (x,y) is passable from any direction (used for spawn validation).
func (t *MapDataTable) IsPassablePoint(mapID int16, x, y int32) bool {
	return t.IsPassable(mapID, x, y-1, 4) ||
		t.IsPassable(mapID, x+1, y, 6) ||
		t.IsPassable(mapID, x, y+1, 0) ||
		t.IsPassable(mapID, x-1, y, 2)
}

// IsSafetyZone checks if the tile at (x,y) is a safety zone.
func (t *MapDataTable) IsSafetyZone(mapID int16, x, y int32) bool {
	tile := t.accessOriginalTile(mapID, x, y)
	return tile&tileZoneMask == tileZoneSafety
}

// IsCombatZone checks if the tile at (x,y) is a combat zone.
func (t *MapDataTable) IsCombatZone(mapID int16, x, y int32) bool {
	tile := t.accessOriginalTile(mapID, x, y)
	return tile&tileZoneMask == tileZoneCombat
}

// IsNormalZone checks if the tile at (x,y) is a normal zone.
func (t *MapDataTable) IsNormalZone(mapID int16, x, y int32) bool {
	tile := t.accessOriginalTile(mapID, x, y)
	return tile&tileZoneMask == tileZoneNormal
}

// SetImpassable sets or clears the dynamic impassable flag (for mob blocking).
func (t *MapDataTable) SetImpassable(mapID int16, x, y int32, blocked bool) {
	e := t.maps[mapID]
	if e == nil {
		return
	}
	lx := x - e.info.StartX
	ly := y - e.info.StartY
	if lx < 0 || lx >= e.width || ly < 0 || ly >= e.height {
		return
	}
	idx := int(lx)*int(e.height) + int(ly)
	if blocked {
		e.tiles[idx] |= tileImpassable
	} else {
		e.tiles[idx] &^= tileImpassable
	}
}

// heading direction deltas: 0=N, 1=NE, 2=E, 3=SE, 4=S, 5=SW, 6=W, 7=NW
var headingDX = [8]int32{0, 1, 1, 1, 0, -1, -1, -1}
var headingDY = [8]int32{-1, -1, 0, 1, 1, 1, 0, -1}
