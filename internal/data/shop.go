package data

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ShopItem holds one item entry in an NPC's shop.
type ShopItem struct {
	ItemID          int32 `yaml:"item_id"`
	Order           int32 `yaml:"order"`
	SellingPrice    int32 `yaml:"selling_price"`    // price NPC sells at (-1 = not selling)
	PackCount       int32 `yaml:"pack_count"`       // items per purchase (0 treated as 1)
	PurchasingPrice int32 `yaml:"purchasing_price"` // price NPC buys at (-1 = not buying)
}

// Shop holds the sell/buy item lists for one NPC.
type Shop struct {
	NpcID      int32
	SellingItems    []*ShopItem // items NPC sells to player (selling_price >= 0)
	PurchasingItems []*ShopItem // items NPC buys from player (purchasing_price >= 0)
}

// ShopTable holds all NPC shops indexed by NpcID.
type ShopTable struct {
	shops map[int32]*Shop
}

// Get returns a shop by NPC template ID, or nil if not found.
func (t *ShopTable) Get(npcID int32) *Shop {
	return t.shops[npcID]
}

// Count returns the number of shops loaded.
func (t *ShopTable) Count() int {
	return len(t.shops)
}

type shopYAMLItem struct {
	ItemID          int32 `yaml:"item_id"`
	Order           int32 `yaml:"order"`
	SellingPrice    int32 `yaml:"selling_price"`
	PackCount       int32 `yaml:"pack_count"`
	PurchasingPrice int32 `yaml:"purchasing_price"`
}

type shopYAMLEntry struct {
	NpcID int32          `yaml:"npc_id"`
	Items []shopYAMLItem `yaml:"items"`
}

type shopListFile struct {
	Shops []shopYAMLEntry `yaml:"shops"`
}

// LoadShopTable loads NPC shop data from a YAML file.
func LoadShopTable(path string) (*ShopTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read shop_list: %w", err)
	}
	var f shopListFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse shop_list: %w", err)
	}

	t := &ShopTable{shops: make(map[int32]*Shop, len(f.Shops))}
	for _, entry := range f.Shops {
		shop := &Shop{NpcID: entry.NpcID}
		for i := range entry.Items {
			item := &ShopItem{
				ItemID:          entry.Items[i].ItemID,
				Order:           entry.Items[i].Order,
				SellingPrice:    entry.Items[i].SellingPrice,
				PackCount:       entry.Items[i].PackCount,
				PurchasingPrice: entry.Items[i].PurchasingPrice,
			}
			if item.PackCount <= 0 {
				item.PackCount = 1
			}
			if item.SellingPrice >= 0 {
				shop.SellingItems = append(shop.SellingItems, item)
			}
			if item.PurchasingPrice >= 0 {
				shop.PurchasingItems = append(shop.PurchasingItems, item)
			}
		}
		t.shops[entry.NpcID] = shop
	}
	return t, nil
}
