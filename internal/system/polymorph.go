package system

import (
	"fmt"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

// PolymorphSystem 處理玩家變身與解除變身邏輯。
type PolymorphSystem struct {
	deps *handler.Deps
}

func NewPolymorphSystem(deps *handler.Deps) *PolymorphSystem {
	return &PolymorphSystem{deps: deps}
}

// ==================== 變身 ====================

// DoPoly implements handler.PolymorphManager — 將玩家變身為指定形態。
// cause: PolyCauseMagic(1), PolyCauseGM(2), PolyCauseNPC(4). cause=0 bypasses cause check.
// durationSec: buff 持續秒數（0 = 永久直到取消）。
func (s *PolymorphSystem) DoPoly(player *world.PlayerInfo, polyID int32, durationSec int, cause int) {
	if player.Dead {
		return
	}
	if s.deps.Polys == nil {
		return
	}

	poly := s.deps.Polys.GetByID(polyID)
	if poly == nil {
		return
	}

	// Cause check (Java: isMatchCause)
	if !poly.IsMatchCause(cause) {
		return
	}

	// 英雄變身限制（Java: L1PolyMorph GFX 13715-13745 需排名上榜）
	if polyID >= 13715 && polyID <= 13745 {
		if s.deps.Ranking == nil || !s.deps.Ranking.IsHero(player.Name) {
			handler.SendServerMessage(player.Session, 181) // "你無法使用。"
			return
		}
	}

	// 已有變身則先解除
	if player.TempCharGfx > 0 {
		s.UndoPoly(player)
	}

	// 設定變身狀態
	player.TempCharGfx = polyID
	player.PolyID = polyID

	// 檢查武器相容性 — 變身形態不允許的武器則隱藏視覺
	if player.CurrentWeapon != 0 {
		wpn := player.Equip.Weapon()
		if wpn != nil {
			wpnInfo := s.deps.Items.Get(wpn.ItemID)
			if wpnInfo != nil && !poly.IsWeaponEquipable(wpnInfo.Type) {
				player.CurrentWeapon = 0
			}
		}
	}

	// 廣播外觀變更
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		handler.SendChangeShape(viewer.Session, player.CharID, polyID, player.CurrentWeapon)
	}

	// 強制脫下不相容裝備
	s.forceUnequipIncompat(player, poly)

	// 註冊為 buff（skillID=67 變形術）
	if durationSec > 0 {
		buff := &world.ActiveBuff{
			SkillID:   handler.SkillShapeChange,
			TicksLeft: durationSec * 5, // 秒 → tick（每 tick 200ms）
		}
		old := player.AddBuff(buff)
		if old != nil {
			handler.RevertBuffStats(player, old)
		}

		// 發送變身計時圖示：S_PacketBox sub 35
		handler.SendPolyIcon(player.Session, uint16(durationSec))
	}

	s.deps.Log.Info(fmt.Sprintf("玩家變身  角色=%s  形態=%s(GFX:%d)  持續=%d秒",
		player.Name, poly.Name, polyID, durationSec))
}

// ==================== 解除變身 ====================

// UndoPoly implements handler.PolymorphManager — 解除玩家變身，恢復原始外觀。
func (s *PolymorphSystem) UndoPoly(player *world.PlayerInfo) {
	if player.TempCharGfx == 0 {
		return // 未變身
	}

	player.TempCharGfx = 0
	player.PolyID = 0

	// 恢復武器視覺
	if wpn := player.Equip.Weapon(); wpn != nil {
		wpnInfo := s.deps.Items.Get(wpn.ItemID)
		if wpnInfo != nil {
			player.CurrentWeapon = world.WeaponVisualID(wpnInfo.Type)
		}
	}

	// 廣播原始外觀
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		handler.SendChangeShape(viewer.Session, player.CharID, player.ClassID, player.CurrentWeapon)
	}

	// 取消變身計時圖示
	handler.SendPolyIcon(player.Session, 0)

	// 移除變形術 buff
	player.RemoveBuff(handler.SkillShapeChange)

	s.deps.Log.Info(fmt.Sprintf("玩家解除變身  角色=%s", player.Name))
}

// ==================== 內部輔助函式 ====================

// ==================== 變身卷軸 / 技能 ====================

// UsePolyScroll 處理變身卷軸使用（業務邏輯從 handler/polymorph.go 搬入）。
// monsterName="" 表示取消目前變身。
func (s *PolymorphSystem) UsePolyScroll(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, monsterName string) {
	// 空字串 = 取消變身（Java: s.equals("")）
	if monsterName == "" {
		if player.TempCharGfx != 0 {
			s.UndoPoly(player)
			// 消耗卷軸
			removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
			if removed {
				handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
			} else {
				handler.SendItemCountUpdate(sess, invItem)
			}
			handler.SendWeightUpdate(sess, player)
		}
		return
	}

	if s.deps.Polys == nil {
		return
	}

	// 查詢變身形態
	poly := s.deps.Polys.GetByName(monsterName)
	if poly == nil {
		handler.SendServerMessage(sess, 181) // "無法變成你指定的怪物。"
		return
	}

	// 等級檢查（Java: poly.getMinLevel() <= pc.getLevel()）
	if poly.MinLevel > 0 && int(player.Level) < poly.MinLevel {
		handler.SendServerMessage(sess, 181)
		return
	}

	// 原因檢查 — 卷軸使用 PolyCauseMagic (1)
	if !poly.IsMatchCause(data.PolyCauseMagic) {
		handler.SendServerMessage(sess, 181)
		return
	}

	// 計算持續時間
	duration := handler.PolyScrollDuration(invItem.ItemID)

	// 執行變身
	s.DoPoly(player, poly.PolyID, duration, data.PolyCauseMagic)

	// 消耗卷軸
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, invItem)
	}
	handler.SendWeightUpdate(sess, player)
}

// UsePolySkill 處理變形術技能選擇對話框結果（業務邏輯從 handler/polymorph.go 搬入）。
func (s *PolymorphSystem) UsePolySkill(sess *net.Session, player *world.PlayerInfo, monsterName string) {
	if s.deps.Polys == nil {
		return
	}

	poly := s.deps.Polys.GetByName(monsterName)
	if poly == nil {
		handler.SendServerMessage(sess, 181) // "此怪物名稱不正確。"
		return
	}

	// 等級檢查
	if poly.MinLevel > 0 && int(player.Level) < poly.MinLevel {
		handler.SendServerMessage(sess, 181)
		return
	}

	// 原因檢查 — 技能 67 為魔法原因
	if !poly.IsMatchCause(data.PolyCauseMagic) {
		handler.SendServerMessage(sess, 181)
		return
	}

	// 消耗魔法材料（item 40318 for skill 67）
	skill := s.deps.Skills.Get(handler.SkillShapeChange)
	if skill != nil && skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot == nil || slot.Count < int32(skill.ItemConsumeCount) {
			handler.SendServerMessage(sess, 280) // "施展魔法失敗。"
			return
		}
		removed := player.Inv.RemoveItem(slot.ObjectID, int32(skill.ItemConsumeCount))
		if removed {
			handler.SendRemoveInventoryItem(sess, slot.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, slot)
		}
		handler.SendWeightUpdate(sess, player)
	}

	// 執行變身：7200 秒 = 2 小時（Java 預設）
	s.DoPoly(player, poly.PolyID, 7200, data.PolyCauseMagic)
}

// ==================== 內部輔助函式 ====================

// forceUnequipIncompat 強制脫下與當前變身形態不相容的所有裝備。
// Java: L1PolyMorph.doPoly() → takeoff loop
func (s *PolymorphSystem) forceUnequipIncompat(player *world.PlayerInfo, poly *data.PolymorphInfo) {
	sess := player.Session

	for slot := world.EquipSlot(1); slot < world.SlotMax; slot++ {
		item := player.Equip.Get(slot)
		if item == nil {
			continue
		}

		itemInfo := s.deps.Items.Get(item.ItemID)
		if itemInfo == nil {
			continue
		}

		shouldUnequip := false

		if slot == world.SlotWeapon {
			// 檢查武器相容性
			if !poly.IsWeaponEquipable(itemInfo.Type) {
				shouldUnequip = true
			}
		} else {
			// 檢查防具相容性
			if !poly.IsArmorEquipable(itemInfo.Type) {
				shouldUnequip = true
			}
		}

		if shouldUnequip {
			// 詛咒物品（bless == 2）即使變身也無法脫下
			if item.Bless == 2 {
				continue
			}
			if s.deps.Equip != nil {
				s.deps.Equip.UnequipSlot(sess, player, slot)
			}
		}
	}
}
