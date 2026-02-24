-- Potion effect definitions
-- Go engine calls get_potion_effect(item_id) to get potion type and parameters
--
-- Item IDs verified against data/yaml/etcitem_list.yaml (Taiwan 3.80C data).
-- NOTE: IDs differ from standard Java L1J — always verify against the YAML file.

POTIONS = {
    -- ========== Heal potions ==========
    [40010] = { type = "heal", amount = 15 },   -- 治癒藥水
    [40011] = { type = "heal", amount = 45 },   -- 強力治癒藥水
    [40012] = { type = "heal", amount = 75 },   -- 終極治癒藥水
    [40029] = { type = "heal", amount = 15 },   -- 象牙塔治癒藥水
    [40019] = { type = "heal", amount = 150 },  -- 濃縮體力恢復劑
    [40020] = { type = "heal", amount = 300 },  -- 濃縮強力體力恢復劑
    [40021] = { type = "heal", amount = 600 },  -- 濃縮終極體力恢復劑
    [40022] = { type = "heal", amount = 100 },  -- 古代體力恢復劑
    [40023] = { type = "heal", amount = 200 },  -- 古代強力體力恢復劑
    [40024] = { type = "heal", amount = 400 },  -- 古代終極體力恢復劑

    -- ========== Mana potions ==========
    [40042] = { type = "mana", amount = 25 },   -- 精神藥水

    -- ========== Blue potions (MP regen buff) — gfx 190 ==========
    [40015]  = { type = "blue_potion", duration = 600,  gfx = 190 },  -- 藍色藥水 (10 min)
    [140015] = { type = "blue_potion", duration = 700,  gfx = 190 },  -- 受祝福的藍色藥水
    [40736]  = { type = "blue_potion", duration = 600,  gfx = 190 },  -- 智慧貨幣
    [49306]  = { type = "blue_potion", duration = 2400, gfx = 190 },  -- 福利藍色藥水 (40 min)

    -- ========== Haste (speed) potions — gfx 191 ==========
    [40013]  = { type = "haste", duration = 300,  gfx = 191 },  -- 自我加速藥水 (5 min)
    [140013] = { type = "haste", duration = 350,  gfx = 191 },  -- 受祝福的自我加速藥水
    [40030]  = { type = "haste", duration = 300,  gfx = 191 },  -- 象牙塔加速藥水
    [49302]  = { type = "haste", duration = 1200, gfx = 191 },  -- 福利加速藥水 (20 min)
    [40039]  = { type = "haste", duration = 600,  gfx = 191 },  -- 紅酒 (10 min)
    [40040]  = { type = "haste", duration = 900,  gfx = 191 },  -- 威士忌 (15 min)

    -- ========== Brave potions — gfx 751 ==========
    -- brave_type: 1 = brave (attack speed up), 3 = elf brave (movement speed up)
    [40014]  = { type = "brave", duration = 300,  brave_type = 1, gfx = 751 },  -- 勇敢藥水 (5 min)
    [140014] = { type = "brave", duration = 350,  brave_type = 1, gfx = 751 },  -- 受祝福的勇敢藥水
    [40068]  = { type = "brave", duration = 480,  brave_type = 3, gfx = 751 },  -- 精靈餅乾 (8 min)
    [140068] = { type = "brave", duration = 700,  brave_type = 3, gfx = 751 },  -- 受祝福的精靈餅乾
    [49305]  = { type = "brave", duration = 1200, brave_type = 1, gfx = 751 },  -- 福利勇敢藥水 (20 min)

    -- ========== Wisdom potions (SP+2) — gfx 750 ==========
    [40016]  = { type = "wisdom", duration = 300,  gfx = 750, sp = 2 },  -- 慎重藥水 (5 min)
    [140016] = { type = "wisdom", duration = 360,  gfx = 750, sp = 2 },  -- 受祝福的慎重藥水
    [49307]  = { type = "wisdom", duration = 1200, gfx = 750, sp = 2 },  -- 福利慎重藥水 (20 min)

    -- ========== Antidote (cure poison) ==========
    [40017]  = { type = "cure_poison" },  -- 翡翠藥水 (jade potion = antidote)
}

function get_potion_effect(item_id)
    return POTIONS[item_id]
end
