package schema

type Maybe uint8

const (
	Maybe_Absent = Maybe(0)
	Maybe_Null   = Maybe(1)
	Maybe_Value  = Maybe(2)
)
