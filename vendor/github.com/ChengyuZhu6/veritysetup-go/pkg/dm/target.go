package dm

import (
	"encoding/hex"
	"fmt"
	"strings"
)

type OpenArgs struct {
	Version            uint32
	DataDevice         string
	HashDevice         string
	DataBlockSize      uint32
	HashBlockSize      uint32
	DataBlocks         uint64
	HashName           string
	RootDigest         []byte
	Salt               []byte
	HashStartBytes     uint64
	Flags              []string
	RootHashSigKeyDesc string
}

func BuildTargetParams(a OpenArgs) (string, error) {
	if a.DataDevice == "" || a.HashDevice == "" {
		return "", fmt.Errorf("data/hash device required")
	}
	if a.DataBlockSize == 0 || a.HashBlockSize == 0 {
		return "", fmt.Errorf("block sizes must be non-zero")
	}
	if a.DataBlocks == 0 {
		return "", fmt.Errorf("data blocks must be non-zero")
	}
	if len(a.RootDigest) == 0 {
		return "", fmt.Errorf("root digest required")
	}
	if a.HashStartBytes%uint64(a.HashBlockSize) != 0 {
		return "", fmt.Errorf("hash start %d must be aligned to hash block size %d", a.HashStartBytes, a.HashBlockSize)
	}

	hashStartBlocks := a.HashStartBytes / uint64(a.HashBlockSize)

	algo := strings.ToLower(strings.TrimSpace(a.HashName))
	if algo == "" {
		algo = "sha256"
	}

	rootHex := strings.ToLower(hex.EncodeToString(a.RootDigest))
	saltHex := "-"
	if len(a.Salt) > 0 {
		saltHex = strings.ToLower(hex.EncodeToString(a.Salt))
	}

	b := fmt.Sprintf("%d %s %s %d %d %d %d %s %s %s",
		a.Version,
		a.DataDevice,
		a.HashDevice,
		a.DataBlockSize,
		a.HashBlockSize,
		a.DataBlocks,
		hashStartBlocks,
		algo,
		rootHex,
		saltHex,
	)

	optionalCount := len(a.Flags)
	if a.RootHashSigKeyDesc != "" {
		optionalCount += 2
	}

	if optionalCount > 0 {
		b += fmt.Sprintf(" %d", optionalCount)

		for _, flag := range a.Flags {
			b += " " + flag
		}

		if a.RootHashSigKeyDesc != "" {
			b += fmt.Sprintf(" root_hash_sig_key_desc %s", a.RootHashSigKeyDesc)
		}
	}

	return b, nil
}
