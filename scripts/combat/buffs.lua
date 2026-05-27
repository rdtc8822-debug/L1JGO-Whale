-- Buff definitions for all skills
-- Each entry: stat deltas, mutual exclusion list, and special flags
-- Go engine calls get_buff_effect(skill_id, target_level) and applies returned deltas
--
-- Keys match ActiveBuff delta field names:
--   ac, str, dex, con, wis, intel, cha
--   max_hp, max_mp, hit_mod, dmg_mod, sp, mr, hpr, mpr
--   bow_hit, bow_dmg, dodge
--   weight_pct
--   fire_res, water_res, wind_res, earth_res
--   exclusions = {skill_ids to remove first}
--   move_speed = 1(haste) / 2(slow)
--   brave_speed = 4(brave/holy walk)
--   invisible, paralyzed, sleeped = true

local DRAGON_EYE_EXCLUSIONS = {6683, 6684, 6685, 6686, 6687, 6688, 6689}

BUFF_DEFS = {
    -- ==================== Wizard Spells (1-80) ====================

    -- AC Buffs (mutual exclusion: Shield <-> Shadow Armor <-> Blessed Armor)
    [3]  = { ac = -2, exclusions = {24, 21} },                              -- Shield
    [21] = { ac = -3, exclusions = {3, 24} },                               -- Blessed Armor
    [24] = { ac = -3, exclusions = {3, 21} },                               -- Shadow Armor

    -- Weapon enchant buffs (mutual exclusion group)
    [8]  = { dmg_mod = 1, hit_mod = 1, exclusions = {12, 48} },             -- Holy Weapon
    [12] = { dmg_mod = 2, hit_mod = 2, exclusions = {8, 48} },              -- Enchant Weapon
    [48] = { dmg_mod = 3, hit_mod = 3, exclusions = {8, 12} },              -- Blessed Weapon

    [14] = {},                                                                -- Extra Weight (flag only)
    [20] = {},                                                                -- Curse Blind (debuff flag)

    [26] = { dex = 5, exclusions = {110} },                                  -- Physical Enchant DEX（Java REPEATEDSKILLS[4]={26,110} 與 DRESS_DEXTERITY 互斥）

    [29] = { move_speed = 2, exclusions = {43, 54} },                        -- Slow

    [31] = {},                                                               -- Counter Magic (state only)
    [32] = { mpr = 5 },                                                      -- Meditation
    [33] = {},                                                                -- Mummy's Curse (debuff flag)
    [36] = {},                                                                -- Charm (flag only)
    [40] = {},                                                                -- Darkness (blind debuff)

    [42] = { str = 5, exclusions = {109} },                                  -- Physical Enchant STR（Java REPEATEDSKILLS[5]={42,109} 與 DRESS_MIGHTY 互斥）

    [43] = { move_speed = 1, exclusions = {29, 76, 54} },                    -- Haste

    [47] = { dmg_mod = -5, hit_mod = -1 },                                     -- Weakness 弱化術 (debuff)

    [50] = { paralyzed = true },                                              -- Ice Lance (freeze)
    [52] = { brave_speed = 4, exclusions = {101, 150, 155, 186} },            -- Holy Walk
    [54] = { move_speed = 1, exclusions = {43, 29, 76} },                    -- Greater Haste

    [55] = { hit_mod = 2, dmg_mod = 5, ac = 10 },                            -- Berserker
    [56] = { dmg_mod = -6, ac = 12 },                                         -- Disease 疾病術 (debuff: DMG-6, AC+12)

    [60] = { invisible = true },                                              -- Invisibility

    [63] = { hpr = 3 },                                                      -- Heal of Energy Storm
    [64] = {},                                                                -- Magic Seal (flag)
    [66] = { sleeped = true },                                                -- Fog of Sleeping
    [67] = {},                                                                -- Polymorph (visual only)
    [68] = {},                                                                -- Turn Undead Field
    [71] = {},                                                                -- Potion Freeze (flag)

    [76] = { move_speed = 2, exclusions = {43, 54} },                        -- Mass Slow

    [78] = {},                                                                -- Absolute Barrier (flag)

    -- Advance Spirit: level-dependent (handled dynamically below)
    [79] = { _dynamic = true },

    -- [80] 冰雪颶風：凍結只作用於 NPC 目標（skill.go executeSelfSkill 中處理），不作用於施法者

    -- ==================== Dark Elf Skills (97-108) ====================

    [97]  = { invisible = true },                                             -- Dark Invisibility
    [98]  = {},                                                               -- Venom (poison enchant flag)
    [99]  = { mr = 5 },                                                       -- Shadow Armor（Java: MR +5）

    [101] = { brave_speed = 4, exclusions = {52, 150, 155, 186, 1000, 1016} }, -- Moving Acceleration
    [102] = {},                                                               -- Burning Spirit（觸發型增傷旗標）
    [103] = {},                                                               -- Dark Blind (debuff flag)
    [104] = {},                                                               -- Poison Resist (flag)
    [105] = {},                                                               -- Double Break（觸發型增傷旗標）
    [106] = { dodge = 5 },                                                    -- Shadow Dodge
    [107] = { dmg_mod = 5 },                                                  -- Shadow Fang
    [112] = {},                                                               -- Armor Break（破壞盔甲，旗標型 debuff：傷害倍率在 Go 戰鬥系統中處理）

    -- ==================== Knight/Royal Skills (87-91, 109-118) ====================

    [87]  = { paralyzed = true },                                             -- Shock Stun
    [88]  = {},                                                               -- Reduction Armor（增幅防禦）— Java 是 flat 傷害減免（npc_ai/pvp/magic 路徑套用 applyReductionArmorDamage），不是 AC 加成
    [89]  = { hit_mod = 6 },                                                  -- Spiked Armor（尖刺盔甲，HIT+6 + PvP 武器破壞）
    [90]  = { dodge = 15 },                                                   -- Solid Carriage（堅固防護，Java: ER +15）
    [91]  = { ac = -2 },                                                      -- Counter Barrier（反擊屏障）— AC-2 + 近戰反彈

    [109] = { str = 3, exclusions = {42} },                                  -- Dress Mighty（Java REPEATEDSKILLS[5]={42,109} 與 PHYSICAL_ENCHANT_STR 互斥）
    [110] = { dex = 3, exclusions = {26} },                                  -- Dress Dexterity（Java REPEATEDSKILLS[4]={26,110} 與 PHYSICAL_ENCHANT_DEX 互斥）
    [111] = { dodge = 18 },                                                   -- Dress Evasion（Java: ER +18）
    [113] = {},                                                               -- True Target（精準目標，傷害加成在 Go PvP/戰鬥路徑處理）

    -- Royal Auras 114/115/117：Java `L1SkillUse.java:2421-2438` 三技能無互斥邏輯，REPEATEDSKILLS 10 群均不含此三者，
    -- Java 三王族光環可同時掛在同一玩家身上；2026-05-19 117 audit 移除原本不對稱的三向 exclusions。
    [114] = { hit_mod = 5, dmg_mod = 5 },                                     -- Glowing Aura
    [115] = { ac = -8 },                                                      -- Shining Aura
    [117] = {},                                                               -- Brave Aura（純機率旗標，33% 物理傷害 1.5 倍 via braveAuraDamage helper）
    [118] = {},                                                               -- Guard Ally (flag)

    -- ==================== Elf Skills (129-176) ====================

    [129] = { mr = 10 },                                                      -- Resist Magic
    [133] = {},                                                               -- Weaken Element（Go 依施法者 ElfAttr 動態降低單一屬性 -50）
    [134] = {},                                                               -- Mirror Reflect (flag)
    [137] = { wis = 3 },                                                      -- Clear Mind
    [138] = { fire_res = 10, water_res = 10, wind_res = 10, earth_res = 10 }, -- Resist Elemental
    [147] = {},                                                               -- Elemental Protection（Go 依 ElfAttr 動態加單一屬性 +50）

    [148] = { dmg_mod = 4, exclusions = {149, 156, 163, 166} },               -- Fire Weapon（Java REPEATEDSKILLS[0]={148,149,156,163,166}）
    [149] = { bow_hit = 6, exclusions = {148, 156, 163, 166} },               -- Wind Shot（Java REPEATEDSKILLS[0]={148,149,156,163,166}）
    [150] = { brave_speed = 4, exclusions = {52, 101, 155, 186, 1000, 1016} },-- Wind Walk（Java REPEATEDSKILLS[2]={52,101,150,1000,1016,186,155}）

    [151] = { ac = -6, exclusions = {168} },                                 -- Earth Skin（Java REPEATEDSKILLS[1]={151,168} 只與 168 互斥）
    [152] = { move_speed = 2, exclusions = {43, 54} },                        -- Entangle (slow)

    [155] = { brave_speed = 1, exclusions = {52, 101, 150, 186, 1000, 1016} },-- Fire Bless（烈炎氣息，Java REPEATEDSKILLS[2]={52,101,150,1000,1016,186,155}）
    [156] = { bow_hit = 2, bow_dmg = 3, exclusions = {148, 149, 163, 166} }, -- Eye of Storm（Java REPEATEDSKILLS[0]={148,149,156,163,166}）
    [157] = { paralyzed = true },                                              -- Earth Barrier
    [158] = { hpr = 15 },                                                     -- Spring of Life（Java HprExecutor.java:55 NATURES_TOUCH=15，每 tick +15 HPR）

    [159] = {},                                                               -- Earth Bless（義維 Java 只送盾牌圖示 S_SkillIconShield(7,...)，不在 REPEATEDSKILLS 任何群組）
    [160] = { dodge = 5 },                                                    -- Water Protection（Java getEr: ER +5）
    [161] = {},                                                               -- Area of Silence（Go 範圍沉默旗標）

    [163] = { dmg_mod = 6, hit_mod = 3, exclusions = {148, 149, 156, 166} }, -- Burning Weapon（Java REPEATEDSKILLS[0]={148,149,156,163,166}）
    [166] = { bow_dmg = 5, bow_hit = -1, exclusions = {148, 149, 156, 163} },-- Storm Shot（Java REPEATEDSKILLS[0]={148,149,156,163,166}）
    [167] = {},                                                               -- Wind Shackle (flag)

    [168] = { ac = -10, exclusions = {151} },                                -- Iron Skin（Java REPEATEDSKILLS[1]={151,168} 只與 EARTH_SKIN 互斥）
    [169] = {},                                                               -- Exotic Vitalize（體能激發；Java 僅負重 HP/MP 回復旗標，無屬性加成）
    [170] = {},                                                               -- Water Life（下一次治療加倍後移除）
    [171] = {},                                                               -- Elemental Fire（近戰觸發型增傷）
    [173] = {},                                                               -- Pollute Water (debuff flag)
    [174] = {},                                                               -- Striker Gale（遠程承傷加成旗標）
    [175] = {},                                                               -- Soul of Flame（近戰增傷旗標）
    [176] = {},                                                               -- Additional Fire（能量激發；Java 僅負重 HP/MP 回復旗標，無屬性加成）

    -- ==================== Dragon Knight Skills (181-195) ====================

    [181] = { ac = -5 },                                                      -- Dragon Armor
    [182] = {},                                                               -- Burning Slash（燃燒擊砍；Java 一次性 +10 + 消耗 buff，於 burningSlashDamage 處理，無 passive 屬性）
    [183] = { ac = 10 },                                                      -- Guard Brake（護衛毀滅；Java L1SkillUse2:2271-2275 cast addAc(10), L1SkillStop:669-673 stop addAc(-10), 無 DmgMod 影響）

    [185] = { ac = -3, regist_sustain = 10, exclusions = {190, 195} },         -- Awaken Antharas（安塔瑞斯覺醒：AC-3, 持傷抗+10）
    [186] = { brave_speed = 1,
              exclusions = {52, 101, 150, 155, 1000, 1016} },                -- Blood Lust（血之渴望：純勇敢速度+1，對齊 Java skillmode/BLOODLUST.java 只 setBraveSpeed(1)；exclusions 對齊 L1BuffUtil.braveStart() 清單 {52,101,150,1000,1016,155}）
    [188] = { dodge = -5 },                                                   -- Resist Fear
    -- 189 無 buff effect — Java codebase 對 SHOCK_SKIN(189) 唯一引用是 `L1BuffUtil.java:59 if (hasSkillEffect(SHOCK_SKIN)) 阻擋傳送卷軸`，
    -- 但 yiwei/fly skills.sql 第 189 列實際為 `岩漿之箭`（type=64 attack），沒有任何 Java 路徑會 setSkillEffect(189)，是 L1BuffUtil 的 dead code。
    -- Go yaml `[189] type=64 area=2 damage_value=45` 走 `executeSelfAreaAttackSkill` 自身 AOE 攻擊路徑（已 return 不到 applyBuffEffect），
    -- 原本的 `[189] = { ac = -5 }` 為 dead entry，移除避免誤導後續對齊。
    [190] = { regist_freeze = 10, exclusions = {185, 195} },                  -- Awaken Fafurion（法利昂覺醒：凍結抗+10）
    [191] = {},                                                               -- Mortal Body（致命身軀：純旗標，無屬性加成；Java L1PcInstance:2776 在 receiveDamage 內 23% 機率反彈 40 傷害）
    [193] = { str = -3, intel = -3 },                                         -- 驚悚死神 HORROR_OF_DEATH（Java L1SkillUse:2290/L1SkillUse2:2277：對 PC addStr(-3)+addInt(-3)，L1SkillStop case 193 解除時 +3 還原）
    [195] = { hit_mod = 5, regist_stun = 10, exclusions = {185, 190} },       -- Awaken Valakas（巴拉卡斯覺醒：HIT+5, 暈眩抗+10）

    -- ==================== Illusionist Skills (201-220) ====================

    [201] = { dodge = 5 },                                                    -- Mirror Image

    [204] = { dmg_mod = 4, hit_mod = 4 },                                     -- 幻覺：歐吉 ILLUSION_OGRE（Java L1SkillUse:2660-2664 only addDmgup(+4)+addHitup(+4)；無 bow 修正、無 REPEATEDSKILLS 互斥群——Java 允許四個 illusion buff 並存）
    [206] = { mpr = 2 },                                                      -- Concentration

    [209] = { sp = 2 },                                                       -- 幻覺：巫妖 ILLUSION_LICH（Java skillmode/ILLUSION_LICH.java:19-32 只檢查 !hasSkillEffect(209)、無 REPEATEDSKILLS 互斥群——Java 允許四個 illusion buff 並存）
    [211] = { hpr = 5 },                                                      -- Patience
    [212] = { sleeped = true },                                                -- Phantasm

    [214] = { ac = -8 },                                                      -- 幻覺：鑽石高崙 ILLUSION_DIA_GOLEM（Java L1SkillUse:2665-2668 only pc.addAc(-8)；無 REPEATEDSKILLS 互斥群——Java 允許四個 illusion buff 並存）

    [216] = { str = 1, con = 1, dex = 1, wis = 1, intel = 1 },              -- Insight
    [217] = { str = -1, con = -1, dex = -1, wis = -1, intel = -1 },         -- Panic (debuff)

    [219] = { dmg_mod = 10, bow_dmg = 10 },                                  -- Illusion Avatar（Java skillmode/ILLUSION_AVATAR.java:28-31 only addDmgup(+10)+addBowDmgup(+10)+setAvatar；無 REPEATEDSKILLS 互斥群——Java 允許四個 illusion buff 並存。setAvatar 用於 dmg-=(dmg*Avatar/100) 但 Java default ILLUSION_AVATAR_DAMAGE=1 → 整數除 1/100=0 等同 dead-code，Go 不實作）

    -- ==================== ADLV80 龍副本祝福 (4009/4010/4018) ====================

    [4009] = { hpr = 3, mpr = 3, max_hp = 100, max_mp = 50, earth_res = 30,
               dmg_mod = 1, bow_dmg = 1, hit_mod = 5, bow_hit = 5, weight_pct = 4 }, -- 卡瑞的祝福（地龍副本）
    [4010] = { hpr = 8, mpr = 1, max_hp = 80, max_mp = 10, bow_hit = 6,
               bow_dmg = 3, water_res = 30, ac = -8 },                       -- 莎爾的祝福（水龍副本）
    [4018] = { dex = 5, bow_hit = 7, bow_dmg = 5, hpr = 10, mpr = 3,
               max_hp = 100, max_mp = 40, wind_res = 30, mr = 15 },          -- 甘特的祝福（風龍副本）

    -- ==================== Dragon Eye 龍之眼 (6683-6689) ====================

    [6683] = { dmg_mod = 2, regist_stun = 3, exclusions = DRAGON_EYE_EXCLUSIONS }, -- 火龍之眼（Valakas）
    [6684] = { regist_stone = 3, dodge = 1, exclusions = DRAGON_EYE_EXCLUSIONS }, -- 地龍之眼（Antharas）
    [6685] = { regist_freeze = 3, exclusions = DRAGON_EYE_EXCLUSIONS },       -- 水龍之眼（Fafurion）
    [6686] = { regist_sleep = 3, magic_critical = 2, exclusions = DRAGON_EYE_EXCLUSIONS }, -- 風龍之眼（Lindvior）
    [6687] = { dmg_mod = 2, dodge = 1, magic_critical = 2, exclusions = DRAGON_EYE_EXCLUSIONS }, -- 生命之眼（Life）
    [6688] = { regist_blind = 3, dodge = 1, exclusions = DRAGON_EYE_EXCLUSIONS }, -- 誕生之眼（Birth）
    [6689] = { regist_sustain = 3, magic_critical = 2, dodge = 1, exclusions = DRAGON_EYE_EXCLUSIONS }, -- 形象之眼（Figure）
}

---------------------------------------------------------------------
-- get_buff_effect(skill_id, target_level)
-- Returns buff definition table (stat deltas + exclusions + flags)
-- Returns nil for unknown buffs (Go will create a generic timer buff)
---------------------------------------------------------------------
function get_buff_effect(skill_id, target_level)
    local def = BUFF_DEFS[skill_id]
    if not def then
        return nil
    end

    -- Copy the definition so we don't mutate the original
    local result = {}
    for k, v in pairs(def) do
        if k ~= "_dynamic" then
            result[k] = v
        end
    end

    -- Handle exclusions array (need to copy)
    if def.exclusions then
        local exc = {}
        for i, v in ipairs(def.exclusions) do
            exc[i] = v
        end
        result.exclusions = exc
    end

    -- Dynamic level-dependent buffs
    if def._dynamic then
        if skill_id == 79 then  -- Advance Spirit: MaxHP + Level/5, MaxMP + Level/5
            local bonus = math.max(1, math.floor(target_level / 5))
            result.max_hp = bonus
            result.max_mp = bonus
        end
    end

    return result
end

---------------------------------------------------------------------
-- Non-cancellable skill IDs (used by Cancellation/Dispel)
---------------------------------------------------------------------
NON_CANCELLABLE = {
    -- 法師系
    [12]  = true,  -- Enchant Weapon（武器強化）
    [21]  = true,  -- Blessed Armor（鎧甲護持）
    [79]  = true,  -- Advance Spirit（靈魂昇華）

    -- 騎士系
    [87]  = true,  -- Shock Stun（衝擊之暈）
    [88]  = true,  -- Reduction Armor（增幅防禦）
    [89]  = true,  -- Bounce Attack（尖刺盔甲）
    [90]  = true,  -- Solid Carriage（堅固防護）
    [91]  = true,  -- Counter Barrier（反擊屏障）

    -- 暗黑妖精系
    [99]  = true,  -- Shadow Armor（暗影防護）
    [106] = true,  -- Uncanny Dodge（暗影閃避）
    [107] = true,  -- Shadow Fang（暗影之牙）

    -- 王族系
    [109] = true,  -- Dress Mighty（力量提升）
    [110] = true,  -- Dress Dexterity（敏捷提升）
    [111] = true,  -- Dress Evasion（迴避提升）

    -- 特殊 debuff（不可被相消術解除）
    [112] = true,  -- Armor Break（破壞盔甲）
    [208] = true,  -- Bone Break（骷髏毀壞）
    -- 龍騎士覺醒
    [185] = true,  -- Awaken Antharas（安乘覺醒）
    [190] = true,  -- Awaken Fafurion（法利昂覺醒）
    [195] = true,  -- Awaken Valakas（巴拉卡斯覺醒）

    -- 幻術師幻象
    [204] = true,  -- Illusion Ogre（幻覺：歐吉）
    [209] = true,  -- Illusion Lich（幻覺：巫妖）
    [214] = true,  -- Illusion Dia Golem（幻覺：鑽石乙魔）
    [219] = true,  -- Illusion Avatar（幻覺：化身）
}

function is_non_cancellable(skill_id)
    return NON_CANCELLABLE[skill_id] == true
end
