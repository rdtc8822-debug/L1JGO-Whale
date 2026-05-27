package system

import (
	"math/rand"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────────────
// TrapSystem — 陷阱觸發邏輯（遊戲邏輯層）
// 實作 handler.TrapTriggerer 介面。
// ─────────────────────────────────────────────────────────────────────────────

// TrapSystem 處理陷阱觸發的所有遊戲邏輯：傷害、治療、中毒、技能、傳送。
type TrapSystem struct {
	deps *handler.Deps
}

// NewTrapSystem 建立陷阱系統。
func NewTrapSystem(deps *handler.Deps) *TrapSystem {
	return &TrapSystem{deps: deps}
}

// TriggerTraps 處理玩家踩到陷阱的所有遊戲邏輯。
// Java: WorldTrap.onPlayerMoved() → L1TrapInstance.onTrod() → L1Trap.onTrod()。
func (s *TrapSystem) TriggerTraps(sess *net.Session, player *world.PlayerInfo, traps []*world.TrapInstance) {
	// Java C_MoveChar 只在 !pc.isGmInvis() 時呼叫 WorldTrap.onPlayerMoved()。
	if player != nil && isGMInvisible(player) {
		return
	}

	for _, trap := range traps {
		if !trap.Alive {
			continue
		}

		tpl := trap.Template

		// 依類型分派處理
		switch tpl.Type {
		case 1: // 傷害
			s.trapDamage(sess, player, trap)
		case 2: // 治療
			s.trapHeal(sess, player, trap)
		case 3: // 召喚怪物
			s.trapMonster(player, trap)
		case 4: // 中毒
			s.trapPoison(sess, player, trap)
		case 5: // 技能
			s.trapSkill(player, trap)
		case 6: // 傳送
			s.trapTeleport(sess, player, trap)
		}

		// 停用陷阱 + 排入重生佇列
		s.deps.TrapMgr.DisableTrap(trap)

		s.deps.Log.Debug("陷阱觸發",
			zap.String("player", player.Name),
			zap.Int32("trapID", tpl.TrapID),
			zap.Int32("type", tpl.Type),
			zap.Int32("x", trap.X),
			zap.Int32("y", trap.Y),
		)
	}
}

// DetectTraps 處理 DETECTION / COUNTER_DETECTION 的畫面內陷阱偵測。
// Java: WorldTrap.onDetection() → L1Trap.onDetection() → disableTrap()。
func (s *TrapSystem) DetectTraps(sess *net.Session, player *world.PlayerInfo) {
	if s == nil || s.deps == nil || s.deps.TrapMgr == nil || player == nil {
		return
	}
	traps := s.deps.TrapMgr.GetTrapsInScreen(player.X, player.Y, player.MapID)
	for _, trap := range traps {
		if trap == nil || !trap.Alive {
			continue
		}
		if trap.Template != nil && trap.Template.Detectionable && trap.Template.GfxID > 0 {
			s.sendTrapEffect(sess, player, trap)
		}
		s.deps.TrapMgr.DisableTrap(trap)
	}
}

// sendTrapEffect 發送陷阱動畫效果到附近玩家。
// Java: S_EffectLocation — writeC(106) + writeH(x) + writeH(y) + writeH(gfxId)。
func (s *TrapSystem) sendTrapEffect(sess *net.Session, player *world.PlayerInfo, trap *world.TrapInstance) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECTLOCATION)
	w.WriteH(uint16(trap.X))
	w.WriteH(uint16(trap.Y))
	w.WriteH(uint16(trap.Template.GfxID))
	data := w.Bytes()

	// 發送給觸發者
	sess.Send(data)

	handler.BroadcastToVisiblePlayers(s.deps.World, trap.X, trap.Y, trap.MapID, sess.ID, player.ShowID, data)
}

func (s *TrapSystem) sendTrapEffectIfAny(sess *net.Session, player *world.PlayerInfo, trap *world.TrapInstance) {
	if trap.Template.GfxID > 0 {
		s.sendTrapEffect(sess, player, trap)
	}
}

// trapDamage 陷阱傷害處理。
// Java: L1Trap.onType1 — dmg = dice.roll(diceCount) + base → receiveDamage()。
func (s *TrapSystem) trapDamage(sess *net.Session, player *world.PlayerInfo, trap *world.TrapInstance) {
	tpl := trap.Template
	if tpl.Base <= 0 {
		return
	}
	s.sendTrapEffectIfAny(sess, player, trap)
	if playerDamageBlockedBySkm0LikeJava(player) {
		return
	}
	dmg := tpl.Base
	for i := int32(0); i < tpl.DiceCount; i++ {
		if tpl.Dice > 0 {
			dmg += rand.Int31n(tpl.Dice) + 1
		}
	}
	player.HP -= dmg
	if player.HP < 0 {
		player.HP = 0
	}
	player.Dirty = true
	handler.SendHpUpdate(sess, player)
}

// trapHeal 陷阱治療處理。
// Java: L1Trap.onType2 — pt = dice.roll(diceCount) + base → healHp()。
func (s *TrapSystem) trapHeal(sess *net.Session, player *world.PlayerInfo, trap *world.TrapInstance) {
	tpl := trap.Template
	if tpl.Base <= 0 {
		return
	}
	s.sendTrapEffectIfAny(sess, player, trap)
	heal := tpl.Base
	for i := int32(0); i < tpl.DiceCount; i++ {
		if tpl.Dice > 0 {
			heal += rand.Int31n(tpl.Dice) + 1
		}
	}
	player.HP += heal
	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	player.Dirty = true
	handler.SendHpUpdate(sess, player)
}

// trapMonster 處理 type 3 召喚怪物陷阱。
// Java: L1Trap.onType3 以觸發玩家 randomLocation(5,false) 召怪，再 setLink(trodFrom)。
func (s *TrapSystem) trapMonster(player *world.PlayerInfo, trap *world.TrapInstance) {
	if s.deps == nil || s.deps.World == nil || s.deps.Npcs == nil || player == nil || trap == nil || trap.Template == nil {
		return
	}
	tpl := trap.Template
	if tpl.MonsterNpcID <= 0 {
		return
	}
	s.sendTrapEffectIfAny(player.Session, player, trap)
	if tpl.MonsterCount <= 0 {
		return
	}
	tmpl := s.deps.Npcs.Get(tpl.MonsterNpcID)
	if tmpl == nil {
		if s.deps.Log != nil {
			s.deps.Log.Warn("陷阱召喚怪物找不到 NPC 模板", zap.Int32("npcID", tpl.MonsterNpcID))
		}
		return
	}

	for i := int32(0); i < tpl.MonsterCount; i++ {
		x, y := s.trapMonsterLocation(player)
		npc := newRuntimeNpcFromTemplate(tmpl, x, y, player.MapID, 0, player.ShowID, 0, s.deps.SprTable)
		setNpcLinkTargetLikeJava(npc, player.SessionID)
		s.deps.World.AddNpc(npc)
		if s.deps.MapData != nil {
			s.deps.MapData.SetImpassable(npc.MapID, npc.X, npc.Y, true)
		}

		viewers := s.deps.World.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
		for _, viewer := range viewers {
			sendNpcPack(viewer.Session, npc)
		}
	}
}

func (s *TrapSystem) trapMonsterLocation(player *world.PlayerInfo) (int32, int32) {
	if player == nil {
		return 0, 0
	}
	if s.deps == nil || s.deps.World == nil || s.deps.MapData == nil {
		return player.X, player.Y
	}
	for try := 0; try < 40; try++ {
		x := player.X + int32(rand.Intn(11)) - 5
		y := player.Y + int32(rand.Intn(11)) - 5
		if !s.deps.MapData.IsPassablePoint(player.MapID, x, y) {
			continue
		}
		if s.deps.World.IsOccupied(x, y, player.MapID, 0) {
			continue
		}
		return x, y
	}
	return player.X, player.Y
}

// trapPoison 陷阱中毒處理。
// Java: L1Trap.onType4 — 依 poisonType 施加不同中毒效果。
func (s *TrapSystem) trapPoison(_ *net.Session, player *world.PlayerInfo, trap *world.TrapInstance) {
	tpl := trap.Template
	s.sendTrapEffectIfAny(player.Session, player, trap)
	switch tpl.PoisonType {
	case 1: // 傷害毒
		amount := int16(tpl.PoisonDamage)
		if amount <= 0 {
			return
		}
		applyDamagePoisonToPlayerWithInterval(player, player.SessionID, amount, tpl.PoisonTime, s.deps)
	case 2: // 沉默中毒
		if canApplyPoisonToPlayer(player) {
			applySilencePoisonToPlayer(player, s.deps)
		}
	case 3: // 麻痺中毒
		if canApplyPoisonToPlayer(player) {
			applyParalysisPoisonToPlayerWithTiming(player, tpl.PoisonDelay, tpl.PoisonTime, s.deps)
		}
	}
}

// trapSkill 陷阱技能處理。
// Java: L1Trap.onType5 — 施展指定技能。
func (s *TrapSystem) trapSkill(player *world.PlayerInfo, trap *world.TrapInstance) {
	tpl := trap.Template
	if tpl.SkillID <= 0 {
		return
	}
	s.sendTrapEffectIfAny(player.Session, player, trap)
	// 使用 GM buff 方式施放技能到玩家身上
	if s.deps.Skill != nil {
		if skillWithDuration, ok := s.deps.Skill.(interface {
			ApplyGMBuffWithDuration(player *world.PlayerInfo, skillID int32, durationSec int32) bool
		}); ok && tpl.SkillTime > 0 {
			skillWithDuration.ApplyGMBuffWithDuration(player, tpl.SkillID, tpl.SkillTime)
			return
		}
		s.deps.Skill.ApplyGMBuff(player, tpl.SkillID)
	}
}

// trapTeleport 陷阱傳送處理。
// Java: L1Trap.onType6 — 傳送玩家到指定座標。
func (s *TrapSystem) trapTeleport(sess *net.Session, player *world.PlayerInfo, trap *world.TrapInstance) {
	tpl := trap.Template
	if tpl.TeleportX == 0 && tpl.TeleportY == 0 {
		return
	}
	s.sendTrapEffectIfAny(sess, player, trap)
	handler.TeleportPlayer(sess, player, tpl.TeleportX, tpl.TeleportY, int16(tpl.TeleportMapID), 5, s.deps)
}

// ─────────────────────────────────────────────────────────────────────────────
// TrapRespawnSystem — 陷阱重生計時（Phase 3）
// ─────────────────────────────────────────────────────────────────────────────

// TrapRespawnSystem 處理陷阱重生計時。Phase 3（PostUpdate，與 SpawnSystem 同層）。
// Java: ServerTrapTimer — 每 5 秒掃描一次待重生列表。
// Go: 每 tick 檢查到期的重生佇列，由 TrapManager.ProcessRespawns() 處理。
type TrapRespawnSystem struct {
	trapMgr *world.TrapManager
}

// NewTrapRespawnSystem 建立 TrapRespawnSystem。
func NewTrapRespawnSystem(trapMgr *world.TrapManager) *TrapRespawnSystem {
	return &TrapRespawnSystem{trapMgr: trapMgr}
}

// Phase 回傳系統執行階段。
func (s *TrapRespawnSystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

// Update 每 tick 處理到期的陷阱重生。
func (s *TrapRespawnSystem) Update(_ time.Duration) {
	s.trapMgr.ProcessRespawns(time.Now())
}
