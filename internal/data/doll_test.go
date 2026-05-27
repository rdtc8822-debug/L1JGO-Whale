package data

import "testing"

func TestDollTableUsesClientSpriteGfxLikeYiwei(t *testing.T) {
	t.Parallel()

	table, err := LoadDollTable("../../data/yaml/dolls.yaml")
	if err != nil {
		t.Fatalf("載入魔法娃娃資料失敗: %v", err)
	}

	tests := []struct {
		itemID int32
		gfxID  int32
	}{
		{itemID: 41248, gfxID: 6100},
		{itemID: 41249, gfxID: 5919},
		{itemID: 41250, gfxID: 6096},
		{itemID: 47105, gfxID: 6477},
		{itemID: 47106, gfxID: 6483},
		{itemID: 47107, gfxID: 6480},
		{itemID: 47108, gfxID: 7047},
		{itemID: 47109, gfxID: 7257},
		{itemID: 47110, gfxID: 7662},
		{itemID: 47111, gfxID: 7661},
		{itemID: 47112, gfxID: 7659},
		{itemID: 47113, gfxID: 7660},
		{itemID: 49037, gfxID: 6443},
		{itemID: 49038, gfxID: 6449},
		{itemID: 49039, gfxID: 6452},
	}

	for _, tc := range tests {
		def := table.Get(tc.itemID)
		if def == nil {
			t.Fatalf("缺少魔法娃娃 item_id=%d", tc.itemID)
		}
		if def.GfxID != tc.gfxID {
			t.Errorf("item_id=%d gfx_id=%d，應使用客戶端 sprite gfx_id=%d，不可使用 NPC template id", tc.itemID, def.GfxID, tc.gfxID)
		}
	}
}
