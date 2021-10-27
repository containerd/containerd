package selector

const (
	SelectorKey_Matcher              = "."
	SelectorKey_ExploreAll           = "a"
	SelectorKey_ExploreFields        = "f"
	SelectorKey_ExploreIndex         = "i"
	SelectorKey_ExploreRange         = "r"
	SelectorKey_ExploreRecursive     = "R"
	SelectorKey_ExploreUnion         = "|"
	SelectorKey_ExploreConditional   = "&"
	SelectorKey_ExploreRecursiveEdge = "@"
	SelectorKey_Next                 = ">"
	SelectorKey_Fields               = "f>"
	SelectorKey_Index                = "i"
	SelectorKey_Start                = "^"
	SelectorKey_End                  = "$"
	SelectorKey_Sequence             = ":>"
	SelectorKey_Limit                = "l"
	SelectorKey_LimitDepth           = "depth"
	SelectorKey_LimitNone            = "none"
	SelectorKey_StopAt               = "!"
	SelectorKey_Condition            = "&"
	// not filling conditional keys since it's not complete
)
