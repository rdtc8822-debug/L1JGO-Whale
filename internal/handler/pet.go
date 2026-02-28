package handler

// Pet collar item IDs (amulets that store pet data).
const (
	petCollarNormal int32 = 40314
	petCollarHigher int32 = 40316
)

// isPetCollar returns true if the item is a pet collar (amulet).
func isPetCollar(itemID int32) bool {
	return itemID == petCollarNormal || itemID == petCollarHigher
}
