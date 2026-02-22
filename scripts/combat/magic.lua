-- Skill damage formulas for PC -> NPC attack skills
-- Entry point: calc_skill_damage(ctx) routes to the correct sub-formula.
--
-- ctx.skill = {id, damage_value, damage_dice, damage_dice_count, skill_level, attr}
-- ctx.attacker = {level, str, dex, intel, wis, sp, dmg_mod, hit_mod, weapon_dmg, hp, max_hp}
-- ctx.target = {ac, level, mr, fire_res, water_res, wind_res, earth_res, mp}
--
-- Returns: {damage, drain_mp, hit_count}

-- Element constants (matching YAML attr values)
local ATTR_EARTH = 1
local ATTR_FIRE  = 2
local ATTR_WATER = 4
local ATTR_WIND  = 8

---------------------------------------------------------------------
-- Main routing function
---------------------------------------------------------------------
function calc_skill_damage(ctx)
    local sk = ctx.skill
    local sid = sk.id

    -- Special skills with unique formulas
    if sid == 207 then return calc_mind_break(ctx) end
    if sid == 218 then return calc_joy_of_pain(ctx) end

    -- Physical skills: damage_value == 0 AND damage_dice == 0
    if sk.damage_value == 0 and sk.damage_dice == 0 then
        return calc_physical_skill(ctx)
    end

    -- Magic damage: has damage_value or damage_dice
    return calc_magic_damage(ctx)
end

---------------------------------------------------------------------
-- Magic damage formula (wizard spells, DK/IL spells with dice)
-- Based on Java L1Magic.calcMagicDiceDamage + calcAttrResistance + calcMrDefense
---------------------------------------------------------------------
function calc_magic_damage(ctx)
    local sk = ctx.skill
    local atk = ctx.attacker
    local tgt = ctx.target

    -- Stage 1: Base damage + dice rolls
    local damage = sk.damage_value
    if sk.damage_dice > 0 and sk.damage_dice_count > 0 then
        for i = 1, sk.damage_dice_count do
            damage = damage + math.random(1, sk.damage_dice)
        end
    end

    -- Stage 2: INT + SP coefficient (Java: charaIntelligence)
    local char_intel = atk.intel + atk.sp - 12
    if char_intel < 1 then char_intel = 1 end

    -- Stage 3: Elemental resistance
    local attr_defence = calc_attr_resistance(sk.attr, tgt)

    -- Stage 4: Apply coefficient = (1.0 - attrDefence + INT*3/32)
    local coefficient = 1.0 - attr_defence + char_intel * 3.0 / 32.0
    if coefficient < 0 then coefficient = 0 end
    damage = math.floor(damage * coefficient)

    -- Stage 5: Magic critical (level 1-6 spells, 10% chance, 1.5x)
    if sk.skill_level >= 1 and sk.skill_level <= 6 then
        if math.random(1, 100) <= 10 then
            damage = math.floor(damage * 1.5)
        end
    end

    -- Stage 6: MR defense
    damage = apply_mr_defense(damage, tgt.mr)

    if damage < 1 then damage = 1 end
    return { damage = damage, drain_mp = 0, hit_count = 1 }
end

---------------------------------------------------------------------
-- Physical skill damage (108, 132, 187, 203, 208)
-- Uses STR/DEX/weapon tables from tables.lua, not INT/dice
---------------------------------------------------------------------
function calc_physical_skill(ctx)
    local sk = ctx.skill
    local atk = ctx.attacker
    local tgt = ctx.target
    local sid = sk.id

    local str = atk.str
    local dex = atk.dex
    local level = atk.level
    local weapon_dmg = atk.weapon_dmg
    local hit_mod = atk.hit_mod or 0
    local dmg_mod = atk.dmg_mod or 0

    if weapon_dmg <= 0 then weapon_dmg = 4 end

    -- Hit calculation (same as melee)
    local str_hit = table_lookup(STR_HIT, str)
    local dex_hit = table_lookup(DEX_HIT, dex)
    local hit_rate = level + str_hit + dex_hit + hit_mod

    local attack_roll = math.random(1, 20) + hit_rate - 10
    local defense = 10 - tgt.ac

    local fumble = hit_rate - 9
    local critical = hit_rate + 10

    local is_hit = false
    if attack_roll <= fumble then
        is_hit = false
    elseif attack_roll >= critical then
        is_hit = true
    elseif attack_roll > defense then
        is_hit = true
    end

    local damage = 0
    local hit_count = 1

    if is_hit then
        local base = math.random(1, weapon_dmg)
        local str_dmg = table_lookup(STR_DMG, str)
        damage = base + str_dmg + dmg_mod

        -- Skill-specific bonuses
        if sid == 108 then      -- Critical Strike: guaranteed extra damage
            damage = damage + level + math.floor(str / 3)
        elseif sid == 132 then  -- Triple Arrow: 3 separate hits
            hit_count = 3
        elseif sid == 187 then  -- Slaughterer: heavy melee
            damage = damage + math.floor(level / 2)
        elseif sid == 203 then  -- Smash/Rampage
            damage = damage + math.floor(level / 3)
        elseif sid == 208 then  -- Bone Break/Skull Destruction
            damage = damage + math.floor(level / 3)
        end

        if damage < 1 then damage = 1 end
    end

    return { damage = damage, drain_mp = 0, hit_count = hit_count }
end

---------------------------------------------------------------------
-- Mind Break (207): drains 5 MP from target, deals WIS*5 damage
---------------------------------------------------------------------
function calc_mind_break(ctx)
    local atk = ctx.attacker
    local tgt = ctx.target
    local drain_mp = 0
    local damage = 0

    if tgt.mp >= 5 then
        drain_mp = 5
        damage = atk.wis * 5
    end

    if damage < 0 then damage = 0 end
    return { damage = damage, drain_mp = drain_mp, hit_count = 1 }
end

---------------------------------------------------------------------
-- Joy of Pain (218): damage = (maxHP - currentHP) / 5, ignores MR
---------------------------------------------------------------------
function calc_joy_of_pain(ctx)
    local atk = ctx.attacker
    local hp_deficit = atk.max_hp - atk.hp
    local damage = 0
    if hp_deficit > 0 then
        damage = math.floor(hp_deficit / 5)
    end
    if damage < 1 then damage = 1 end
    return { damage = damage, drain_mp = 0, hit_count = 1 }
end

---------------------------------------------------------------------
-- Elemental resistance calculation
-- Java: calcAttrResistance -> resist -> floor(0.32 * |resist|) / 32
-- Returns a fraction: positive = damage reduction
---------------------------------------------------------------------
function calc_attr_resistance(attr, tgt)
    local resist = 0
    if attr == ATTR_EARTH then
        resist = tgt.earth_res or 0
    elseif attr == ATTR_FIRE then
        resist = tgt.fire_res or 0
    elseif attr == ATTR_WATER then
        resist = tgt.water_res or 0
    elseif attr == ATTR_WIND then
        resist = tgt.wind_res or 0
    else
        return 0
    end

    local resist_floor = math.floor(0.32 * math.abs(resist))
    if resist < 0 then
        resist_floor = -resist_floor
    end
    return resist_floor / 32.0
end

---------------------------------------------------------------------
-- MR defense calculation (PC->NPC formula from Java L1Magic)
-- MR <= 100: coefficient = 1 - 0.01 * floor(MR/2)
-- MR >  100: coefficient = 0.6 - 0.01 * floor(MR/10)
---------------------------------------------------------------------
function apply_mr_defense(damage, mr)
    if mr <= 0 then return damage end

    local mr_coefficient
    if mr <= 100 then
        mr_coefficient = 1 - 0.01 * math.floor(mr / 2)
    else
        mr_coefficient = 0.6 - 0.01 * math.floor(mr / 10)
    end

    if mr_coefficient < 0 then mr_coefficient = 0 end
    return math.floor(damage * mr_coefficient)
end

---------------------------------------------------------------------
-- Heal amount calculation
-- calc_heal_amount(damage_value, damage_dice, damage_dice_count, intel, sp)
-- Returns heal amount (int)
---------------------------------------------------------------------
function calc_heal_amount(damage_value, damage_dice, damage_dice_count, intel, sp)
    local heal = damage_value

    if damage_dice > 0 and damage_dice_count > 0 then
        for i = 1, damage_dice_count do
            heal = heal + math.random(1, damage_dice)
        end
    elseif damage_dice > 0 then
        heal = heal + math.random(1, damage_dice)
    end

    -- SP bonus
    heal = heal + sp

    -- INT bonus: (INT - 12) / 4 if INT > 12
    if intel > 12 then
        heal = heal + math.floor((intel - 12) / 4)
    end

    if heal < 0 then heal = 0 end
    return heal
end
