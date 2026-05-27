package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// --- 家具系統 ---
// Java: com.lineage.data.item_etcitem.furniture.*
// 使用家具道具 → 在血盟小屋內生成/移除對應的 NPC 模型。

// furnitureMap 道具 ID → NPC 模板 ID 對照表。
// Java: 每個 furniture 類別硬編碼 npcId。
var furnitureMap = map[int32]int32{
	41383: 80109, // 巨大兵蟻標本
	41384: 80110, // 熊標本
	41385: 80113, // 蛇女標本
	41386: 80114, // 黑虎標本
	41387: 80115, // 鹿標本
	41388: 80124, // 哈維標本
	41389: 80118, // 青銅騎士
	41390: 80119, // 青銅馬
	41391: 80120, // 燭台
	41392: 80121, // 茶几
	41393: 80126, // 火爐
	41394: 80125, // 火把
	41395: 80111, // 君主用講台
	41396: 80112, // 旗幟
	41397: 80116, // 茶几椅子A
	41398: 80117, // 茶几椅子B
	41399: 80123, // 屏風B
	41400: 80122, // 屏風A
	49065: 80153, // 噴水池
	49066: 80155, // 花園柱子B
	49067: 80154, // 花園柱子A
	49068: 80157, // 屏風D
	49069: 80156, // 屏風C
	49070: 80158, // 花瓶架
	49071: 80159, // 派對蛋糕
	49072: 80160, // 惡魔的銅像
	49073: 80161, // 飛龍的銅像
	49074: 80162, // 黑豹的銅像
	49075: 80163, // 艾莉絲的銅像
	49076: 80164, // 巨大牛人的銅像
}

// IsFurnitureItem 檢查道具是否為家具。
func IsFurnitureItem(itemID int32) bool {
	_, ok := furnitureMap[itemID]
	return ok
}

// HandleFurnitureUse 處理家具道具使用（放置/移除切換）。
// Java: furniture.*.execute()
func HandleFurnitureUse(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, deps *Deps) {
	npcTemplateID, ok := furnitureMap[invItem.ItemID]
	if !ok {
		return
	}

	// 必須在血盟小屋範圍內
	if deps.Houses == nil || deps.Houses.FindHouseAt(player.X, player.Y, player.MapID) == nil {
		SendServerMessage(sess, 563) // "你無法在這個地方使用。"
		return
	}

	// 檢查是否已放置（切換移除）
	existingNpcID := deps.World.GetFurnitureNpc(invItem.ObjectID)
	if existingNpcID != 0 {
		// 移除家具
		removeFurnitureNpc(existingNpcID, invItem.ObjectID, deps)
		return
	}

	// Java: heading 必須為 0 或 2
	if player.Heading != 0 && player.Heading != 2 {
		SendServerMessage(sess, 79) // "沒有任何事發生"
		return
	}

	// 生成家具 NPC
	spawnFurnitureNpc(sess, player, invItem.ObjectID, npcTemplateID, deps)
}

// spawnFurnitureNpc 在玩家位置生成家具 NPC。
func spawnFurnitureNpc(sess *net.Session, player *world.PlayerInfo, itemObjID, npcTemplateID int32, deps *Deps) {
	tmpl := deps.Npcs.Get(npcTemplateID)
	if tmpl == nil {
		deps.Log.Warn("家具 NPC 模板不存在", zap.Int32("npcID", npcTemplateID))
		return
	}
	x, y := player.X, player.Y
	if player.Heading == 0 {
		y--
	} else if player.Heading == 2 {
		x++
	}

	npc := &world.NpcInfo{
		ID:                 world.NextNpcID(),
		NpcID:              tmpl.NpcID,
		Impl:               "L1FurnitureInstance",
		GfxID:              tmpl.GfxID,
		Name:               tmpl.Name,
		NameID:             tmpl.NameID,
		Level:              tmpl.Level,
		X:                  x,
		Y:                  y,
		MapID:              player.MapID,
		Heading:            0,
		HP:                 tmpl.HP,
		MaxHP:              tmpl.HP,
		MP:                 tmpl.MP,
		MaxMP:              tmpl.MP,
		ShowID:             player.ShowID,
		FurnitureItemObjID: itemObjID,
		RespawnDelay:       0, // 不重生
	}

	deps.World.AddNpc(npc)
	deps.World.AddFurnitureNpc(itemObjID, npc.ID)

	nearby := deps.World.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, viewer := range nearby {
		SendNpcPack(viewer.Session, npc)
	}

	deps.Log.Debug("家具已放置",
		zap.String("player", player.Name),
		zap.Int32("npcID", npcTemplateID),
		zap.Int32("itemObjID", itemObjID),
	)
}

// removeFurnitureNpc 移除家具 NPC。
func removeFurnitureNpc(npcObjID, itemObjID int32, deps *Deps) {
	npc := deps.World.GetNpc(npcObjID)
	if npc == nil {
		deps.World.RemoveFurnitureNpc(itemObjID)
		return
	}

	nearby := deps.World.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	removeData := BuildRemoveObject(npc.ID)
	BroadcastToPlayers(nearby, removeData)

	// 從世界移除
	deps.World.RemoveNpc(npc.ID)
	deps.World.RemoveFurnitureNpc(itemObjID)

	deps.Log.Debug("家具已移除",
		zap.Int32("npcObjID", npcObjID),
		zap.Int32("itemObjID", itemObjID),
	)
}
