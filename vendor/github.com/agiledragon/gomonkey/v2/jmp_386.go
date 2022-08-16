package gomonkey

func buildJmpDirective(double uintptr) []byte {
	d0 := byte(double)
	d1 := byte(double >> 8)
	d2 := byte(double >> 16)
	d3 := byte(double >> 24)

	return []byte{
		0xBA, d0, d1, d2, d3, // MOV edx, double
		0xFF, 0x22, // JMP [edx]
	}
}
