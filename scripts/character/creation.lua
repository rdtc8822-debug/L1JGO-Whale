-- Character creation data tables
-- ClassType: 0=Prince, 1=Knight, 2=Elf, 3=Wizard, 4=DarkElf, 5=DragonKnight, 6=Illusionist

CLASS_DATA = {
    [0] = { str=13, dex=10, con=10, wis=11, cha=13, intel=10, bonus=8,
            base_hp=14, base_mp=4,  male_gfx=0,    female_gfx=1 },
    [1] = { str=16, dex=12, con=14, wis=9,  cha=12, intel=8,  bonus=4,
            base_hp=16, base_mp=2,  male_gfx=61,   female_gfx=48 },
    [2] = { str=11, dex=12, con=12, wis=12, cha=9,  intel=12, bonus=7,
            base_hp=15, base_mp=6,  male_gfx=138,  female_gfx=37 },
    [3] = { str=8,  dex=7,  con=12, wis=12, cha=8,  intel=12, bonus=16,
            base_hp=12, base_mp=10, male_gfx=734,  female_gfx=1186 },
    [4] = { str=12, dex=15, con=8,  wis=10, cha=9,  intel=11, bonus=10,
            base_hp=12, base_mp=6,  male_gfx=2786, female_gfx=2796 },
    [5] = { str=13, dex=11, con=14, wis=12, cha=8,  intel=11, bonus=6,
            base_hp=16, base_mp=4,  male_gfx=6658, female_gfx=6661 },
    [6] = { str=11, dex=10, con=12, wis=12, cha=8,  intel=12, bonus=10,
            base_hp=14, base_mp=8,  male_gfx=6671, female_gfx=6650 },
}

-- Initial spells per class (only Wizard gets starting spells)
CLASS_SPELLS = {
    [3] = {1, 2, 4},  -- Wizard: Minor Heal, Light, Energy Bolt
}

function get_char_create_data(class_type)
    local d = CLASS_DATA[class_type]
    if not d then return nil end

    local result = {}
    for k, v in pairs(d) do
        result[k] = v
    end

    result.initial_spells = CLASS_SPELLS[class_type] or {}
    return result
end

-- Initial HP/MP calculation (CON/WIS bonus)
function calc_init_hp(class_type, con)
    local d = CLASS_DATA[class_type]
    if not d then return 14 end
    local hp = d.base_hp
    if con >= 16 then
        hp = hp + 3
    elseif con >= 14 then
        hp = hp + 2
    elseif con >= 12 then
        hp = hp + 1
    end
    return hp
end

function calc_init_mp(class_type, wis)
    local d = CLASS_DATA[class_type]
    if not d then return 4 end
    local mp = d.base_mp
    if wis >= 16 then
        mp = mp + 3
    elseif wis >= 14 then
        mp = mp + 2
    elseif wis >= 12 then
        mp = mp + 1
    end
    return mp
end
