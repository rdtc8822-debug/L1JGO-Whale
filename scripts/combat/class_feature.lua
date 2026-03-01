-- 職業專屬特性（Java: L1*ClassFeature.java）
-- class_type 對應: 0=王族, 1=騎士, 2=精靈, 3=法師, 4=黑暗精靈, 5=龍騎士, 6=幻術師

-- get_ac_defense_max(class_type, ac)
-- Java: getAcDefenseMax(ac) — 根據職業計算 AC 防禦上限
-- ac 參數 = math.max(0, 10 - target_ac)（已正規化）
function get_ac_defense_max(class_type, ac)
    if ac <= 0 then return 0 end

    if class_type == 0 then     -- 王族: ac / 5
        return math.floor(ac / 5)
    elseif class_type == 1 then -- 騎士: ac / 2
        return math.floor(ac / 2)
    elseif class_type == 2 then -- 精靈: ac / 3
        return math.floor(ac / 3)
    elseif class_type == 3 then -- 法師: ac / 5
        return math.floor(ac / 5)
    elseif class_type == 4 then -- 黑暗精靈: ac / 3
        return math.floor(ac / 3)
    elseif class_type == 5 then -- 龍騎士: ac / 3
        return math.floor(ac / 3)
    elseif class_type == 6 then -- 幻術師: ac / 4
        return math.floor(ac / 4)
    end
    return 0
end

-- get_magic_level(class_type, level)
-- Java: getMagicLevel(playerLevel) — 根據職業和等級計算魔法等級
function get_magic_level(class_type, level)
    if level <= 0 then return 0 end

    if class_type == 0 then     -- 王族: min(2, lv/10)
        return math.min(2, math.floor(level / 10))
    elseif class_type == 1 then -- 騎士: lv/50
        return math.floor(level / 50)
    elseif class_type == 2 then -- 精靈: min(6, lv/8)
        return math.min(6, math.floor(level / 8))
    elseif class_type == 3 then -- 法師: min(13, lv/4)
        return math.min(13, math.floor(level / 4))
    elseif class_type == 4 then -- 黑暗精靈: min(2, lv/12)
        return math.min(2, math.floor(level / 12))
    elseif class_type == 5 then -- 龍騎士: min(4, lv/9)
        return math.min(4, math.floor(level / 9))
    elseif class_type == 6 then -- 幻術師: min(10, lv/6)
        return math.min(10, math.floor(level / 6))
    end
    return 0
end

-- calc_ac_defense(class_type, raw_ac)
-- 完整的 AC 防禦計算（Java: L1AttackMode.java 第259-264行）
-- 回傳要從傷害中減去的防禦值（隨機）
function calc_ac_defense(class_type, raw_ac)
    local ac = math.max(0, 10 - raw_ac)
    local ac_def_max = get_ac_defense_max(class_type, ac)
    if ac_def_max <= 0 then return 0 end

    -- Java: int srcacd = Math.max(1, acDefMax >> 3); return random.nextInt(acDefMax) + srcacd
    local srcacd = math.max(1, math.floor(ac_def_max / 8))
    return math.random(0, ac_def_max - 1) + srcacd
end
