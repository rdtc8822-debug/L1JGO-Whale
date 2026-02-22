-- Potion effect definitions
-- Go engine calls get_potion_effect(item_id) to get potion type and parameters

POTIONS = {
    -- ========== Heal potions ==========
    [40010] = { type = "heal", amount = 15 },   -- 治癒藥水
    [40011] = { type = "heal", amount = 45 },   -- 強力治癒藥水
    [40012] = { type = "heal", amount = 75 },   -- 終極治癒藥水
    [40019] = { type = "heal", amount = 150 },  -- 濃縮治癒藥水
    [40020] = { type = "heal", amount = 300 },  -- 強力濃縮治癒藥水
    [40021] = { type = "heal", amount = 600 },  -- 終極濃縮治癒藥水
    [40022] = { type = "heal", amount = 60 },   -- 古代治癒藥水
    [40029] = { type = "heal", amount = 15 },   -- 象牙塔治癒藥水
    [40024] = { type = "heal", amount = 20 },   -- 人蔘
    [40025] = { type = "heal", amount = 55 },   -- 濃縮人蔘
    [40026] = { type = "heal", amount = 120 },  -- 靈芝
    [40027] = { type = "heal", amount = 240 },  -- 濃縮靈芝
    [40028] = { type = "heal", amount = 480 },  -- 神仙草

    -- ========== Mana potions ==========
    [40013] = { type = "mana", amount = 10 },   -- 瑪那藥水
    [40014] = { type = "mana", amount = 25 },   -- 強力瑪那藥水
    [40015] = { type = "mana", amount = 50 },   -- 終極瑪那藥水
    [40023] = { type = "mana", amount = 100 },  -- 濃縮瑪那藥水
    [40016] = { type = "mana", amount = 30 },   -- 清澈藥水

    -- ========== Haste (green) potions — gfx 191 ==========
    [40005]  = { type = "haste", duration = 300,  gfx = 191 },  -- 自我加速藥水 (5 min)
    [140005] = { type = "haste", duration = 350,  gfx = 191 },  -- 受祝福的自我加速藥水
    [40018]  = { type = "haste", duration = 1800, gfx = 191 },  -- 強化自我加速藥水 (30 min)
    [140018] = { type = "haste", duration = 2100, gfx = 191 },  -- 受祝福的強化自我加速藥水
    [40030]  = { type = "haste", duration = 300,  gfx = 191 },  -- 象牙塔加速藥水
    [40039]  = { type = "haste", duration = 600,  gfx = 191 },  -- 紅酒 (10 min)
    [40040]  = { type = "haste", duration = 900,  gfx = 191 },  -- 威士忌 (15 min)

    -- ========== Brave potions — gfx 751 ==========
    -- brave_type: 1 = brave (1.33x), 3 = elf brave (1.15x)
    [40048]  = { type = "brave", duration = 300, brave_type = 1, gfx = 751 },  -- 勇敢藥水 (5 min)
    [140048] = { type = "brave", duration = 350, brave_type = 1, gfx = 751 },  -- 受祝福的勇敢藥水
    [40049]  = { type = "brave", duration = 480, brave_type = 3, gfx = 751 },  -- 精靈餅乾 (8 min)
    [140049] = { type = "brave", duration = 700, brave_type = 3, gfx = 751 },  -- 受祝福的精靈餅乾
    [41338]  = { type = "brave", duration = 1800, brave_type = 1, gfx = 751 }, -- 強化勇敢藥水 (30 min)
    [41342]  = { type = "brave", duration = 1800, brave_type = 3, gfx = 751 }, -- 強化精靈餅乾 (30 min)

    -- ========== Wisdom potions (SP+2) — gfx 750 ==========
    [40050]  = { type = "wisdom", duration = 300,  gfx = 750, sp = 2 },  -- 慎重藥水 (5 min)
    [140050] = { type = "wisdom", duration = 360,  gfx = 750, sp = 2 },  -- 受祝福的慎重藥水
    [41340]  = { type = "wisdom", duration = 1800, gfx = 750, sp = 2 },  -- 強化慎重藥水 (30 min)
}

function get_potion_effect(item_id)
    return POTIONS[item_id]
end
