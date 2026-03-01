-- Melee combat formula for PC attacking NPC
-- Receives a context table, returns {is_hit, damage}

function calc_melee_attack(ctx)
    local atk = ctx.attacker
    local tgt = ctx.target

    local str = atk.str
    local dex = atk.dex
    local level = atk.level
    local weapon_dmg = atk.weapon_dmg
    local hit_mod = atk.hit_mod or 0
    local dmg_mod = atk.dmg_mod or 0

    -- Default fist damage
    if weapon_dmg <= 0 then
        weapon_dmg = 4
    end

    -- Hit calculation: STR for hit + DEX for hit
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

    -- Damage calculation: STR for damage
    local damage = 0
    if is_hit then
        local base = math.random(1, weapon_dmg)
        local str_dmg = table_lookup(STR_DMG, str)
        damage = base + str_dmg + dmg_mod

        -- 職業 AC 防禦減傷（Java: L1AttackMode.java — 僅對玩家目標生效）
        local target_class = tgt.class_type or -1
        if target_class >= 0 then
            local ac_def = calc_ac_defense(target_class, tgt.ac)
            damage = damage - ac_def
        end

        -- Minimum 1 damage on hit
        if damage < 1 then
            damage = 1
        end
    end

    return { is_hit = is_hit, damage = damage }
end

---------------------------------------------------------------------
-- Ranged (bow) combat formula for PC attacking NPC
-- Java: L1Attack — bow uses DEX for both hit and damage
--
-- ctx.attacker = {level, str, dex, bow_dmg, arrow_dmg, bow_hit_mod, bow_dmg_mod}
-- ctx.target = {ac, level, mr}
---------------------------------------------------------------------
function calc_ranged_attack(ctx)
    local atk = ctx.attacker
    local tgt = ctx.target

    local dex = atk.dex
    local str = atk.str
    local level = atk.level
    local bow_dmg = atk.bow_dmg or 1
    local arrow_dmg = atk.arrow_dmg or 0
    local bow_hit_mod = atk.bow_hit_mod or 0
    local bow_dmg_mod = atk.bow_dmg_mod or 0

    -- Hit calculation: DEX is primary for ranged hit (Java: calcBowHit)
    local dex_hit = table_lookup(DEX_HIT, dex)
    local hit_rate = level + dex_hit + bow_hit_mod

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

    -- Damage calculation: DEX for damage (Java: calcBowDamage)
    local damage = 0
    if is_hit then
        local base = math.random(1, bow_dmg)
        local dex_dmg = table_lookup(DEX_DMG, dex)
        local str_dmg = table_lookup(STR_DMG, str)
        -- Java: bow damage = base + DEX_DMG + STR_DMG/2 + arrow
        damage = base + dex_dmg + math.floor(str_dmg / 2) + arrow_dmg + bow_dmg_mod

        -- 職業 AC 防禦減傷（僅對玩家目標生效）
        local target_class = tgt.class_type or -1
        if target_class >= 0 then
            local ac_def = calc_ac_defense(target_class, tgt.ac)
            damage = damage - ac_def
        end

        if damage < 1 then
            damage = 1
        end
    end

    return { is_hit = is_hit, damage = damage }
end
