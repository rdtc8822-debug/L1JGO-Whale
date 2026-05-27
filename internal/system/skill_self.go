package system

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

// ========================================================================
//  自身技能
// ========================================================================

// executeSelfSkill 處理自身目標技能（護盾、光明、冥想等）。
// fireBlessWeaponAllowed 對齊 Java `C_UseSkill.java:154-169`——155 FIRE_BLESS（烈炎氣息）要求
// 裝備 type 1/2/3/5/6/7 之一（劍/匕首/雙手劍/長矛/棍棒/法杖），其他武器類型或無武器一律拒絕。
func fireBlessWeaponAllowed(player *world.PlayerInfo, items *data.ItemTable) bool {
	if player == nil || items == nil {
		return false
	}
	wpn := player.Equip.Weapon()
	if wpn == nil {
		return false
	}
	info := items.Get(wpn.ItemID)
	if info == nil {
		return false
	}
	switch info.Type {
	case "sword", "dagger", "tohandsword", "spear", "blunt", "staff":
		return true
	}
	return false
}

func (s *SkillSystem) executeSelfSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo) {
	nearby := s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, 0, player.ShowID)

	switch skill.SkillID {
	case 2: // 日光術
		// 有持續時間但無屬性變化，由 applyBuffEffect 處理

	case 13, 72: // 無所遁形術 / 強力無所遁形術 — 揭示附近隱身玩家
		// Java 參考: L1SkillUse.detection() — 移除 buff 60（隱身術）和 97（暗影閃避）
		// 注意: Java 會跳過 GM 隱身（isGmInvis）。
		for _, tgt := range nearby {
			if tgt.CharID == player.CharID {
				continue
			}
			if isGMInvisible(tgt) {
				continue
			}
			if tgt.HasBuff(60) || tgt.HasBuff(97) {
				s.removeBuffAndRevert(tgt, 60)
				s.removeBuffAndRevert(tgt, 97)
				// 通知被揭示者：你不再隱身
				handler.SendInvisible(tgt.Session, tgt.CharID, false)
				// 向附近玩家廣播角色出現（讓被揭示者重新顯示在其他人畫面上）
				nearbyOfTarget := s.deps.World.GetNearbyPlayersInShow(tgt.X, tgt.Y, tgt.MapID, 0, tgt.ShowID)
				for _, viewer := range nearbyOfTarget {
					if viewer.CharID != tgt.CharID {
						handler.SendPutObject(viewer.Session, tgt)
					}
				}
			}
		}
		// 施法者自己若在隱身中也會被揭示
		if !isGMInvisible(player) && (player.HasBuff(60) || player.HasBuff(97)) {
			s.removeBuffAndRevert(player, 60)
			s.removeBuffAndRevert(player, 97)
			handler.SendInvisible(sess, player.CharID, false)
		}
		if s.deps.Trap != nil {
			s.deps.Trap.DetectTraps(sess, player)
		}

	case 44: // 魔法相消術（自身）
		s.cancelAllBuffs(player)

	case 60: // 隱身術 — 施法者隱形，直到攻擊/施法/使用道具時解除
		player.Invisible = true
		invisBuff := &world.ActiveBuff{
			SkillID:      skill.SkillID,
			TicksLeft:    3600 * 5, // 永久直到行動解除（cancelInvisibility）
			SetInvisible: true,
		}
		old60 := player.AddBuff(invisBuff)
		if old60 != nil {
			s.revertBuffStats(player, old60)
		}
		// 通知施法者已隱身
		handler.SendInvisible(sess, player.CharID, true)
		// 從附近所有玩家畫面移除（Java: S_RemoveObject）
		removeData := handler.BuildRemoveObject(player.CharID)
		for _, viewer := range nearby {
			if viewer.CharID != player.CharID {
				viewer.Session.Send(removeData)
			}
		}

	case 97: // 暗隱術 — 黑妖隱形（與 60 同分支），duration 由 yaml buff_duration 決定
		// Java `L1SkillUse2.java:2511-2514` 把 INVISIBILITY 與 BLIND_HIDING 放在同一個 if：
		// `pc.sendPackets(new S_Invis(pc.getId(), 1))` + `pc.broadcastPacketAll(new S_RemoveObject(pc))`。
		// Go buff 屬性走 applyBuffEffect（buffs.lua `[97] = { invisible = true }` + yaml 32s），
		// 此處只需補 self-packet 與 RemoveObject 廣播；否則施法者 UI 不切換、附近玩家畫面不移除。
		handler.SendInvisible(sess, player.CharID, true)
		removeData97 := handler.BuildRemoveObject(player.CharID)
		for _, viewer := range nearby {
			if viewer.CharID != player.CharID {
				viewer.Session.Send(removeData97)
			}
		}

	case 78: // 絕對屏障 — 免疫所有傷害，停止 HP/MP 回復
		// Java: 攻擊/施法/使用道具/裝備武器時解除；移動時不解除
		player.AbsoluteBarrier = true
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 12
		}
		abBuff := &world.ActiveBuff{
			SkillID:            skill.SkillID,
			TicksLeft:          dur * 5,
			SetAbsoluteBarrier: true,
		}
		old78 := player.AddBuff(abBuff)
		if old78 != nil {
			s.revertBuffStats(player, old78)
		}
		// 廣播屏障特效（Java: castGfx 2234）
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
		}

	case 161: // 封印禁地 — 對附近玩家套用沉默，施法者不受影響
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
		s.applyAreaOfSilence(player, skill, nearby)
		return

	case 90: // 堅固防護 — Java 需要盾牌/臂甲，效果為 ER +15
		if !s.validateSolidCarriage(player) {
			// Java `SOLID_CARRIAGE.start()` 第 20/28 行送 `S_ServerMessage("你並未裝備盾牌")`
			// 而非 standard msg 280 "施展魔法失敗"，給玩家明確的盾牌缺失回饋。
			handler.SendNormalChat(sess, 0, "你並未裝備盾牌")
			return
		}

	case 130: // 心靈轉換 — 恢復 2 MP（Java: BODY_TO_MIND +2）
		// Java skillmode/BODY_TO_MIND.java:16 `setCurrentMp(currentMp + 2)`，
		// L1PcInstance.setCurrentMp 內建 MaxMp clamp + S_MPUpdate（已對齊 Go sendMpUpdate）。
		player.MP += 2
		if player.MP > player.MaxMP {
			player.MP = player.MaxMP
		}
		sendMpUpdate(sess, player)
		// Java `L1SkillUse.sendGrfx` 第 1681 行 default 分支對 target='none' + type=buff 走
		// `_player.sendPacketsAll(new S_SkillSound(self.id, cast_gfx))` 廣播施法效果（cast_gfx=2179）。
		// 後置 BuildActionGfx 已負責 S_DoActionGFX(action_id=19)，這裡補上 cast_gfx 廣播。
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
		}

	case 146: // 魂體轉換 — Java `BLOODY_SOUL.start()` 第 19 行
		// `setCurrentMp(currentMp + ConfigElfSkill.BLOODY_SOULADDMP)`，
		// yiwei `各職業技能相關設置.properties: BLOODY_SOULADDMP = 20`（不是 skill.skill_level=19）。
		player.MP += 20
		if player.MP > player.MaxMP {
			player.MP = player.MaxMP
		}
		sendMpUpdate(sess, player)
		// Java `L1SkillUse.sendGrfx:1681` default 分支對 target='none' + type=buff 送
		// `_player.sendPacketsAll(new S_SkillSound(self.id, cast_gfx=2178))` 廣播施法效果。
		// 後置 BuildActionGfx 已負責 S_DoActionGFX(action_id=19)，這裡補上 cast_gfx 廣播
		// （同 130 BODY_TO_MIND precedent）。
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
		}

	case 186: // 血之渴望 — 自身 buff + 勇敢速度（Java: BLOODLUST.java）
		// 與暴風疾走/聖潔之行等互斥（由 buffs.lua 無 exclusions，brave_speed 會覆蓋）
		s.applyBuffEffect(player, skill)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
		}
		handler.SendPlayerStatus(player.Session, player)
		if skill.SysMsgHappen > 0 {
			handler.SendServerMessage(sess, uint16(skill.SysMsgHappen))
		}

	case 172: // 暴風疾走
		for _, conflictID := range []int32{
			handler.SkillStatusBrave, handler.SkillStatusElfBrave,
			52,  // HOLY_WALK
			101, // MOVING_ACCELERATION
			150, // WIND_WALK
			155, // FIRE_BLESS
			186, // BLOOD_LUST
		} {
			s.removeBuffAndRevert(player, conflictID)
		}
		stormBuff := &world.ActiveBuff{
			SkillID:       172,
			TicksLeft:     300 * 5,
			SetBraveSpeed: 4,
		}
		old172 := player.AddBuff(stormBuff)
		if old172 != nil {
			s.revertBuffStats(player, old172)
		}
		player.BraveSpeed = 4
		player.BraveTicks = stormBuff.TicksLeft
		s.sendSpeedToAll(player, 4, 300)
	}

	// 廣播施法動畫
	if !isSelfAreaAttackSkill(skill) {
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
	}

	// 自身範圍治療
	if skill.Type == 16 && (skill.DamageValue > 0 || skill.DamageDice > 0) {
		casterINT := int(player.Intel)
		casterLawful := int(player.Lawful)

		if skill.Area == -1 {
			heal := int32(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterLawful, 10))
			heal = s.applyElfWaterHealingModifiers(player, heal)
			applyPlayerHealDeltaLikeJava(s.deps, player, heal)
			// Java L1SkillUse.isInTarget() 877-880：target_to=8 (TARGET_TO_PARTY) 只對隊伍成員生效，自己永遠通過 671-676。
			// 目前僅 164 NATURES_BLESSING 屬 target_to=8 + type=16；非隊員 nearby 不應受惠。
			var partyMembers map[int32]bool
			if skill.TargetTo == 8 {
				if party := s.deps.World.Parties.GetParty(player.CharID); party != nil {
					partyMembers = make(map[int32]bool, len(party.Members))
					for _, mid := range party.Members {
						partyMembers[mid] = true
					}
				}
			}
			for _, p := range nearby {
				if p.SessionID == sess.ID {
					continue
				}
				if skill.TargetTo == 8 && (partyMembers == nil || !partyMembers[p.CharID]) {
					continue
				}
				h := int32(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterLawful, 10))
				h = s.applyElfWaterHealingModifiers(p, h)
				applyPlayerHealDeltaLikeJava(s.deps, p, h)
			}
		} else {
			heal := int32(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterLawful, 10))
			heal = s.applyElfWaterHealingModifiers(player, heal)
			applyPlayerHealDeltaLikeJava(s.deps, player, heal)
		}
	}

	// 自身範圍 AoE 傷害（龍捲風 53、震裂術 62、冰雪颶風 80 等）
	if isSelfAreaAttackSkill(skill) {
		s.executeSelfAreaAttackSkill(sess, player, skill, nearby)
		return
	}

	// Owner: skill_clan.go
	if s.applyRoyalAuraSkill(player, skill, nearby) {
		return
	}

	// 套用 buff 效果
	// 重施守衛清單（Java skillmode 有 `if (!hasSkillEffect(X))` 包住 stat add + timer set 的技能）：
	//   - UNCANNY_DODGE (106)：`skillmode/UNCANNY_DODGE.java:17` 跳過 add_dodge(5)+timer+S_PacketBoxIcon1。
	//   - BOUNCE_ATTACK (89)：`skillmode/BOUNCE_ATTACK.java:13` 跳過 addHitup(6)+timer。
	// 重施時保留外層 cast GFX 廣播 + MP 消耗（與 Java handleCommands 一致），但 buff 內容不變。
	if !((skill.SkillID == 106 || skill.SkillID == 89) && player.HasBuff(skill.SkillID)) {
		s.applyBuffEffect(player, skill)
	}

	// 負重強化：套用時立即更新負重顯示
	if skill.SkillID == 14 || skill.SkillID == 218 {
		handler.SendWeightUpdate(sess, player)
	}

	// 效果 GFX
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
	}

	if skill.SysMsgHappen > 0 {
		handler.SendServerMessage(sess, uint16(skill.SysMsgHappen))
	}
	s.sendYiweiPostCastStatusRefresh(player)
}
