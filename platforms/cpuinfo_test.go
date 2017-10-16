package platforms

import (
	"runtime"
	"testing"
)

func TestCPUVariant(t *testing.T) {
	if !isArmArch(runtime.GOARCH) || !isLinuxOS(runtime.GOOS) {
		t.Skip("only relevant on linux/arm")
	}

	variants := []string{"v8", "v7", "v6", "v5", "v4", "v3"}

	p := getCPUVariant()
	for _, variant := range variants {
		if p == variant {
			t.Logf("got valid variant as expected: %#v = %#v\n", p, variant)
			return
		}
	}

	t.Fatalf("could not get valid variant as expected: %v\n", variants)
}
