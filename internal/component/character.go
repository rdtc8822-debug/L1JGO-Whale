package component

// Character stores all character data for an entity.
// Pure data, zero methods — all mutations happen in System functions.
type Character struct {
	DBID      int32
	Name      string
	ClassType int16 // 0-6
	Sex       int16 // 0=male, 1=female
	ClassID   int32 // GFX ID

	Level int16
	Exp   int64
	HP    int16
	MP    int16
	MaxHP int16
	MaxMP int16
	AC    int16

	Str   int16
	Dex   int16
	Con   int16
	Wis   int16
	Cha   int16
	Intel int16

	X       int32
	Y       int32
	MapID   int16
	Heading int16

	Lawful   int32
	Title    string
	ClanID   int32
	ClanName string
	ClanRank int16

	PKCount     int32
	Karma       int32
	BonusStats  int16
	ElixirStats int16
	PartnerID   int32
	Food        int16
	HighLevel   int16
	AccessLevel int16
	Birthday    int32
}
