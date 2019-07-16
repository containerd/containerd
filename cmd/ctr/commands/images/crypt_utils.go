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

package images

import (
	gocontext "context"

	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	imgenc "github.com/containerd/containerd/images/encryption"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/pkg/encryption"
	encconfig "github.com/containerd/containerd/pkg/encryption/config"
	encutils "github.com/containerd/containerd/pkg/encryption/utils"
	"github.com/containerd/containerd/platforms"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// LayerInfo holds information about an image layer
type LayerInfo struct {
	// The Number of this layer in the sequence; starting at 0
	Index      uint32
	Descriptor ocispec.Descriptor
}

// isUserSelectedLayer checks whether a layer is user-selected given its number
// A layer can be described with its (positive) index number or its negative number.
// The latter is counted relative to the topmost one (-1), the former relative to
// the bottommost one (0).
func isUserSelectedLayer(layerIndex, layersTotal int32, layers []int32) bool {
	if len(layers) == 0 {
		// convenience for the user; none given means 'all'
		return true
	}
	negNumber := layerIndex - layersTotal

	for _, l := range layers {
		if l == negNumber || l == layerIndex {
			return true
		}
	}
	return false
}

// isUserSelectedPlatform determines whether the platform matches one in
// the array of user-provided platforms
func isUserSelectedPlatform(platform *ocispec.Platform, platformList []ocispec.Platform) bool {
	if len(platformList) == 0 {
		// convenience for the user; none given means 'all'
		return true
	}
	matcher := platforms.NewMatcher(*platform)

	for _, platform := range platformList {
		if matcher.Match(platform) {
			return true
		}
	}
	return false
}

// processRecipientKeys sorts the array of recipients by type. Recipients may be either
// x509 certificates, public keys, or PGP public keys identified by email address or name
func processRecipientKeys(recipients []string) ([][]byte, [][]byte, [][]byte, error) {
	var (
		gpgRecipients [][]byte
		pubkeys       [][]byte
		x509s         [][]byte
	)
	for _, recipient := range recipients {
		tmp, err := ioutil.ReadFile(recipient)
		if err != nil {
			gpgRecipients = append(gpgRecipients, []byte(recipient))
			continue
		}
		if encutils.IsCertificate(tmp) {
			x509s = append(x509s, tmp)
		} else if encutils.IsPublicKey(tmp) {
			pubkeys = append(pubkeys, tmp)
		} else {
			gpgRecipients = append(gpgRecipients, []byte(recipient))
		}
	}
	return gpgRecipients, pubkeys, x509s, nil
}

// Process a password that may be in any of the following formats:
// - file=<passwordfile>
// - pass=<password>
// - fd=<filedescriptor>
// - <password>
func processPwdString(pwdString string) ([]byte, error) {
	if strings.HasPrefix(pwdString, "file=") {
		return ioutil.ReadFile(pwdString[5:])
	} else if strings.HasPrefix(pwdString, "pass=") {
		return []byte(pwdString[5:]), nil
	} else if strings.HasPrefix(pwdString, "fd=") {
		fdStr := pwdString[3:]
		fd, err := strconv.Atoi(fdStr)
		if err != nil {
			return nil, errors.Wrapf(err, "could not parse file descriptor %s", fdStr)
		}
		f := os.NewFile(uintptr(fd), "pwdfile")
		if f == nil {
			return nil, fmt.Errorf("%s is not a valid file descriptor", fdStr)
		}
		defer f.Close()
		pwd := make([]byte, 64)
		n, err := f.Read(pwd)
		if err != nil {
			return nil, errors.Wrapf(err, "could not read from file descriptor")
		}
		return pwd[:n], nil
	}
	return []byte(pwdString), nil
}

// processPrivateKeyFiles sorts the different types of private key files; private key files may either be
// private keys or GPG private key ring files. The private key files may include the password for the
// private key and take any of the following forms:
// - <filename>
// - <filename>:file=<passwordfile>
// - <filename>:pass=<password>
// - <filename>:fd=<filedescriptor>
// - <filename>:<password>
func processPrivateKeyFiles(keyFilesAndPwds []string) ([][]byte, [][]byte, [][]byte, [][]byte, error) {
	var (
		gpgSecretKeyRingFiles [][]byte
		gpgSecretKeyPasswords [][]byte
		privkeys              [][]byte
		privkeysPasswords     [][]byte
		err                   error
	)
	// keys needed for decryption in case of adding a recipient
	for _, keyfileAndPwd := range keyFilesAndPwds {
		var password []byte

		parts := strings.Split(keyfileAndPwd, ":")
		if len(parts) == 2 {
			password, err = processPwdString(parts[1])
			if err != nil {
				return nil, nil, nil, nil, err
			}
		}

		keyfile := parts[0]
		tmp, err := ioutil.ReadFile(keyfile)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		isPrivKey, err := encutils.IsPrivateKey(tmp, password)
		if encutils.IsPasswordError(err) {
			return nil, nil, nil, nil, err
		}
		if isPrivKey {
			privkeys = append(privkeys, tmp)
			privkeysPasswords = append(privkeysPasswords, password)
		} else if encutils.IsGPGPrivateKeyRing(tmp) {
			gpgSecretKeyRingFiles = append(gpgSecretKeyRingFiles, tmp)
			gpgSecretKeyPasswords = append(gpgSecretKeyPasswords, password)
		} else {
			return nil, nil, nil, nil, fmt.Errorf("unidentified private key in file %s (password=%s)", keyfile, string(password))
		}
	}
	return gpgSecretKeyRingFiles, gpgSecretKeyPasswords, privkeys, privkeysPasswords, nil
}

func createGPGClient(context *cli.Context) (encryption.GPGClient, error) {
	return encryption.NewGPGClient(context.String("gpg-version"), context.String("gpg-homedir"))
}

func getGPGPrivateKeys(context *cli.Context, gpgSecretKeyRingFiles [][]byte, descs []ocispec.Descriptor, mustFindKey bool, dcparameters map[string][][]byte) error {
	gpgClient, err := createGPGClient(context)
	if err != nil {
		return err
	}

	var gpgVault encryption.GPGVault
	if len(gpgSecretKeyRingFiles) > 0 {
		gpgVault = encryption.NewGPGVault()
		err = gpgVault.AddSecretKeyRingDataArray(gpgSecretKeyRingFiles)
		if err != nil {
			return err
		}
	}
	return encryption.GPGGetPrivateKey(descs, gpgClient, gpgVault, mustFindKey, dcparameters)
}

func createLayerFilter(client *containerd.Client, ctx gocontext.Context, desc ocispec.Descriptor, layers []int32, platformList []ocispec.Platform) (imgenc.LayerFilter, error) {
	alldescs, err := images.GetImageLayerDescriptors(ctx, client.ContentStore(), desc)
	if err != nil {
		return nil, err
	}

	_, descs := filterLayerDescriptors(alldescs, layers, platformList)

	lf := func(d ocispec.Descriptor) bool {
		for _, desc := range descs {
			if desc.Digest.String() == d.Digest.String() {
				return true
			}
		}
		return false
	}
	return lf, nil
}

// cryptImage encrypts or decrypts an image with the given name and stores it either under the newName
// or updates the existing one
func cryptImage(client *containerd.Client, ctx gocontext.Context, name, newName string, cc *encconfig.CryptoConfig, layers []int32, platformList []string, encrypt bool) (images.Image, error) {
	s := client.ImageService()

	image, err := s.Get(ctx, name)
	if err != nil {
		return images.Image{}, err
	}

	pl, err := parsePlatformArray(platformList)
	if err != nil {
		return images.Image{}, err
	}

	lf, err := createLayerFilter(client, ctx, image.Target, layers, pl)
	if err != nil {
		return images.Image{}, err
	}

	var (
		modified bool
		newSpec  ocispec.Descriptor
	)

	ls := client.LeasesService()
	l, err := ls.Create(ctx, leases.WithRandomID(), leases.WithExpiration(5*time.Minute))
	if err != nil {
		return images.Image{}, err
	}
	defer ls.Delete(ctx, l, leases.SynchronousDelete)

	if encrypt {
		newSpec, modified, err = imgenc.EncryptImage(ctx, client.ContentStore(), ls, l, image.Target, cc, lf)
	} else {
		newSpec, modified, err = imgenc.DecryptImage(ctx, client.ContentStore(), ls, l, image.Target, cc, lf)
	}
	if err != nil {
		return image, err
	}
	if !modified {
		return image, nil
	}

	image.Target = newSpec

	// if newName is either empty or equal to the existing name, it's an update
	if newName == "" || strings.Compare(image.Name, newName) == 0 {
		// first Delete the existing and then Create a new one
		// We have to do it this way since we have a newSpec!
		err = s.Delete(ctx, image.Name)
		if err != nil {
			return images.Image{}, err
		}
		newName = image.Name
	}

	image.Name = newName
	return s.Create(ctx, image)
}

func encryptImage(client *containerd.Client, ctx gocontext.Context, name, newName string, cc *encconfig.CryptoConfig, layers []int32, platformList []string) (images.Image, error) {
	return cryptImage(client, ctx, name, newName, cc, layers, platformList, true)
}

func decryptImage(client *containerd.Client, ctx gocontext.Context, name, newName string, cc *encconfig.CryptoConfig, layers []int32, platformList []string) (images.Image, error) {
	return cryptImage(client, ctx, name, newName, cc, layers, platformList, false)
}

func getImageLayerInfos(client *containerd.Client, ctx gocontext.Context, name string, layers []int32, platformList []string) ([]LayerInfo, []ocispec.Descriptor, error) {
	s := client.ImageService()

	image, err := s.Get(ctx, name)
	if err != nil {
		return nil, nil, err
	}

	pl, err := parsePlatformArray(platformList)
	if err != nil {
		return nil, nil, err
	}

	alldescs, err := images.GetImageLayerDescriptors(ctx, client.ContentStore(), image.Target)
	if err != nil {
		return nil, nil, err
	}

	lis, descs := filterLayerDescriptors(alldescs, layers, pl)
	return lis, descs, nil
}

func countLayers(descs []ocispec.Descriptor, platform *ocispec.Platform) int32 {
	c := int32(0)

	for _, desc := range descs {
		if desc.Platform == platform {
			c = c + 1
		}
	}

	return c
}

func filterLayerDescriptors(alldescs []ocispec.Descriptor, layers []int32, pl []ocispec.Platform) ([]LayerInfo, []ocispec.Descriptor) {
	var (
		layerInfos  []LayerInfo
		descs       []ocispec.Descriptor
		curplat     *ocispec.Platform
		layerIndex  int32
		layersTotal int32
	)

	for _, desc := range alldescs {
		if curplat != desc.Platform {
			curplat = desc.Platform
			layerIndex = 0
			layersTotal = countLayers(alldescs, desc.Platform)
		} else {
			layerIndex = layerIndex + 1
		}

		if isUserSelectedLayer(layerIndex, layersTotal, layers) && isUserSelectedPlatform(curplat, pl) {
			li := LayerInfo{
				Index:      uint32(layerIndex),
				Descriptor: desc,
			}
			descs = append(descs, desc)
			layerInfos = append(layerInfos, li)
		}
	}
	return layerInfos, descs
}

// CreateDcParameters creates the decryption parameter map from command line options and possibly
// LayerInfos describing the image and helping us to query for the PGP decryption keys
func CreateDcParameters(context *cli.Context, descs []ocispec.Descriptor) (map[string][][]byte, error) {
	dcparameters := make(map[string][][]byte)

	// x509 cert is needed for PKCS7 decryption
	_, _, x509s, err := processRecipientKeys(context.StringSlice("dec-recipient"))
	if err != nil {
		return nil, err
	}

	gpgSecretKeyRingFiles, gpgSecretKeyPasswords, privKeys, privKeysPasswords, err := processPrivateKeyFiles(context.StringSlice("key"))
	if err != nil {
		return nil, err
	}

	_, err = createGPGClient(context)
	gpgInstalled := err == nil
	if gpgInstalled {
		if len(gpgSecretKeyRingFiles) == 0 && len(privKeys) == 0 && descs != nil {
			// Get pgp private keys from keyring only if no private key was passed
			err = getGPGPrivateKeys(context, gpgSecretKeyRingFiles, descs, true, dcparameters)
			if err != nil {
				return nil, err
			}
		} else {
			if len(gpgSecretKeyRingFiles) == 0 {
				dcparameters["gpg-client"] = [][]byte{[]byte("1")}
				dcparameters["gpg-client-version"] = [][]byte{[]byte(context.String("gpg-version"))}
				dcparameters["gpg-client-homedir"] = [][]byte{[]byte(context.String("gpg-homedir"))}
			} else {
				dcparameters["gpg-privatekeys"] = gpgSecretKeyRingFiles
				dcparameters["gpg-privatekeys-passwords"] = gpgSecretKeyPasswords
			}
		}
	}
	dcparameters["privkeys"] = privKeys
	dcparameters["privkeys-passwords"] = privKeysPasswords
	dcparameters["x509s"] = x509s

	return dcparameters, nil
}

// parsePlatformArray parses an array of specifiers and converts them into an array of specs.Platform
func parsePlatformArray(specifiers []string) ([]ocispec.Platform, error) {
	var speclist []ocispec.Platform

	for _, specifier := range specifiers {
		spec, err := platforms.Parse(specifier)
		if err != nil {
			return []ocispec.Platform{}, err
		}
		speclist = append(speclist, spec)
	}
	return speclist, nil
}
