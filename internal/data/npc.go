package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// NpcTemplate holds static data for an NPC type loaded from YAML.
type NpcTemplate struct {
	NpcID        int32  `yaml:"npc_id"`
	Name         string `yaml:"name"`
	NameID       string `yaml:"nameid"`
	Impl         string `yaml:"impl"` // L1Monster, L1Merchant, L1Guard, etc.
	GfxID        int32  `yaml:"gfx_id"`
	Level        int16  `yaml:"level"`
	HP           int32  `yaml:"hp"`
	MP           int32  `yaml:"mp"`
	AC           int16  `yaml:"ac"`
	STR          int16  `yaml:"str"`
	DEX          int16  `yaml:"dex"`
	CON          int16  `yaml:"con"`
	WIS          int16  `yaml:"wis"`
	INT          int16  `yaml:"intel"`
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

// SpawnEntry defines where and how many NPCs to spawn.
type SpawnEntry struct {
	NpcID        int32 `yaml:"npc_id"`
	MapID        int16 `yaml:"map_id"`
	X            int32 `yaml:"x"`
	Y            int32 `yaml:"y"`
	Count        int   `yaml:"count"`
	RandomX      int32 `yaml:"randomx"`
	RandomY      int32 `yaml:"randomy"`
	Heading      int16 `yaml:"heading"`
	RespawnDelay int   `yaml:"respawn_delay"` // seconds
}

type npcListFile struct {
	Npcs []NpcTemplate `yaml:"npcs"`
}

type spawnListFile struct {
	Spawns []SpawnEntry `yaml:"spawns"`
}

// NpcTable holds all NPC templates indexed by NpcID.
type NpcTable struct {
	templates map[int32]*NpcTemplate
}

// LoadNpcTable loads NPC templates from a YAML file.
func LoadNpcTable(path string) (*NpcTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read npc_list: %w", err)
	}
	var f npcListFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse npc_list: %w", err)
	}
	t := &NpcTable{templates: make(map[int32]*NpcTemplate, len(f.Npcs))}
	for i := range f.Npcs {
		npc := &f.Npcs[i]
		t.templates[npc.NpcID] = npc
	}
	return t, nil
}

// Get returns an NPC template by ID, or nil if not found.
func (t *NpcTable) Get(npcID int32) *NpcTemplate {
	return t.templates[npcID]
}

// Count returns the number of loaded templates.
func (t *NpcTable) Count() int {
	return len(t.templates)
}

// LoadSpawnList loads spawn entries from a YAML file.
func LoadSpawnList(path string) ([]SpawnEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spawn_list: %w", err)
	}
	var f spawnListFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse spawn_list: %w", err)
	}
	return f.Spawns, nil
}

// NpcAction holds dialog data for an NPC (which HTML to show on click).
type NpcAction struct {
	NpcID        int32  `yaml:"npc_id"`
	NormalAction string `yaml:"normal_action"`
	CaoticAction string `yaml:"caotic_action"`
	TeleportURL  string `yaml:"teleport_url,omitempty"`
	TeleportURLA string `yaml:"teleport_urla,omitempty"`
}

type npcActionListFile struct {
	Actions []NpcAction `yaml:"actions"`
}

// NpcActionTable holds NPC dialog data indexed by NpcID.
type NpcActionTable struct {
	actions map[int32]*NpcAction
}

// LoadNpcActionTable loads NPC dialog actions from a YAML file.
func LoadNpcActionTable(path string) (*NpcActionTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read npc_action_list: %w", err)
	}
	var f npcActionListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse npc_action_list: %w", err)
	}
	t := &NpcActionTable{actions: make(map[int32]*NpcAction, len(f.Actions))}
	for i := range f.Actions {
		a := &f.Actions[i]
		t.actions[a.NpcID] = a
	}
	return t, nil
}

// Get returns an NPC action by NPC ID, or nil if not found.
func (t *NpcActionTable) Get(npcID int32) *NpcAction {
	return t.actions[npcID]
}

// Count returns the number of loaded NPC actions.
func (t *NpcActionTable) Count() int {
	return len(t.actions)
}

// TeleportDest holds a single teleport destination for an NPC action.
type TeleportDest struct {
	Action  string `yaml:"action"`
	NpcID   int32  `yaml:"npc_id"`
	X       int32  `yaml:"x"`
	Y       int32  `yaml:"y"`
	MapID   int16  `yaml:"map_id"`
	Heading int16  `yaml:"heading"`
	Price   int32  `yaml:"price"`
}

type teleportListFile struct {
	Teleports []TeleportDest `yaml:"teleports"`
}

// teleportKey is the composite lookup key: (npc_id, action_name).
type teleportKey struct {
	npcID  int32
	action string
}

// TeleportTable holds teleport destinations indexed by (npcID, action).
type TeleportTable struct {
	dests map[teleportKey]*TeleportDest
}

// LoadTeleportTable loads teleport destinations from a YAML file.
func LoadTeleportTable(path string) (*TeleportTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read teleport_list: %w", err)
	}
	var f teleportListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse teleport_list: %w", err)
	}
	t := &TeleportTable{dests: make(map[teleportKey]*TeleportDest, len(f.Teleports))}
	for i := range f.Teleports {
		d := &f.Teleports[i]
		t.dests[teleportKey{npcID: d.NpcID, action: d.Action}] = d
	}
	return t, nil
}

// Get returns a teleport destination by NPC ID and action name, or nil if not found.
func (t *TeleportTable) Get(npcID int32, action string) *TeleportDest {
	return t.dests[teleportKey{npcID: npcID, action: action}]
}

// Count returns the number of loaded teleport destinations.
func (t *TeleportTable) Count() int {
	return len(t.dests)
}

// TeleportHtmlData holds data values for a teleport HTML dialog page.
type TeleportHtmlData struct {
	ActionName string   `yaml:"action_name"`
	HtmlID     string   `yaml:"html_id"`
	NpcID      int32    `yaml:"npc_id"`
	Data       []string `yaml:"data"`
}

type teleportHtmlFile struct {
	HtmlData []TeleportHtmlData `yaml:"html_data"`
}

// TeleportHtmlTable holds HTML data values indexed by (npcID, actionName).
type TeleportHtmlTable struct {
	entries map[teleportKey]*TeleportHtmlData
}

// LoadTeleportHtmlTable loads teleport HTML data values from a YAML file.
func LoadTeleportHtmlTable(path string) (*TeleportHtmlTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read teleport_html: %w", err)
	}
	var f teleportHtmlFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse teleport_html: %w", err)
	}
	t := &TeleportHtmlTable{entries: make(map[teleportKey]*TeleportHtmlData, len(f.HtmlData))}
	for i := range f.HtmlData {
		h := &f.HtmlData[i]
		t.entries[teleportKey{npcID: h.NpcID, action: h.ActionName}] = h
	}
	return t, nil
}

// Get returns HTML data for a teleport dialog by NPC ID and action name.
func (t *TeleportHtmlTable) Get(npcID int32, actionName string) *TeleportHtmlData {
	return t.entries[teleportKey{npcID: npcID, action: actionName}]
}

// Count returns the number of loaded HTML data entries.
func (t *TeleportHtmlTable) Count() int {
	return len(t.entries)
}
