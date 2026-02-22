-- Spell shop tier definitions per class
-- Each tier: skill_level range, required character level, cost in adena

SPELL_TIERS = {
    [0] = {  -- Royal (Prince)
        { min_skill_level = 15, max_skill_level = 15, min_char_level = 10, cost = 100 },
        { min_skill_level = 16, max_skill_level = 16, min_char_level = 20, cost = 400 },
    },
    [1] = {  -- Knight
        { min_skill_level = 11, max_skill_level = 11, min_char_level = 50, cost = 100 },
    },
    [2] = {  -- Elf
        { min_skill_level = 17, max_skill_level = 18, min_char_level = 8,  cost = 100 },
        { min_skill_level = 19, max_skill_level = 20, min_char_level = 16, cost = 400 },
        { min_skill_level = 21, max_skill_level = 22, min_char_level = 24, cost = 900 },
    },
    [3] = {  -- Wizard
        { min_skill_level = 1, max_skill_level = 1, min_char_level = 4,  cost = 100 },
        { min_skill_level = 2, max_skill_level = 2, min_char_level = 8,  cost = 400 },
        { min_skill_level = 3, max_skill_level = 3, min_char_level = 12, cost = 900 },
    },
    [4] = {  -- Dark Elf
        { min_skill_level = 13, max_skill_level = 13, min_char_level = 12, cost = 100 },
        { min_skill_level = 14, max_skill_level = 14, min_char_level = 24, cost = 400 },
    },
    [5] = {  -- Dragon Knight
        { min_skill_level = 23, max_skill_level = 23, min_char_level = 15, cost = 100 },
        { min_skill_level = 24, max_skill_level = 24, min_char_level = 30, cost = 400 },
    },
    [6] = {  -- Illusionist
        { min_skill_level = 25, max_skill_level = 25, min_char_level = 15, cost = 100 },
        { min_skill_level = 26, max_skill_level = 26, min_char_level = 30, cost = 400 },
    },
}

function get_spell_tiers(class_type)
    return SPELL_TIERS[class_type]
end
