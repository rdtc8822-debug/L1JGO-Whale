// portalconv converts dungeon.sql INSERT statements to portal_list.yaml.
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Portal struct {
	SrcX       int    `yaml:"src_x"`
	SrcY       int    `yaml:"src_y"`
	SrcMapID   int    `yaml:"src_map_id"`
	DstX       int    `yaml:"dst_x"`
	DstY       int    `yaml:"dst_y"`
	DstMapID   int    `yaml:"dst_map_id"`
	DstHeading int    `yaml:"dst_heading"`
	Note       string `yaml:"note"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: portalconv <dungeon.sql> <output.yaml>")
		os.Exit(1)
	}

	inFile, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer inFile.Close()

	// Pattern: INSERT INTO `dungeon` VALUES ('32477', '32851', '0', '32669', '32802', '1', '4', 'note');
	re := regexp.MustCompile(`VALUES\s*\(\s*'(-?\d+)'\s*,\s*'(-?\d+)'\s*,\s*'(-?\d+)'\s*,\s*'(-?\d+)'\s*,\s*'(-?\d+)'\s*,\s*'(-?\d+)'\s*,\s*'(-?\d+)'\s*,\s*'([^']*)'\s*\)`)

	var portals []Portal
	scanner := bufio.NewScanner(inFile)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, len(buf))

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "INSERT INTO") {
			continue
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		srcX, _ := strconv.Atoi(m[1])
		srcY, _ := strconv.Atoi(m[2])
		srcMap, _ := strconv.Atoi(m[3])
		dstX, _ := strconv.Atoi(m[4])
		dstY, _ := strconv.Atoi(m[5])
		dstMap, _ := strconv.Atoi(m[6])
		heading, _ := strconv.Atoi(m[7])
		note := m[8]

		// Skip hotel portals (mapID >= 16384) — they need special key logic
		if dstMap >= 16384 {
			continue
		}

		portals = append(portals, Portal{
			SrcX: srcX, SrcY: srcY, SrcMapID: srcMap,
			DstX: dstX, DstY: dstY, DstMapID: dstMap,
			DstHeading: heading, Note: note,
		})
	}

	// Sort by src_map_id, src_x, src_y
	sort.Slice(portals, func(i, j int) bool {
		if portals[i].SrcMapID != portals[j].SrcMapID {
			return portals[i].SrcMapID < portals[j].SrcMapID
		}
		if portals[i].SrcX != portals[j].SrcX {
			return portals[i].SrcX < portals[j].SrcX
		}
		return portals[i].SrcY < portals[j].SrcY
	})

	out, err := os.Create(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer out.Close()

	fmt.Fprintf(out, "# Portal list — auto-generated from dungeon.sql (%d entries)\n", len(portals))
	for _, p := range portals {
		note := strings.ReplaceAll(p.Note, `"`, `\"`)
		fmt.Fprintf(out, "- src_x: %d\n  src_y: %d\n  src_map_id: %d\n  dst_x: %d\n  dst_y: %d\n  dst_map_id: %d\n  dst_heading: %d\n  note: \"%s\"\n",
			p.SrcX, p.SrcY, p.SrcMapID, p.DstX, p.DstY, p.DstMapID, p.DstHeading, note)
	}

	fmt.Printf("Wrote %d portal entries to %s\n", len(portals), os.Args[2])
}
