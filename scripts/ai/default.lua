-- Default NPC AI script
-- Called once per tick per alive L1Monster NPC
-- Receives AIContext table, returns array of AICommand tables
--
-- Command types:
--   "attack"         - melee attack current target
--   "ranged_attack"  - ranged attack current target
--   "skill"          - use skill {skill_id, act_id} on target
--   "move_toward"    - move 1 tile toward target
--   "wander"         - move 1 tile in direction {dir} (-1 = continue current)
--   "lose_aggro"     - clear aggro target
--   "idle"           - do nothing

function npc_ai(ctx)
    -- Has aggro target
    if ctx.target_id > 0 then
        return ai_with_target(ctx)
    end

    -- No target: idle wander
    if ctx.can_move then
        local dir = pick_wander_dir(ctx)
        return {{ type = "wander", dir = dir }}
    end

    return {{ type = "idle" }}
end

-- AI logic when NPC has a target
function ai_with_target(ctx)
    -- Target too far: lose aggro
    if ctx.target_dist > 15 then
        return {{ type = "lose_aggro" }}
    end

    -- Determine effective attack range
    local atk_range = 1
    if ctx.ranged > 1 then
        atk_range = ctx.ranged
    end

    local in_range = ctx.target_dist <= atk_range

    -- In attack range: fight or wait for cooldown (NEVER move)
    if in_range then
        if ctx.can_attack then
            -- Try mob skill first
            local skill_cmd = try_use_skill(ctx)
            if skill_cmd then
                return { skill_cmd }
            end

            -- Ranged NPC and target is further than melee: use ranged attack
            if ctx.ranged > 1 and ctx.target_dist > 1 then
                return {{ type = "ranged_attack" }}
            else
                return {{ type = "attack" }}
            end
        end
        -- In range but attack on cooldown: stand still and wait
        return {{ type = "idle" }}
    end

    -- Out of range: try skill that can reach, otherwise chase
    if ctx.can_attack then
        local skill_cmd = try_use_skill(ctx)
        if skill_cmd then
            return { skill_cmd }
        end
    end

    if ctx.can_move then
        return {{ type = "move_toward" }}
    end

    return {{ type = "idle" }}
end

-- Try to use a mob skill. Returns a command table or nil.
function try_use_skill(ctx)
    local skills = ctx.skills
    if not skills or #skills == 0 then
        return nil
    end

    local hp_pct = 100
    if ctx.max_hp > 0 then
        hp_pct = math.floor(ctx.hp * 100 / ctx.max_hp)
    end

    for _, sk in ipairs(skills) do
        local ok = true

        -- HP threshold check (0 = no threshold, otherwise only use when HP% <= trigger_hp)
        if ok and sk.trigger_hp > 0 and hp_pct > sk.trigger_hp then
            ok = false
        end

        -- Range check (trigger_range is negative: within abs(trigger_range) tiles)
        if ok then
            local sk_range = math.abs(sk.trigger_range)
            if sk_range > 0 and ctx.target_dist > sk_range then
                ok = false
            end
        end

        -- MP check
        if ok and sk.mp_consume > 0 and sk.mp_consume > ctx.mp then
            ok = false
        end

        -- Probability roll
        if ok and sk.trigger_random < 100 and math.random(100) > sk.trigger_random then
            ok = false
        end

        -- Passed all checks
        if ok then
            return {
                type = "skill",
                skill_id = sk.skill_id,
                act_id = sk.act_id,
                gfx_id = sk.gfx_id,
            }
        end
    end
    return nil
end

-- Pick a wander direction.
-- Returns heading 0-7 for a new direction, or -1 to continue current direction.
function pick_wander_dir(ctx)
    -- Still walking in current direction
    if ctx.wander_dist > 0 then
        return -1
    end

    -- Far from spawn: bias toward spawn (Go handles actual heading calculation)
    if ctx.spawn_dist > 20 then
        return -2  -- special: Go will calculate heading toward spawn
    end

    -- Pick random direction
    return math.random(0, 7)
end
