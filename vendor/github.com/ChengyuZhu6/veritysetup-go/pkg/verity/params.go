package verity

const (
	VeritySignature      = "verity\x00\x00"
	VeritySuperblockSize = 512
	VerityMaxHashType    = 1
	VerityMaxLevels      = 63
	VerityMaxDigestSize  = 1024
	MaxSaltSize          = 256
	diskSectorSize       = 512
)

type VerityParams struct {
	HashName       string
	DataBlockSize  uint32
	HashBlockSize  uint32
	DataBlocks     uint64
	HashType       uint32
	Salt           []byte
	SaltSize       uint16
	HashAreaOffset uint64
	NoSuperblock   bool
	UUID           [16]byte
}

func DefaultVerityParams() VerityParams {
	return VerityParams{
		HashName: "sha256",
		HashType: 1,
	}
}

func IsBlockSizeValid(size uint32) bool {
	return size%512 == 0 && size >= 512 && size <= (512*1024) && (size&(size-1)) == 0
}
