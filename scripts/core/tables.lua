-- L1J stat lookup tables
-- STR/DEX hit and damage bonuses, exp table

-- STR hit bonus (index 1-59, representing STR 1-59)
STR_HIT = {
    -2, -2, -2, -2, -2, -2, -2,
    -2, -1, -1, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4,
    5, 5, 5, 6, 6, 6, 7, 7, 7, 8, 8, 8, 9, 9, 9,
    10, 10, 10, 11, 11, 11, 12, 12, 12, 13, 13, 13,
    14, 14, 14, 15, 15, 15, 16, 16, 16, 17, 17, 17
}

-- DEX hit bonus (index 1-60)
DEX_HIT = {
    -2, -2, -2, -2, -2, -2, -1, -1, 0, 0,
    1, 1, 2, 2, 3, 3, 4, 4, 5, 6,
    7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
    17, 18, 19, 19, 19, 20, 20, 20, 21, 21,
    21, 22, 22, 22, 23, 23, 23, 24, 24, 24,
    25, 25, 25, 26, 26, 26, 27, 27, 27, 28
}

-- STR damage bonus (index 1-50, simplified from Java's 0-127 table)
STR_DMG = {
    -6, -5, -4, -3, -3, -2, -2, -1, -1, 0,
    0, 1, 1, 1, 2, 2, 3, 3, 4, 4,
    5, 5, 5, 6, 6, 7, 7, 8, 8, 9,
    9, 10, 10, 11, 12, 12, 12, 12, 13, 13,
    13, 13, 14, 14, 14, 14, 15, 15, 15, 15
}

-- DEX damage bonus (index 1-50, simplified)
DEX_DMG = {
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 1, 2, 3, 4, 4, 4,
    5, 5, 5, 6, 6, 6, 7, 7, 7, 8,
    8, 8, 9, 9, 9, 10, 10, 10, 10, 11,
    11, 11, 11, 12, 12, 12, 12, 13, 13, 13
}

-- Experience table (cumulative exp for each level)
EXP_TABLE = {
    [1]  = 0,
    [2]  = 125,
    [3]  = 300,
    [4]  = 500,
    [5]  = 750,
    [6]  = 1296,
    [7]  = 2401,
    [8]  = 4096,
    [9]  = 6581,
    [10] = 10000,
    [11] = 14661,
    [12] = 20756,
    [13] = 28581,
    [14] = 38436,
    [15] = 50645,
    [16] = 65556,
    [17] = 83541,
    [18] = 104996,
    [19] = 130341,
    [20] = 160020,
    [21] = 194501,
    [22] = 234276,
    [23] = 279861,
    [24] = 331792,
    [25] = 390641,
    [26] = 456992,
    [27] = 531457,
    [28] = 614672,
    [29] = 707297,
    [30] = 810016,
    [31] = 923537,
    [32] = 1048592,
    [33] = 1185937,
    [34] = 1336352,
    [35] = 1500641,
    [36] = 1679632,
    [37] = 1874177,
    [38] = 2085152,
    [39] = 2313457,
    [40] = 2560016,
    [41] = 2825777,
    [42] = 3111713,
    [43] = 3418818,
    [44] = 3748113,
    [45] = 4100642,
    [46] = 4830002,
    [47] = 6338418,
    [48] = 9833681,
    [49] = 19745870,
    [50] = 55810962,
}

-- Helper: clamp value to table bounds
function table_lookup(tbl, index)
    if index < 1 then index = 1 end
    if index > #tbl then index = #tbl end
    return tbl[index]
end

-- Get level from cumulative exp
function level_from_exp(exp)
    for lv = 50, 2, -1 do
        if EXP_TABLE[lv] and exp >= EXP_TABLE[lv] then
            return lv
        end
    end
    return 1
end

-- Get cumulative exp required for a given level
function exp_for_level(level)
    if level <= 1 then return 0 end
    if level <= 50 then
        return EXP_TABLE[level] or 0
    end
    -- Beyond 50: linear extension
    return (EXP_TABLE[50] or 0) + (level - 50) * 10000000
end
