-- Level up HP/MP formulas
-- Matches Java CalcStat.calcStatHp / calcStatMp

-- HP gain per level by class
-- ClassType: 0=Prince, 1=Knight, 2=Elf, 3=Wizard, 4=DarkElf, 5=DragonKnight, 6=Illusionist
local BASE_HP = { [0]=11, [1]=17, [2]=10, [3]=7, [4]=10, [5]=13, [6]=9 }

function calc_level_up_hp(class_type, con)
    local base = BASE_HP[class_type] or 10
    local hp = base + math.random(0, 1)  -- base + rand(0 or 1)
    if con > 15 then
        hp = hp + (con - 15)
    end
    if hp < 0 then hp = 0 end
    return hp
end

-- WIS -> seedY (variance range) table from Java CalcStat
local function wis_to_seed_y(wis)
    if wis == 9 or (wis >= 12 and wis <= 17) then
        return 3
    elseif (wis >= 18 and wis <= 23) or wis == 25 or wis == 26
        or wis == 29 or wis == 30 or wis == 33 or wis == 34 then
        return 4
    elseif wis == 24 or wis == 27 or wis == 28
        or wis == 31 or wis == 32 or wis >= 35 then
        return 5
    end
    return 2
end

-- WIS -> seedZ (base bonus) table from Java CalcStat
local function wis_to_seed_z(wis)
    if wis >= 33 then return 6
    elseif wis >= 29 then return 5
    elseif wis >= 25 then return 4
    elseif wis >= 21 then return 3
    elseif wis >= 15 then return 2
    elseif wis >= 10 then return 1
    end
    return 0
end

-- Class multipliers for MP gain
-- Prince=1, Knight=2/3, Elf=3/2, Wizard=2, DarkElf=3/2, DK=2/3, Illusionist=5/3
local CLASS_MP_MULT = {
    [0] = {1, 1},   -- Prince: *1
    [1] = {2, 3},   -- Knight: *2/3
    [2] = {3, 2},   -- Elf: *3/2
    [3] = {2, 1},   -- Wizard: *2
    [4] = {3, 2},   -- Dark Elf: *3/2
    [5] = {2, 3},   -- Dragon Knight: *2/3
    [6] = {5, 3},   -- Illusionist: *5/3
}

function calc_level_up_mp(class_type, wis)
    local seed_y = wis_to_seed_y(wis)
    local seed_z = wis_to_seed_z(wis)

    local mp = math.random(1, seed_y) + seed_z

    local mult = CLASS_MP_MULT[class_type]
    if mult then
        mp = math.floor(mp * mult[1] / mult[2])
    end

    if mp < 0 then mp = 0 end
    return mp
end

-- Death exp penalty: lose 10% of current level's exp range
function calc_death_exp_penalty(level, exp)
    if level <= 1 then return 0 end
    local current = EXP_TABLE[level]
    local prev = EXP_TABLE[level - 1] or 0
    if not current then return 0 end
    local range = current - prev
    if range <= 0 then return 0 end
    local penalty = math.floor(range / 10)
    -- Don't go below previous level's exp
    if exp - penalty < prev then
        penalty = exp - prev
    end
    if penalty < 0 then penalty = 0 end
    return penalty
end
