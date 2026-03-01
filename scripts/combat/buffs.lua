-- Buff definitions for all skills
-- Each entry: stat deltas, mutual exclusion list, and special flags
-- Go engine calls get_buff_effect(skill_id, target_level) and applies returned deltas
--
-- Keys match ActiveBuff delta field names:
--   ac, str, dex, con, wis, intel, cha
--   max_hp, max_mp, hit_mod, dmg_mod, sp, mr, hpr, mpr
--   bow_hit, bow_dmg, dodge
--   fire_res, water_res, wind_res, earth_res
--   exclusions = {skill_ids to remove first}
--   move_speed = 1(haste) / 2(slow)
--   brave_speed = 4(brave/holy walk)
--   invisible, paralyzed, sleeped = true

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

    [26] = { dex = 5 },                                                      -- Physical Enchant DEX

    [29] = { move_speed = 2, exclusions = {43, 54} },                        -- Slow

    [31] = { ac = -2 },                                                      -- Magic Shield
    [32] = { mpr = 5 },                                                      -- Meditation
    [33] = {},                                                                -- Mummy's Curse (debuff flag)
    [36] = {},                                                                -- Charm (flag only)
    [40] = {},                                                                -- Darkness (blind debuff)

    [42] = { str = 5 },                                                      -- Physical Enchant STR

    [43] = { move_speed = 1, exclusions = {29, 76, 54} },                    -- Haste

    [47] = { dmg_mod = -5, hit_mod = -1 },                                     -- Weakness 弱化術 (debuff)

    [50] = { paralyzed = true },                                              -- Ice Lance (freeze)
    [52] = { brave_speed = 4 },                                               -- Holy Walk
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

    [80] = { paralyzed = true },                                              -- Freezing Blizzard

    -- ==================== Dark Elf Skills (97-108) ====================

    [97]  = { invisible = true },                                             -- Dark Invisibility
    [98]  = {},                                                               -- Venom (poison enchant flag)
    [99]  = { ac = -3, exclusions = {3, 21, 24} },                           -- DE Shadow Armor

    [101] = { brave_speed = 4 },                                              -- Moving Acceleration
    [102] = { sp = 2, hit_mod = 3 },                                          -- Burning Spirit
    [103] = {},                                                               -- Dark Blind (debuff flag)
    [104] = {},                                                               -- Poison Resist (flag)
    [105] = { dmg_mod = 4 },                                                  -- Double Break
    [106] = { dodge = 5 },                                                    -- Shadow Dodge
    [107] = { dmg_mod = 5 },                                                  -- Shadow Fang

    -- ==================== Knight/Royal Skills (87-91, 109-118) ====================

    [87]  = { paralyzed = true },                                             -- Shock Stun
    [88]  = { ac = -4 },                                                      -- Reduction Armor（增幅防禦）
    [89]  = { hit_mod = 6 },                                                  -- Spiked Armor（尖刺盔甲，HIT+6 + PvP 武器破壞）
    [90]  = { ac = -2 },                                                      -- Solid Carriage（堅固防護）
    [91]  = { ac = -2 },                                                      -- Counter Barrier（反擊屏障）— AC-2 + 近戰反彈

    [109] = { str = 1 },                                                      -- Dress Mighty
    [110] = { dex = 1 },                                                      -- Dress Dexterity
    [111] = { dodge = 5, ac = -4 },                                           -- Reduction Armor
    [113] = { hit_mod = 3 },                                                  -- Accurate Target

    [114] = { hit_mod = 5, bow_hit = 5, mr = 20, exclusions = {115, 117} },  -- Glowing Aura
    [115] = { ac = -8, exclusions = {114, 117} },                             -- Shining Aura
    [117] = { dmg_mod = 5, exclusions = {114, 115} },                         -- Brave Aura
    [118] = {},                                                               -- Guard Ally (flag)

    -- ==================== Elf Skills (129-176) ====================

    [129] = { mr = 10 },                                                      -- Resist Magic
    [133] = { fire_res = -20, water_res = -20, wind_res = -20, earth_res = -20 }, -- Weaken Element
    [134] = {},                                                               -- Mirror Reflect (flag)
    [137] = { wis = 3 },                                                      -- Clear Mind
    [138] = { fire_res = 10, water_res = 10, wind_res = 10, earth_res = 10 }, -- Resist Elemental
    [147] = { fire_res = 30, water_res = 30, wind_res = 30, earth_res = 30 }, -- Elemental Protection

    [148] = { dmg_mod = 4, exclusions = {163} },                              -- Fire Weapon
    [149] = { bow_hit = 6, exclusions = {166} },                              -- Wind Shot
    [150] = { brave_speed = 4 },                                              -- Wind Walk

    [151] = { ac = -6, exclusions = {3, 21, 24, 99, 159, 168} },             -- Earth Skin
    [152] = { move_speed = 2, exclusions = {43, 54} },                        -- Entangle (slow)

    [155] = { bow_hit = 2, bow_dmg = 3 },                                    -- Storm Eye (party)
    [156] = { bow_hit = 2, bow_dmg = 3 },                                    -- Eye of Storm
    [157] = { paralyzed = true },                                              -- Earth Barrier
    [158] = { hpr = 4 },                                                      -- Spring of Life

    [159] = { ac = -7, exclusions = {3, 21, 24, 151, 168} },                 -- Earth Bless
    [160] = { ac = -2, water_res = 30 },                                      -- Water Protection

    [163] = { dmg_mod = 6, hit_mod = 3, exclusions = {148} },                -- Burning Weapon
    [166] = { bow_dmg = 5, bow_hit = -1, exclusions = {149} },               -- Storm Shot
    [167] = {},                                                               -- Wind Shackle (flag)

    [168] = { ac = -10, exclusions = {3, 21, 24, 151, 159} },                -- Iron Skin
    [169] = { str = 5 },                                                      -- Physical Power (Elf)
    [170] = { hpr = 3, mpr = 1 },                                            -- Aqua Vitality
    [171] = { dmg_mod = 3 },                                                  -- Elemental Fire
    [173] = {},                                                               -- Pollute Water (debuff flag)
    [174] = { bow_hit = 3, bow_dmg = 3 },                                    -- True Target
    [175] = { sp = 2, intel = 2 },                                            -- Flame of Soul
    [176] = { str = 2, dex = 2 },                                            -- Elemental Energy

    -- ==================== Dragon Knight Skills (181-195) ====================

    [181] = { ac = -5 },                                                      -- Dragon Armor
    [182] = { dmg_mod = 5 },                                                  -- Burning Slash
    [183] = { ac = 5, dmg_mod = -3 },                                        -- Guard Break (debuff)

    [185] = { str = 3, con = 3, hpr = 3 },                                   -- Awaken Antharas
    [186] = { dmg_mod = 6, hit_mod = 3, ac = 5 },                            -- Blood Lust
    [188] = { str = -5, intel = -5 },                                         -- Horror of Death (debuff)
    [189] = { ac = -5 },                                                      -- Shock Skin (+ reflect flag)
    [190] = { intel = 3, wis = 3, mpr = 3 },                                 -- Awaken Fafurion
    [191] = { brave_speed = 4 },                                              -- Underground Path
    [193] = { str = -1, con = -1, dex = -1, wis = -1, intel = -1 },          -- Fear (debuff)
    [195] = { str = 5, max_hp = 100 },                                       -- Awaken Valakas

    -- ==================== Illusionist Skills (201-220) ====================

    [201] = { dodge = 5 },                                                    -- Mirror Image

    [204] = { dmg_mod = 4, hit_mod = 4, bow_dmg = 4, bow_hit = 4,           -- Illusion Ogre
              exclusions = {209, 214, 219} },
    [206] = { mpr = 2 },                                                      -- Concentration

    [209] = { sp = 2, exclusions = {204, 214, 219} },                        -- Illusion Lich
    [211] = { hpr = 5 },                                                      -- Patience
    [212] = { paralyzed = true },                                              -- Phantasm

    [214] = { ac = -20, exclusions = {204, 209, 219} },                      -- Illusion Diamond Golem

    [216] = { str = 1, con = 1, dex = 1, wis = 1, intel = 1 },              -- Insight
    [217] = { str = -1, con = -1, dex = -1, wis = -1, intel = -1 },         -- Panic (debuff)

    [219] = { dmg_mod = 10, bow_dmg = 10, exclusions = {204, 209, 214} },   -- Illusion Avatar
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
    [33]  = true,  -- Curse Paralyze（木乃伊詛咒）
    [78]  = true,  -- Absolute Barrier（絕對屏障）
    [112] = true,  -- Armor Break（破壞盔甲）
    [157] = true,  -- Earth Bind（大地屏障）
    [208] = true,  -- Bone Break（骷髏毀壞）
    [226] = true,  -- Gigantic（巨人化）
    [228] = true,  -- Power Grip（力量支配）
    [230] = true,  -- Desperado（亡命之徒）

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
