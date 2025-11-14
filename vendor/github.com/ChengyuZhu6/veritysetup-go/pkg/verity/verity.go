package verity

import (
	"bytes"
	"crypto"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/ChengyuZhu6/veritysetup-go/pkg/utils"
)

func validateParams(params *VerityParams, digestSize int) error {
	if params == nil {
		return errors.New("verity: nil params")
	}

	if params.HashType > VerityMaxHashType {
		return fmt.Errorf("verity: unsupported hash type %d", params.HashType)
	}
	if params.HashName == "" {
		return errors.New("verity: hash algorithm required")
	}

	if params.SaltSize > MaxSaltSize {
		return fmt.Errorf("salt size %d exceeds maximum of %d bytes", params.SaltSize, MaxSaltSize)
	}

	if digestSize > VerityMaxDigestSize {
		return fmt.Errorf("digest size %d exceeds maximum of %d bytes", digestSize, VerityMaxDigestSize)
	}

	if !utils.IsBlockSizeValid(params.DataBlockSize) {
		return fmt.Errorf("invalid data block size: %d", params.DataBlockSize)
	}
	if !utils.IsBlockSizeValid(params.HashBlockSize) {
		return fmt.Errorf("invalid hash block size: %d", params.HashBlockSize)
	}

	if utils.Uint64MultOverflow(params.DataBlocks, uint64(params.DataBlockSize)) {
		return fmt.Errorf("data device offset overflow: %d blocks * %d bytes",
			params.DataBlocks, params.DataBlockSize)
	}

	if params.NoSuperblock {
		if params.HashAreaOffset%uint64(params.HashBlockSize) != 0 {
			return fmt.Errorf("hash offset %d must be aligned to hash block size %d", params.HashAreaOffset, params.HashBlockSize)
		}
	} else {
		if params.HashAreaOffset == 0 {
			return errors.New("verity: hash area offset not initialised for superblock mode")
		}
	}

	pageSize := uint32(unix.Getpagesize())
	if params.DataBlockSize > pageSize {
		log.Printf("WARNING: Kernel cannot activate device if data block size (%d) exceeds page size (%d)",
			params.DataBlockSize, pageSize)
	}

	return nil
}

func VerityVerify(params *VerityParams, dataDevice, hashDevice string, rootHash []byte) error {
	if params == nil {
		return errors.New("verity: nil params")
	}

	if !params.NoSuperblock {
		hashFile, err := os.Open(hashDevice)
		if err != nil {
			return fmt.Errorf("cannot open hash device: %w", err)
		}
		defer hashFile.Close()

		sbOffset := uint64(0)
		if dataDevice == hashDevice && params.HashAreaOffset > 0 {
			sbOffset = params.HashAreaOffset
		}

		sb, err := ReadSuperblock(hashFile, sbOffset)
		if err != nil {
			return err
		}

		if err := adoptParamsFromSuperblock(params, sb, sbOffset); err != nil {
			return err
		}
	}

	vh := NewVerityHash(
		params.HashName,
		params.DataBlockSize, params.HashBlockSize,
		params.DataBlocks,
		params.HashType,
		params.Salt,
		params.HashAreaOffset,
		dataDevice, hashDevice,
		rootHash,
	)

	if err := validateParams(params, vh.hashFunc.Size()); err != nil {
		return err
	}

	return vh.CreateOrVerifyHashTree(true)
}

func VerityCreate(params *VerityParams, dataDevice, hashDevice string) ([]byte, error) {
	if params == nil {
		return nil, errors.New("verity: nil params")
	}

	var sbOffset uint64
	if !params.NoSuperblock {
		sb, err := buildSuperblockFromParams(params)
		if err != nil {
			return nil, err
		}

		openFlags := os.O_RDWR | os.O_CREATE
		if dataDevice == hashDevice {
			sbOffset = params.HashAreaOffset
			params.HashAreaOffset = sbOffset + utils.AlignUp(uint64(VeritySuperblockSize), uint64(params.HashBlockSize))
		} else {
			openFlags |= os.O_TRUNC
			sbOffset = 0
		}

		hashFile, err := os.OpenFile(hashDevice, openFlags, 0644)
		if err != nil {
			return nil, fmt.Errorf("cannot open or create hash device: %w", err)
		}
		defer hashFile.Close()

		if err := sb.WriteSuperblock(hashFile, sbOffset); err != nil {
			return nil, err
		}
	}

	vh := NewVerityHash(
		params.HashName,
		params.DataBlockSize, params.HashBlockSize,
		params.DataBlocks,
		params.HashType,
		params.Salt,
		params.HashAreaOffset,
		dataDevice, hashDevice,
		nil,
	)

	if err := validateParams(params, vh.hashFunc.Size()); err != nil {
		return nil, err
	}

	if err := vh.CreateOrVerifyHashTree(false); err != nil {
		return nil, err
	}

	return vh.RootHash(), nil
}

func VerifyBlock(params *VerityParams, hashName string, data, salt, expectedHash []byte) error {
	vh := &VerityHash{
		hashType: params.HashType,
		hashFunc: func() crypto.Hash {
			hashMap := map[string]crypto.Hash{
				"sha256": crypto.SHA256,
				"sha512": crypto.SHA512,
				"sha1":   crypto.SHA1,
			}
			if h, ok := hashMap[hashName]; ok && h.Available() {
				return h
			}
			return crypto.SHA256
		}(),
	}

	calculatedHash, err := vh.verifyHashBlock(data, salt)
	if err != nil {
		return err
	}

	if !bytes.Equal(calculatedHash, expectedHash) {
		return fmt.Errorf("block hash mismatch")
	}

	return nil
}

func InitParams(params *VerityParams, dataLoop, hashLoop string) error {
	if params == nil {
		return errors.New("verity: nil params")
	}

	if !params.NoSuperblock {
		f, err := os.OpenFile(hashLoop, os.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("open hash device: %w", err)
		}
		defer f.Close()

		sbOffset := uint64(0)
		if dataLoop == hashLoop && params.HashAreaOffset > 0 {
			sbOffset = params.HashAreaOffset
		}

		sb, err := ReadSuperblock(f, sbOffset)
		if err != nil {
			return fmt.Errorf("device is not a valid VERITY device: %w", err)
		}

		if err := adoptParamsFromSuperblock(params, sb, sbOffset); err != nil {
			return fmt.Errorf("failed to adopt params from superblock: %w", err)
		}
	} else {
		if params.HashAreaOffset%uint64(params.HashBlockSize) != 0 {
			return fmt.Errorf("hash offset %d must be aligned to hash block size %d", params.HashAreaOffset, params.HashBlockSize)
		}

		if params.DataBlocks == 0 {
			size, err := utils.GetBlockOrFileSize(dataLoop)
			if err != nil {
				return fmt.Errorf("determine data device size: %w", err)
			}
			if size%int64(params.DataBlockSize) != 0 {
				return fmt.Errorf("data size %d not multiple of data block size %d", size, params.DataBlockSize)
			}
			params.DataBlocks = uint64(size / int64(params.DataBlockSize))
		}
	}

	return nil
}

func DumpDevice(hashPath string) (string, error) {
	hashFile, err := os.Open(hashPath)
	if err != nil {
		return "", fmt.Errorf("cannot open hash device: %w", err)
	}
	defer hashFile.Close()

	params := &VerityParams{}
	superblock, err := ReadSuperblock(hashFile, 0)
	if err != nil {
		return "", fmt.Errorf("failed to read superblock: %w", err)
	}

	if err := adoptParamsFromSuperblock(params, superblock, 0); err != nil {
		return "", fmt.Errorf("failed to adopt params from superblock: %w", err)
	}

	uuidStr, err := superblock.UUIDString()
	if err != nil {
		return "", fmt.Errorf("failed to get UUID string: %w", err)
	}

	digestSize := utils.SelectHashSize(params.HashName)
	if digestSize <= 0 {
		return "", fmt.Errorf("unsupported hash algorithm: %s", params.HashName)
	}

	hashPerBlock := params.HashBlockSize / uint32(digestSize)
	hashBlocks := (params.DataBlocks + uint64(hashPerBlock) - 1) / uint64(hashPerBlock)

	deviceSizeBytes := params.HashAreaOffset + hashBlocks*uint64(params.HashBlockSize)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\nVERITY header information for %s\n", hashPath))
	sb.WriteString(fmt.Sprintf("UUID:            \t%s\n", uuidStr))
	sb.WriteString(fmt.Sprintf("Hash type:       \t%d\n", params.HashType))
	sb.WriteString(fmt.Sprintf("Data blocks:     \t%d\n", params.DataBlocks))
	sb.WriteString(fmt.Sprintf("Data block size: \t%d\n", params.DataBlockSize))
	sb.WriteString(fmt.Sprintf("Hash blocks:     \t%d\n", hashBlocks))
	sb.WriteString(fmt.Sprintf("Hash block size: \t%d\n", params.HashBlockSize))
	sb.WriteString(fmt.Sprintf("Hash algorithm:  \t%s\n", params.HashName))

	sb.WriteString("Salt:            \t")
	if params.SaltSize > 0 {
		sb.WriteString(fmt.Sprintf("%x\n", params.Salt[:params.SaltSize]))
	} else {
		sb.WriteString("-\n")
	}

	sb.WriteString(fmt.Sprintf("Hash device size: \t%d [bytes]\n", deviceSizeBytes))

	return sb.String(), nil
}
