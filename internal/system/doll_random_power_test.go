package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

// Yiwei distinguishes flat Doll_Dmg from chance-based Doll_DmgR.
func TestDollChanceDamagePowerDoesNotApplyFlatDmgModLikeJava(t *testing.T) {
	ws := world.NewState()
	sess := newDollTestSession(t, 1)
	player := &world.PlayerInfo{
		Session:   sess,
		SessionID: 1,
		CharID:    10,
		Name:      "pc",
		X:         100,
		Y:         100,
		MapID:     4,
		Known:     world.NewKnownEntities(),
		Inv:       world.NewInventory(),
	}
	invItem := &world.InvItem{ObjectID: 9001, ItemID: 41250}
	def := &data.DollDef{
		ItemID:   41250,
		GfxID:    6096,
		NameID:   "wolf",
		Name:     "wolf",
		Duration: 1800,
		Powers: []data.DollPowerDef{
			{Type: "dmg", Value: 15, Chance: 3},
		},
	}

	NewDollSystem(&handler.Deps{World: ws}).UseDoll(sess, player, invItem, def)

	if player.DmgMod != 0 {
		t.Fatalf("chance dmg power should not be applied as flat DmgMod: got %d", player.DmgMod)
	}
	if player.DollDmgAdd != 15 || player.DollDmgAddChance != 3 {
		t.Fatalf("chance dmg power should be stored as random doll damage: got value=%d chance=%d", player.DollDmgAdd, player.DollDmgAddChance)
	}
}

// Yiwei distinguishes flat Doll_DmgDown from chance-based Doll_DmgDownR.
func TestDollChanceDamageReductionPowerDoesNotApplyFlatReductionLikeJava(t *testing.T) {
	ws := world.NewState()
	sess := newDollTestSession(t, 1)
	player := &world.PlayerInfo{
		Session:   sess,
		SessionID: 1,
		CharID:    10,
		Name:      "pc",
		X:         100,
		Y:         100,
		MapID:     4,
		Known:     world.NewKnownEntities(),
		Inv:       world.NewInventory(),
	}
	invItem := &world.InvItem{ObjectID: 9002, ItemID: 49039}
	def := &data.DollDef{
		ItemID:   49039,
		GfxID:    6452,
		NameID:   "golem",
		Name:     "golem",
		Duration: 1800,
		Powers: []data.DollPowerDef{
			{Type: "dmg_reduction", Value: 15, Chance: 4},
		},
	}

	NewDollSystem(&handler.Deps{World: ws}).UseDoll(sess, player, invItem, def)

	if player.EquipBonuses.DmgReduction != 0 {
		t.Fatalf("chance dmg_reduction power should not be applied as flat DmgReduction: got %d", player.EquipBonuses.DmgReduction)
	}
	if player.DollDmgReduceRandom != 15 || player.DollDmgReduceRandomChance != 4 {
		t.Fatalf("chance dmg_reduction power should be stored as random doll reduction: got value=%d chance=%d", player.DollDmgReduceRandom, player.DollDmgReduceRandomChance)
	}
}

func TestDollRandomDamageAddUsesYiweiChanceRoll(t *testing.T) {
	player := &world.PlayerInfo{DollDmgAdd: 15, DollDmgAddChance: 3}

	if got := applyDollRandomDamageAddLikeJava(player, 20, 2); got != 35 {
		t.Fatalf("roll 3 should trigger +15 random doll damage: got %d", got)
	}
	if got := applyDollRandomDamageAddLikeJava(player, 20, 3); got != 20 {
		t.Fatalf("roll 4 should not trigger 3%% random doll damage: got %d", got)
	}
}

func TestDollRandomDamageReductionUsesYiweiChanceRoll(t *testing.T) {
	player := &world.PlayerInfo{DollDmgReduceRandom: 15, DollDmgReduceRandomChance: 4}

	if got := applyDollRandomDamageReductionLikeJava(player, 20, 3); got != 5 {
		t.Fatalf("roll 4 should trigger -15 random doll reduction: got %d", got)
	}
	if got := applyDollRandomDamageReductionLikeJava(player, 10, 3); got != 0 {
		t.Fatalf("random doll reduction should clamp damage at 0: got %d", got)
	}
	if got := applyDollRandomDamageReductionLikeJava(player, 20, 4); got != 20 {
		t.Fatalf("roll 5 should not trigger 4%% random doll reduction: got %d", got)
	}
}

func TestDollRandomPowerBonusesAreRemovedOnDismiss(t *testing.T) {
	sys := &DollSystem{}
	player := &world.PlayerInfo{}
	doll := &world.DollInfo{
		BonusDmgRandom:             15,
		BonusDmgRandomChance:       3,
		BonusDmgReduceRandom:       15,
		BonusDmgReduceRandomChance: 4,
	}

	sys.applyDollBonuses(player, doll)
	sys.removeDollBonuses(player, doll)

	if player.DollDmgAdd != 0 || player.DollDmgAddChance != 0 {
		t.Fatalf("random doll damage should be removed: got value=%d chance=%d", player.DollDmgAdd, player.DollDmgAddChance)
	}
	if player.DollDmgReduceRandom != 0 || player.DollDmgReduceRandomChance != 0 {
		t.Fatalf("random doll reduction should be removed: got value=%d chance=%d", player.DollDmgReduceRandom, player.DollDmgReduceRandomChance)
	}
}
