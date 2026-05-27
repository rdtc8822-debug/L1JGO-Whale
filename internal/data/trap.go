package data

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// TrapTemplate 陷阱範本定義（從 YAML 載入）。
// Java: L1Trap — 6 種類型：傷害/治療/怪物/中毒/技能/傳送。
type TrapTemplate struct {
	TrapID        int32  `yaml:"trap_id"`
	Note          string `yaml:"note,omitempty"`
	Type          int32  `yaml:"type"`                    // 1=傷害 2=治療 3=怪物 4=中毒 5=技能 6=傳送
	GfxID         int32  `yaml:"gfx_id"`                  // 客戶端動畫 ID（0=無動畫）
	Detectionable bool   `yaml:"detectionable,omitempty"` // Java: isDetectionable，DETECTION 只對 true 顯示特效

	// 傷害/治療（type 1, 2）
	Base      int32 `yaml:"base,omitempty"`       // 基礎值
	Dice      int32 `yaml:"dice,omitempty"`       // 骰子面數
	DiceCount int32 `yaml:"dice_count,omitempty"` // 骰子次數

	// 怪物（type 3）
	MonsterNpcID int32 `yaml:"monster_npc_id,omitempty"` // 召喚的 NPC ID
	MonsterCount int32 `yaml:"monster_count,omitempty"`  // 召喚數量

	// 中毒（type 4）
	PoisonType   int32 `yaml:"poison_type,omitempty"`   // 1=一般 2=沉默 3=麻痺
	PoisonDelay  int32 `yaml:"poison_delay,omitempty"`  // 延遲（毫秒）
	PoisonTime   int32 `yaml:"poison_time,omitempty"`   // 持續時間（毫秒）
	PoisonDamage int32 `yaml:"poison_damage,omitempty"` // 傷害量

	// 技能（type 5）
	SkillID   int32 `yaml:"skill_id,omitempty"`   // 技能 ID
	SkillTime int32 `yaml:"skill_time,omitempty"` // 技能持續秒數

	// 傳送（type 6）
	TeleportX     int32 `yaml:"teleport_x,omitempty"`
	TeleportY     int32 `yaml:"teleport_y,omitempty"`
	TeleportMapID int32 `yaml:"teleport_map_id,omitempty"`
}

// TrapSpawn 陷阱生成點定義（從 YAML 載入）。
// Java: spawnlist_trap — 每個生成點可產生多個陷阱實例，支援隨機座標範圍。
type TrapSpawn struct {
	TrapID int32 `yaml:"trap_id"` // 對應 TrapTemplate.TrapID
	MapID  int32 `yaml:"map_id"`
	X      int32 `yaml:"x"`     // 基準座標 X
	Y      int32 `yaml:"y"`     // 基準座標 Y
	RndX   int32 `yaml:"rnd_x"` // 隨機範圍 X（0=固定位置）
	RndY   int32 `yaml:"rnd_y"` // 隨機範圍 Y（0=固定位置）
	Count  int32 `yaml:"count"` // 生成數量
	Span   int32 `yaml:"span"`  // 重生週期（毫秒，0=一次性）
}

// TrapData 陷阱資料（範本 + 生成點）。
type TrapData struct {
	Templates map[int32]*TrapTemplate // trap_id → template
	Spawns    []TrapSpawn
}

// LoadTrapData 從 YAML 載入陷阱範本與生成點。
func LoadTrapData(dir string) (*TrapData, error) {
	// 載入範本
	tplPath := filepath.Join(dir, "traps.yaml")
	tplRaw, err := os.ReadFile(tplPath)
	if err != nil {
		return nil, fmt.Errorf("讀取陷阱範本: %w", err)
	}
	var templates []TrapTemplate
	if err := yaml.Unmarshal(tplRaw, &templates); err != nil {
		return nil, fmt.Errorf("解析陷阱範本: %w", err)
	}

	tplMap := make(map[int32]*TrapTemplate, len(templates))
	for i := range templates {
		t := &templates[i]
		tplMap[t.TrapID] = t
	}

	// 載入生成點
	spawnPath := filepath.Join(dir, "spawnlist_trap.yaml")
	spawnRaw, err := os.ReadFile(spawnPath)
	if err != nil {
		return nil, fmt.Errorf("讀取陷阱生成點: %w", err)
	}
	var spawns []TrapSpawn
	if err := yaml.Unmarshal(spawnRaw, &spawns); err != nil {
		return nil, fmt.Errorf("解析陷阱生成點: %w", err)
	}

	return &TrapData{
		Templates: tplMap,
		Spawns:    spawns,
	}, nil
}
