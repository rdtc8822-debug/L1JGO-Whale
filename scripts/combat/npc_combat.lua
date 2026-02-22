-- NPC combat formulas
-- Uses same STR/DEX hit/damage tables as player combat (from tables.lua)

-- NPC melee attack against player
-- ctx.attacker = {level, str, dex, weapon_dmg, hit_mod, dmg_mod}
-- ctx.target = {ac, level, mr}
function calc_npc_melee(ctx)
    local atk = ctx.attacker
    local tgt = ctx.target

    local str = atk.str or 10
    local dex = atk.dex or 10
    local level = atk.level or 1
    local weapon_dmg = atk.weapon_dmg or 4
    local hit_mod = atk.hit_mod or 0
    local dmg_mod = atk.dmg_mod or 0

    if weapon_dmg <= 0 then weapon_dmg = 4 end

    -- Hit calculation using STR/DEX lookup tables
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
    if is_hit then
        local base = math.random(1, weapon_dmg)
        local str_dmg = table_lookup(STR_DMG, str)
        damage = base + str_dmg + dmg_mod
        if damage < 1 then damage = 1 end
    end

    return { is_hit = is_hit, damage = damage }
end

-- NPC ranged attack against player (archers, casters with projectile)
-- Same context as melee
function calc_npc_ranged(ctx)
    local atk = ctx.attacker
    local tgt = ctx.target

    local str = atk.str or 10
    local dex = atk.dex or 10
    local level = atk.level or 1
    local weapon_dmg = atk.weapon_dmg or 4
    local hit_mod = atk.hit_mod or 0
    local dmg_mod = atk.dmg_mod or 0

    if weapon_dmg <= 0 then weapon_dmg = 4 end

    -- Ranged: DEX is primary for hit
    local dex_hit = table_lookup(DEX_HIT, dex)
    local hit_rate = level + dex_hit + hit_mod

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
    if is_hit then
        local base = math.random(1, weapon_dmg)
        local dex_dmg = table_lookup(DEX_DMG, dex)
        damage = base + dex_dmg + dmg_mod
        if damage < 1 then damage = 1 end
    end

    return { is_hit = is_hit, damage = damage }
end
