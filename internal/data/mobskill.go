package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MobSkill represents a single skill entry for an NPC.
type MobSkill struct {
	ActNo         int `yaml:"act_no"`
	Type          int `yaml:"type"`
	MpConsume     int `yaml:"mp_consume"`
	TriggerRandom int `yaml:"trigger_random"` // probability 0-100
	TriggerHP     int `yaml:"trigger_hp"`     // HP% threshold (0 = always)
	TriggerRange  int `yaml:"trigger_range"`  // negative = within N tiles
	SkillID       int `yaml:"skill_id"`
	ActID         int `yaml:"act_id"`         // animation GFX
	Leverage      int `yaml:"leverage"`       // damage multiplier (0 = use skill damage)
	GfxID         int `yaml:"gfx_id"`
	SkillArea     int `yaml:"skill_area"`
}

type mobSkillEntry struct {
	MobID  int32      `yaml:"mob_id"`
	Skills []MobSkill `yaml:"skills"`
}

type mobSkillFile struct {
	MobSkills []mobSkillEntry `yaml:"mob_skills"`
}

// MobSkillTable holds all mob skill data indexed by NPC template ID.
type MobSkillTable struct {
	skills map[int32][]MobSkill
}

// Get returns the skill list for a mob, or nil if none defined.
func (t *MobSkillTable) Get(npcID int32) []MobSkill {
	return t.skills[npcID]
}

// Count returns the number of mobs with skill entries.
func (t *MobSkillTable) Count() int {
	return len(t.skills)
}

// LoadMobSkillTable loads mob skill data from a YAML file.
func LoadMobSkillTable(path string) (*MobSkillTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mob_skill_list: %w", err)
	}
	var f mobSkillFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse mob_skill_list: %w", err)
	}
	t := &MobSkillTable{skills: make(map[int32][]MobSkill, len(f.MobSkills))}
	for _, entry := range f.MobSkills {
		t.skills[entry.MobID] = entry.Skills
	}
	return t, nil
}
