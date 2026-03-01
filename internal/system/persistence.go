package system

import (
	"context"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// PersistenceSystem periodically auto-saves all online players' character data,
// inventory, bookmarks, known spells, and active buffs. Phase 5 (Persist).
type PersistenceSystem struct {
	world     *world.State
	charRepo  *persist.CharacterRepo
	itemRepo  *persist.ItemRepo
	buffRepo  *persist.BuffRepo
	walRepo   *persist.WALRepo
	log       *zap.Logger
	tickCount int
	interval  int // auto-save every N ticks
}

func NewPersistenceSystem(ws *world.State, charRepo *persist.CharacterRepo, itemRepo *persist.ItemRepo, buffRepo *persist.BuffRepo, walRepo *persist.WALRepo, log *zap.Logger, intervalTicks int) *PersistenceSystem {
	return &PersistenceSystem{
		world:    ws,
		charRepo: charRepo,
		itemRepo: itemRepo,
		buffRepo: buffRepo,
		walRepo:  walRepo,
		log:      log,
		interval: intervalTicks,
	}
}

func (s *PersistenceSystem) Phase() coresys.Phase { return coresys.PhasePersist }

func (s *PersistenceSystem) Update(_ time.Duration) {
	s.tickCount++
	if s.tickCount < s.interval {
		return
	}
	s.tickCount = 0
	s.saveAllPlayers()
}

// SaveAllPlayers persists all online players immediately, ignoring dirty flags.
// Called for graceful shutdown to ensure no data is lost.
func (s *PersistenceSystem) SaveAllPlayers() {
	s.savePlayers(false) // dirtyOnly=false → save all for shutdown safety
}

func (s *PersistenceSystem) saveAllPlayers() {
	s.savePlayers(true) // dirtyOnly=true → only save players with dirty flag
}

// savePlayers persists player data. If dirtyOnly is true, only saves players
// whose Dirty flag is set and resets the flag after successful save.
func (s *PersistenceSystem) savePlayers(dirtyOnly bool) {
	count := 0
	s.world.AllPlayers(func(p *world.PlayerInfo) {
		if dirtyOnly && !p.Dirty {
			return // skip clean players — no state change since last save
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// 儲存時必須扣除裝備加成和 buff 加成，只保存基礎值。
		// 否則重新登入時 InitEquipStats / loadAndRestoreBuffs 會重複疊加，造成屬性膨脹。
		eq := p.EquipBonuses
		var bStr, bDex, bCon, bWis, bIntel, bCha, bMaxHP, bMaxMP int16
		for _, b := range p.ActiveBuffs {
			bStr += b.DeltaStr
			bDex += b.DeltaDex
			bCon += b.DeltaCon
			bWis += b.DeltaWis
			bIntel += b.DeltaIntel
			bCha += b.DeltaCha
			bMaxHP += b.DeltaMaxHP
			bMaxMP += b.DeltaMaxMP
		}
		row := &persist.CharacterRow{
			Name:       p.Name,
			Level:      p.Level,
			Exp:        int64(p.Exp),
			HP:         p.HP,
			MP:         p.MP,
			MaxHP:      p.MaxHP - int16(eq.AddHP) - bMaxHP,
			MaxMP:      p.MaxMP - int16(eq.AddMP) - bMaxMP,
			X:          p.X,
			Y:          p.Y,
			MapID:      p.MapID,
			Heading:    p.Heading,
			Lawful:     p.Lawful,
			Str:        p.Str - int16(eq.AddStr) - bStr,
			Dex:        p.Dex - int16(eq.AddDex) - bDex,
			Con:        p.Con - int16(eq.AddCon) - bCon,
			Wis:        p.Wis - int16(eq.AddWis) - bWis,
			Cha:        p.Cha - int16(eq.AddCha) - bCha,
			Intel:      p.Intel - int16(eq.AddInt) - bIntel,
			BonusStats:  p.BonusStats,
			ElixirStats: p.ElixirStats,
			ClanID:      p.ClanID,
			ClanName:   p.ClanName,
			ClanRank:   p.ClanRank,
			Title:      p.Title,
			Karma:      p.Karma,
			PKCount:    p.PKCount,
		}
		if err := s.charRepo.SaveCharacter(ctx, row); err != nil {
			s.log.Error("自動存檔角色失敗", zap.String("name", p.Name), zap.Error(err))
			return
		}
		if err := s.itemRepo.SaveInventory(ctx, p.CharID, p.Inv, &p.Equip); err != nil {
			s.log.Error("自動存檔背包失敗", zap.String("name", p.Name), zap.Error(err))
			return
		}
		if err := s.charRepo.SaveBookmarks(ctx, p.Name, bookmarksToRows(p.Bookmarks)); err != nil {
			s.log.Error("自動存檔書籤失敗", zap.String("name", p.Name), zap.Error(err))
		}
		if err := s.charRepo.SaveKnownSpells(ctx, p.Name, p.KnownSpells); err != nil {
			s.log.Error("自動存檔魔法書失敗", zap.String("name", p.Name), zap.Error(err))
		}
		if len(p.MapTimeUsed) > 0 {
			if err := s.charRepo.SaveMapTimes(ctx, p.Name, p.MapTimeUsed); err != nil {
				s.log.Error("自動存檔限時地圖時間失敗", zap.String("name", p.Name), zap.Error(err))
			}
		}
		// Save active buffs (including polymorph state)
		if s.buffRepo != nil && len(p.ActiveBuffs) > 0 {
			buffRows := handler.BuffRowsFromPlayer(p)
			if len(buffRows) > 0 {
				if err := s.buffRepo.SaveBuffs(ctx, p.CharID, buffRows); err != nil {
					s.log.Error("自動存檔buff失敗", zap.String("name", p.Name), zap.Error(err))
				}
			}
		}
		p.Dirty = false
		count++
	})
	if count > 0 {
		s.log.Info("自動存檔完成", zap.Int("玩家數", count))
	}

	// Mark WAL entries as processed after successful batch save.
	// This prevents replay of already-persisted economic transactions on crash recovery.
	if s.walRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.walRepo.MarkProcessed(ctx); err != nil {
			s.log.Error("WAL MarkProcessed 失敗", zap.Error(err))
		}
	}
}

// bookmarksToRows is defined in input.go (shared within the system package).
