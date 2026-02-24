#!/usr/bin/env python3
"""Convert L1J door SQL data to YAML format."""
import re
import sys

def parse_gfx_sql(path):
    """Parse door_gfxs.sql → list of dicts."""
    entries = []
    pattern = re.compile(
        r"INSERT INTO `door_gfxs` VALUES \('(\d+)',\s*'([^']*)',\s*'(\d+)',\s*'(-?\d+)',\s*'(-?\d+)'\);"
    )
    with open(path, 'r', encoding='utf-8') as f:
        for line in f:
            m = pattern.search(line)
            if m:
                entries.append({
                    'gfxid': int(m.group(1)),
                    'note': m.group(2),
                    'direction': int(m.group(3)),
                    'left_edge_offset': int(m.group(4)),
                    'right_edge_offset': int(m.group(5)),
                })
    return entries

def parse_spawn_sql(path):
    """Parse spawnlist_door.sql → list of dicts."""
    entries = []
    pattern = re.compile(
        r"INSERT INTO `spawnlist_door` VALUES \('(\d+)',\s*'([^']*)',\s*'(\d+)',\s*'(\d+)',\s*'(\d+)',\s*'(\d+)',\s*'(\d+)',\s*'(\d+)',\s*'(\d+)'\);"
    )
    with open(path, 'r', encoding='utf-8') as f:
        for line in f:
            m = pattern.search(line)
            if m:
                entries.append({
                    'id': int(m.group(1)),
                    'location': m.group(2),
                    'gfxid': int(m.group(3)),
                    'x': int(m.group(4)),
                    'y': int(m.group(5)),
                    'map_id': int(m.group(6)),
                    'hp': int(m.group(7)),
                    'keeper': int(m.group(8)),
                    'is_opening': m.group(9) == '1',
                })
    return entries

def write_gfx_yaml(entries, path):
    with open(path, 'w', encoding='utf-8') as f:
        f.write("door_gfxs:\n")
        for e in entries:
            f.write(f"  - gfxid: {e['gfxid']}\n")
            f.write(f"    direction: {e['direction']}\n")
            f.write(f"    left_edge_offset: {e['left_edge_offset']}\n")
            f.write(f"    right_edge_offset: {e['right_edge_offset']}\n")
    print(f"Wrote {len(entries)} GFX entries to {path}")

def write_spawn_yaml(entries, path):
    with open(path, 'w', encoding='utf-8') as f:
        f.write("doors:\n")
        for e in entries:
            f.write(f"  - id: {e['id']}\n")
            f.write(f"    gfxid: {e['gfxid']}\n")
            f.write(f"    x: {e['x']}\n")
            f.write(f"    y: {e['y']}\n")
            f.write(f"    map_id: {e['map_id']}\n")
            f.write(f"    hp: {e['hp']}\n")
            f.write(f"    keeper: {e['keeper']}\n")
            is_opening = "true" if e['is_opening'] else "false"
            f.write(f"    is_opening: {is_opening}\n")
    print(f"Wrote {len(entries)} spawn entries to {path}")

if __name__ == '__main__':
    gfx_sql = 'l1j_java/db/Taiwan/door_gfxs.sql'
    spawn_sql = 'l1j_java/db/Taiwan/spawnlist_door.sql'
    gfx_yaml = 'server/data/yaml/door_gfx.yaml'
    spawn_yaml = 'server/data/yaml/door_spawn.yaml'

    gfx = parse_gfx_sql(gfx_sql)
    spawns = parse_spawn_sql(spawn_sql)

    write_gfx_yaml(gfx, gfx_yaml)
    write_spawn_yaml(spawns, spawn_yaml)
