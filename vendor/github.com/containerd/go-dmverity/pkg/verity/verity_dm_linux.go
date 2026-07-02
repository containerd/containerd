/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

//go:build linux

package verity

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/containerd/go-dmverity/pkg/dm"
	"github.com/containerd/go-dmverity/pkg/keyring"
	"github.com/containerd/go-dmverity/pkg/utils"
)

// Open creates a read-only device-mapper verity target. It activates a
// dm-verity device named name using the supplied data and hash devices, root
// hash, and optional signature file. Returns the /dev/mapper path of the
// activated device.
func Open(params *Params, name, dataDevice, hashDevice string, rootHash []byte, signatureFile string, flags []string) (string, error) {
	var keyDesc string
	var keyID keyring.KeySerial

	if signatureFile != "" {
		if err := keyring.CheckKeyringSupport(); err != nil {
			return "", fmt.Errorf("signature verification requires kernel keyring support: %w", err)
		}
		if err := dm.CheckVeritySignatureSupport(); err != nil {
			return "", fmt.Errorf("failed to check dm-verity signature support: %w", err)
		}
	}

	if err := InitParams(params, dataDevice, hashDevice); err != nil {
		return "", fmt.Errorf("InitParams failed: %w", err)
	}

	if err := utils.ValidateRootHashSize(rootHash, params.HashName); err != nil {
		return "", err
	}

	if signatureFile != "" {
		signatureData, err := os.ReadFile(signatureFile)
		if err != nil {
			return "", fmt.Errorf("failed to read signature file: %w", err)
		}

		uuidStr := ""
		if params.UUID != ([16]byte{}) {
			uuidStr = fmt.Sprintf("%x-%x-%x-%x-%x",
				params.UUID[0:4], params.UUID[4:6], params.UUID[6:8],
				params.UUID[8:10], params.UUID[10:16])
		}

		if uuidStr != "" {
			keyDesc = fmt.Sprintf("cryptsetup:%s-%s", uuidStr, name)
		} else {
			keyDesc = fmt.Sprintf("cryptsetup:%s", name)
		}

		keyID, err = keyring.AddKeyToThreadKeyring("user", keyDesc, signatureData)
		if err != nil {
			return "", fmt.Errorf("failed to load signature into keyring: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Loaded signature into thread keyring (key ID: %d, description: %s)\n", keyID, keyDesc)

		defer func() {
			if err := keyring.UnlinkKeyFromThreadKeyring(keyID); err != nil {
				log.Printf("Warning: failed to unlink key from keyring: %v", err)
			}
		}()
	}

	openArgs := dm.OpenArgs{
		Version:            params.HashType,
		DataDevice:         dataDevice,
		HashDevice:         hashDevice,
		DataBlockSize:      params.DataBlockSize,
		HashBlockSize:      params.HashBlockSize,
		DataBlocks:         params.DataBlocks,
		HashName:           params.HashName,
		RootDigest:         rootHash,
		Salt:               params.Salt,
		HashStartBytes:     params.HashAreaOffset,
		Flags:              flags,
		RootHashSigKeyDesc: keyDesc,
	}

	targetParams, err := dm.BuildTargetParams(openArgs)
	if err != nil {
		return "", err
	}

	lengthSectors := params.DataBlocks * uint64(params.DataBlockSize/512)

	c, err := dm.Open()
	if err != nil {
		return "", err
	}
	defer c.Close()

	created := false
	defer func() {
		if !created {
			_ = c.RemoveDevice(name)
		}
	}()

	if _, err := c.CreateDevice(name); err != nil {
		return "", err
	}

	target := dm.Target{
		SectorStart: 0,
		Length:      lengthSectors,
		Type:        "verity",
		Params:      targetParams,
	}

	if err := c.LoadTable(name, []dm.Target{target}); err != nil {
		_ = c.RemoveDevice(name)
		return "", fmt.Errorf("load table: %w", err)
	}

	if err := c.SuspendDevice(name, false); err != nil {
		_ = c.RemoveDevice(name)
		if signatureFile != "" && errors.Is(err, unix.EKEYREJECTED) {
			return "", fmt.Errorf("signature verification failed: key rejected by kernel (check trusted keyring)")
		}
		return "", fmt.Errorf("resume device: %w", err)
	}

	created = true
	devPath := "/dev/mapper/" + name

	for i := 0; i < 50; i++ {
		if _, err := os.Stat(devPath); err == nil {
			return devPath, nil
		}
		time.Sleep(20 * time.Millisecond)
	}

	return devPath, nil
}

// Close tears down a dm-verity device-mapper target by name.
func Close(name string) error {
	c, err := dm.Open()
	if err != nil {
		return fmt.Errorf("open dm control: %w", err)
	}
	defer c.Close()

	_, err = c.DeviceStatus(name)
	if err != nil {
		return fmt.Errorf("device '%s' not found or inaccessible: %w", name, err)
	}

	if err := c.RemoveDevice(name); err != nil {
		return fmt.Errorf("remove device: %w", err)
	}

	return nil
}

// Check reports whether deviceName is an active dm-verity device in the
// verified state. If expectedRootHash is non-empty, the device's table is
// also checked to confirm the root hash matches.
func Check(deviceName string, expectedRootHash []byte) bool {
	c, err := dm.Open()
	if err != nil {
		return false
	}
	defer c.Close()

	devStatus, err := c.DeviceStatus(deviceName)
	if err != nil || !devStatus.ActivePresent {
		return false
	}

	statusFlag, err := c.TableStatus(deviceName, false)
	if err != nil {
		return false
	}

	// Check if the device is in verified state (status contains "V")
	if !strings.Contains(statusFlag, "V") {
		return false
	}

	if len(expectedRootHash) > 0 {
		tableParams, err := c.TableStatus(deviceName, true)
		if err != nil {
			return false
		}

		expectedHex := fmt.Sprintf("%x", expectedRootHash)

		if !strings.Contains(tableParams, expectedHex) {
			return false
		}
	}

	return true
}
