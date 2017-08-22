package testsuite

const umountflags int = 0

func clearMask() func() {
	return func() {}
}
