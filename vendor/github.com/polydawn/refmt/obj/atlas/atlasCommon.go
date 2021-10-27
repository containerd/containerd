package atlas

// A type to enumerate key sorting modes.
type KeySortMode string

const (
	KeySortMode_Default = KeySortMode("default") // the default mode -- for structs, this is the source-order of the fields; for maps, it's identify to "strings" sort mode.
	KeySortMode_Strings = KeySortMode("strings") // lexical sort by strings.  this *is* the default for maps; it overrides source-order sorting for structs.
	KeySortMode_RFC7049 = KeySortMode("rfc7049") // "Canonical" as proposed by rfc7049 § 3.9 (shorter byte sequences sort to top).
)
