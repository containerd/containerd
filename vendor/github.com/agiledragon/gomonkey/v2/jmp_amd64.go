package gomonkey

func buildJmpDirective(double uintptr) []byte {
    d0 := byte(double)
    d1 := byte(double >> 8)
    d2 := byte(double >> 16)
    d3 := byte(double >> 24)
    d4 := byte(double >> 32)
    d5 := byte(double >> 40)
    d6 := byte(double >> 48)
    d7 := byte(double >> 56)

    return []byte{
        0x48, 0xBA, d0, d1, d2, d3, d4, d5, d6, d7, // MOV rdx, double
        0xFF, 0x22,     // JMP [rdx]
    }
}

