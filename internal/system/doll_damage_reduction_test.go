package system

import (
	"testing"

	"github.com/l1jgo/server/internal/world"
)

func TestDollDamageReductionAffectsIncomingDamageLikeJava(t *testing.T) {
	player := &world.PlayerInfo{}
	player.EquipBonuses.DmgReduction = 3

	got := applyReductionArmorDamage(player, 20, false)
	if got != 17 {
		t.Fatalf("Java Doll_DmgDown 透過 DamageReductionByArmor 扣 incoming damage，got=%d want=17", got)
	}

	got = applyReductionArmorDamage(player, 2, false)
	if got != 0 {
		t.Fatalf("Doll_DmgDown 減傷不可讓傷害變負，got=%d want=0", got)
	}
}
