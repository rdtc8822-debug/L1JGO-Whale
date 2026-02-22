-- Enchant scroll formula
-- ctx.scroll_bless: 0=normal, 1=blessed, 2=cursed
-- ctx.enchant_lvl:  current enchant level of target equipment
-- ctx.safe_enchant: safe enchant level from item template
-- ctx.category:     1=weapon, 2=armor
-- ctx.weapon_chance: config weapon success rate (0.0-1.0)
-- ctx.armor_chance:  config armor success rate (0.0-1.0)
--
-- Returns: { result = "success"/"fail"/"break"/"minus", amount = N }
--   success: +amount enchant levels
--   fail:    nothing happens (blessed scroll above safe)
--   break:   equipment destroyed (normal scroll above safe)
--   minus:   -amount enchant levels (cursed scroll)

function calc_enchant(ctx)
    -- Cursed scroll: always -1
    if ctx.scroll_bless == 2 then
        return { result = "minus", amount = 1 }
    end

    -- Below safe enchant: always succeed
    if ctx.enchant_lvl < ctx.safe_enchant then
        local amount = 1
        if ctx.scroll_bless == 1 then
            amount = math.random(1, 3)
        end
        return { result = "success", amount = amount }
    end

    -- At or above safe enchant: roll for success
    local chance
    if ctx.category == 1 then
        chance = ctx.weapon_chance
    else
        chance = ctx.armor_chance
    end

    if math.random() < chance then
        local amount = 1
        if ctx.scroll_bless == 1 then
            amount = math.random(1, 3)
        end
        return { result = "success", amount = amount }
    end

    -- Failed
    if ctx.scroll_bless == 1 then
        -- Blessed scroll: nothing happens
        return { result = "fail", amount = 0 }
    end

    -- Normal scroll: equipment destroyed
    return { result = "break", amount = 0 }
end
