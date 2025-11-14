package dm

import (
	"encoding/hex"
	"fmt"
	"strings"
)

type OpenArgs struct {
	Version        uint32 // verity target version, usually 1
	DataDevice     string // path to data device
	HashDevice     string // path to hash device
	DataBlockSize  uint32 // bytes
	HashBlockSize  uint32 // bytes
	DataBlocks     uint64 // number of data blocks
	HashName       string // sha256, sha1, sha512
	RootDigest     []byte // root hash digest (binary)
	Salt           []byte // salt (binary)
	HashStartBytes uint64 // byte offset of hash area start on hash device
	Flags          []string
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

	if len(a.Flags) > 0 {
		b += " " + strings.Join(a.Flags, " ")
	}
	return b, nil
}
