package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SkillInfo holds a single skill template.
type SkillInfo struct {
	SkillID         int32
	Name            string
	SkillLevel      int   // 1-based level group (wizard 1-10, knight 11-12, etc.)
	SkillNumber     int   // 0-7 position within level group
	MpConsume        int
	HpConsume        int
	ItemConsumeID    int   // required item template ID (0 = none)
	ItemConsumeCount int   // number of items consumed per cast
	ReuseDelay       int   // ticks
	BuffDuration    int   // seconds (0 = instant)
	Target          string // "attack", "buff", "none"
	TargetTo        int
	DamageValue     int
	DamageDice      int
	DamageDiceCount int
	ProbabilityValue int  // success chance (0 = always succeeds)
	ProbabilityDice  int  // probability penalty per level diff
	Attr            int   // element attribute (1=Fire,2=Ice,4=Wind,8=Earth,16=Light)
	Type            int   // 0=NONE,1=PROB,2=CHANGE,4=CURSE,8=DEATH,16=HEAL,32=RESTORE,64=ATTACK
	Lawful          int   // alignment requirement
	Ranged          int   // -1=touch, 0=self, positive=range
	Area            int   // 0=single, >0=radius, -1=screen
	Through         bool  // pierces obstacles
	ActionID        int   // cast animation action
	CastGfx         int32 // visual effect GFX ID
	CastGfx2        int32
	SysMsgHappen    int   // message ID on success
	SysMsgStop      int   // message ID on buff end
	SysMsgFail      int   // message ID on failure
	IDBitmask       int   // bitmask for S_AddSkill packet (per-level)
}

// SkillTable holds all skills indexed by SkillID.
type SkillTable struct {
	skills map[int32]*SkillInfo
	byName map[string]*SkillInfo // name → skill (for spellbook name matching)
}

// Get returns a skill by ID, or nil if not found.
func (t *SkillTable) Get(skillID int32) *SkillInfo {
	return t.skills[skillID]
}

// GetByName returns a skill by its exact name, or nil if not found.
func (t *SkillTable) GetByName(name string) *SkillInfo {
	return t.byName[name]
}

// Count returns total loaded skills.
func (t *SkillTable) Count() int {
	return len(t.skills)
}

// All returns all skill infos (for building spell lists).
func (t *SkillTable) All() []*SkillInfo {
	result := make([]*SkillInfo, 0, len(t.skills))
	for _, s := range t.skills {
		result = append(result, s)
	}
	return result
}

// --- YAML loading ---

type skillEntry struct {
	SkillID         int32  `yaml:"skill_id"`
	Name            string `yaml:"name"`
	SkillLevel      int    `yaml:"skill_level"`
	SkillNumber     int    `yaml:"skill_number"`
	MpConsume       int    `yaml:"mp_consume"`
	HpConsume       int    `yaml:"hp_consume"`
	ItemConsumeID   int    `yaml:"item_consume_id"`
	ItemConsumeCount int   `yaml:"item_consume_count"`
	ReuseDelay      int    `yaml:"reuse_delay"`
	BuffDuration    int    `yaml:"buff_duration"`
	Target          string `yaml:"target"`
	TargetTo        int    `yaml:"target_to"`
	DamageValue     int    `yaml:"damage_value"`
	DamageDice      int    `yaml:"damage_dice"`
	DamageDiceCount int    `yaml:"damage_dice_count"`
	ProbabilityValue int   `yaml:"probability_value"`
	ProbabilityDice  int   `yaml:"probability_dice"`
	Attr            int    `yaml:"attr"`
	Type            int    `yaml:"type"`
	Lawful          int    `yaml:"lawful"`
	Ranged          int    `yaml:"ranged"`
	Area            int    `yaml:"area"`
	Through         int    `yaml:"through"`
	ID              int    `yaml:"id"`
	NameID          string `yaml:"name_id"`
	ActionID        int    `yaml:"action_id"`
	CastGfx         int32  `yaml:"cast_gfx"`
	CastGfx2        int32  `yaml:"cast_gfx2"`
	SysMsgHappen    int    `yaml:"sys_msg_happen"`
	SysMsgStop      int    `yaml:"sys_msg_stop"`
	SysMsgFail      int    `yaml:"sys_msg_fail"`
}

type skillListFile struct {
	Skills []skillEntry `yaml:"skills"`
}

// LoadSkillTable loads skill definitions from YAML.
func LoadSkillTable(path string) (*SkillTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skills: %w", err)
	}
	var f skillListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse skills: %w", err)
	}
	t := &SkillTable{
		skills: make(map[int32]*SkillInfo, len(f.Skills)),
		byName: make(map[string]*SkillInfo, len(f.Skills)),
	}
	for i := range f.Skills {
		e := &f.Skills[i]
		t.skills[e.SkillID] = &SkillInfo{
			SkillID:         e.SkillID,
			Name:            e.Name,
			SkillLevel:      e.SkillLevel,
			SkillNumber:     e.SkillNumber,
			MpConsume:        e.MpConsume,
			HpConsume:        e.HpConsume,
			ItemConsumeID:    e.ItemConsumeID,
			ItemConsumeCount: e.ItemConsumeCount,
			ReuseDelay:       e.ReuseDelay,
			BuffDuration:    e.BuffDuration,
			Target:          e.Target,
			TargetTo:        e.TargetTo,
			DamageValue:     e.DamageValue,
			DamageDice:      e.DamageDice,
			DamageDiceCount: e.DamageDiceCount,
			ProbabilityValue: e.ProbabilityValue,
			ProbabilityDice:  e.ProbabilityDice,
			Attr:            e.Attr,
			Type:            e.Type,
			Lawful:          e.Lawful,
			Ranged:          e.Ranged,
			Area:            e.Area,
			Through:         e.Through != 0,
			ActionID:        e.ActionID,
			CastGfx:         e.CastGfx,
			CastGfx2:        e.CastGfx2,
			SysMsgHappen:    e.SysMsgHappen,
			SysMsgStop:      e.SysMsgStop,
			SysMsgFail:      e.SysMsgFail,
			IDBitmask:       e.ID,
		}
		t.byName[e.Name] = t.skills[e.SkillID]
	}
	return t, nil
}
