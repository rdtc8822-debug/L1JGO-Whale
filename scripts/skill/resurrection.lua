-- Resurrection skill effect definitions
-- Returns hp_ratio/mp_ratio (0.0-1.0) or fixed_hp for fixed-amount heals

RESURRECT_EFFECTS = {
    [18]  = { fixed_hp = -1, hp_ratio = 0, mp_ratio = 0 },     -- 起死回生術: HP = caster's level (signaled by fixed_hp = -1)
    [75]  = { fixed_hp = 0,  hp_ratio = 1.0, mp_ratio = 1.0 }, -- 終極返生術: full restore
    [131] = { fixed_hp = 0,  hp_ratio = 0.5, mp_ratio = 0.5 }, -- 世界樹的呼喚: 50% restore
    [165] = { fixed_hp = 0,  hp_ratio = 1.0, mp_ratio = 1.0 }, -- 生命呼喚: full restore
}

function get_resurrect_effect(skill_id)
    return RESURRECT_EFFECTS[skill_id]
end

-- Resurrection skill ID set (for routing check)
function is_resurrection_skill(skill_id)
    return RESURRECT_EFFECTS[skill_id] ~= nil
end
