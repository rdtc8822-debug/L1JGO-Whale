// teleconv converts the Java Teleporter.xml into YAML files for the Whale server.
//
// Produces:
//   - data/yaml/teleport_list.yaml   — teleport destinations
//   - data/yaml/teleport_html.yaml   — ShowHtml data values (prices for HTML dialogs)
//
// Usage:
//
//	go run ./cmd/teleconv
package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// XML structures
// ---------------------------------------------------------------------------

type NpcActionList struct {
	XMLName  xml.Name      `xml:"NpcActionList"`
	Actions  []XMLAction   `xml:"Action"`
	ShowHtml []XMLShowHtml `xml:"ShowHtml"`
}

type XMLAction struct {
	Name     string      `xml:"Name,attr"`
	NpcId    string      `xml:"NpcId,attr"`
	Teleport XMLTeleport `xml:"Teleport"`
}

type XMLTeleport struct {
	X       int `xml:"X,attr"`
	Y       int `xml:"Y,attr"`
	Map     int `xml:"Map,attr"`
	Heading int `xml:"Heading,attr"`
	Price   int `xml:"Price,attr"`
}

type XMLShowHtml struct {
	Name   string    `xml:"Name,attr"`
	HtmlId string    `xml:"HtmlId,attr"`
	NpcId  string    `xml:"NpcId,attr"`
	Data   []XMLData `xml:"Data"`
}

type XMLData struct {
	Value string `xml:"Value,attr"`
}

// ---------------------------------------------------------------------------
// YAML structures — teleport destinations
// ---------------------------------------------------------------------------

type TeleportEntry struct {
	Action  string `yaml:"action"`
	NpcID   int    `yaml:"npc_id"`
	X       int    `yaml:"x"`
	Y       int    `yaml:"y"`
	MapID   int    `yaml:"map_id"`
	Heading int    `yaml:"heading"`
	Price   int    `yaml:"price"`
}

type TeleportFile struct {
	Teleports []TeleportEntry `yaml:"teleports"`
}

// ---------------------------------------------------------------------------
// YAML structures — ShowHtml data
// ---------------------------------------------------------------------------

type HtmlDataEntry struct {
	ActionName string   `yaml:"action_name"`
	HtmlID     string   `yaml:"html_id"`
	NpcID      int      `yaml:"npc_id"`
	Data       []string `yaml:"data"`
}

type HtmlDataFile struct {
	HtmlData []HtmlDataEntry `yaml:"html_data"`
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	inputPath := filepath.Join("..", "l1j_java", "data", "xml", "NpcActions", "Teleporter.xml")
	outputPath := filepath.Join("data", "yaml", "teleport_list.yaml")
	htmlOutputPath := filepath.Join("data", "yaml", "teleport_html.yaml")

	if len(os.Args) >= 2 {
		inputPath = os.Args[1]
	}

	// ---- Read & parse XML ----
	data, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	var actionList NpcActionList
	if err := xml.Unmarshal(data, &actionList); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing XML: %v\n", err)
		os.Exit(1)
	}

	// ---- Extract teleport destinations ----
	var entries []TeleportEntry
	for _, a := range actionList.Actions {
		if !strings.HasPrefix(a.Name, "teleport ") {
			continue
		}
		npcParts := strings.Split(a.NpcId, ",")
		for _, part := range npcParts {
			part = strings.TrimSpace(part)
			npcID, err := strconv.Atoi(part)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: bad NpcId %q in action %q, skipping\n", part, a.Name)
				continue
			}
			entries = append(entries, TeleportEntry{
				Action:  a.Name,
				NpcID:   npcID,
				X:       a.Teleport.X,
				Y:       a.Teleport.Y,
				MapID:   a.Teleport.Map,
				Heading: a.Teleport.Heading,
				Price:   a.Teleport.Price,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].NpcID != entries[j].NpcID {
			return entries[i].NpcID < entries[j].NpcID
		}
		return entries[i].Action < entries[j].Action
	})

	// ---- Extract ShowHtml data values ----
	var htmlEntries []HtmlDataEntry
	for _, sh := range actionList.ShowHtml {
		if !strings.HasPrefix(strings.ToLower(sh.Name), "teleporturl") {
			continue
		}
		var dataVals []string
		for _, d := range sh.Data {
			dataVals = append(dataVals, d.Value)
		}
		npcParts := strings.Split(sh.NpcId, ",")
		for _, part := range npcParts {
			part = strings.TrimSpace(part)
			npcID, err := strconv.Atoi(part)
			if err != nil {
				continue
			}
			htmlEntries = append(htmlEntries, HtmlDataEntry{
				ActionName: sh.Name,
				HtmlID:     sh.HtmlId,
				NpcID:      npcID,
				Data:       dataVals,
			})
		}
	}

	sort.Slice(htmlEntries, func(i, j int) bool {
		if htmlEntries[i].NpcID != htmlEntries[j].NpcID {
			return htmlEntries[i].NpcID < htmlEntries[j].NpcID
		}
		return htmlEntries[i].ActionName < htmlEntries[j].ActionName
	})

	// ---- Write teleport_list.yaml ----
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating output directory: %v\n", err)
		os.Exit(1)
	}

	out := TeleportFile{Teleports: entries}
	yamlData, err := yaml.Marshal(&out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling YAML: %v\n", err)
		os.Exit(1)
	}
	header := "# Teleport destinations - converted from L1JTW Teleporter.xml\n\n"
	if err := os.WriteFile(outputPath, append([]byte(header), yamlData...), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", outputPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d teleport entries to %s\n", len(entries), outputPath)

	// ---- Write teleport_html.yaml ----
	htmlOut := HtmlDataFile{HtmlData: htmlEntries}
	htmlYaml, err := yaml.Marshal(&htmlOut)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling HTML YAML: %v\n", err)
		os.Exit(1)
	}
	htmlHeader := "# Teleport HTML data values (prices for dialog pages) - from Teleporter.xml\n\n"
	if err := os.WriteFile(htmlOutputPath, append([]byte(htmlHeader), htmlYaml...), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", htmlOutputPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d HTML data entries to %s\n", len(htmlEntries), htmlOutputPath)
}
