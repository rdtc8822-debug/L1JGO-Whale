-- Respawn (getback) location definitions
-- Map ID -> {x, y, map}

RESPAWN_LOCATIONS = {
    [0]    = { x = 32583, y = 32929, map = 0 },     -- Silver Knight Village (map 0)
    [4]    = { x = 33084, y = 33391, map = 4 },     -- Silver Knight Village (map 4)
    [2005] = { x = 32689, y = 32842, map = 2005 },  -- Talking Island (starter area)
    [70]   = { x = 32579, y = 32735, map = 70 },    -- Hidden Valley
    [303]  = { x = 32596, y = 32807, map = 303 },   -- Orc Forest
    [350]  = { x = 32657, y = 32857, map = 350 },   -- Elf Forest
}

-- Default: Silver Knight Village (map 4)
local DEFAULT_RESPAWN = { x = 33084, y = 33391, map = 4 }

function get_respawn_location(map_id)
    return RESPAWN_LOCATIONS[map_id] or DEFAULT_RESPAWN
end
