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
	"fmt"
	"io/ioutil"
	"os/exec"
)

// GPGVersion enum representing the versino of GPG client to use.
type GPGVersion int

const (
	// GPGv2 signifies gpgv2+
	GPGv2 GPGVersion = iota
	// GPGv1 signifies gpgv1+
	GPGv1
	// GPGVersionUndetermined signifies gpg client version undetermined
	GPGVersionUndetermined
)

// GPGClient defines an interface for wrapping the gpg command line tools
type GPGClient interface {
	// ReadGPGPubRingFile gets the byte sequence of the gpg public keyring
	ReadGPGPubRingFile() ([]byte, error)
	// GetGPGPrivateKey gets the private key bytes of a keyid given a passphrase
	GetGPGPrivateKey(keyid uint64, passphrase string) ([]byte, error)
	// GetSecretKeyDetails gets the details of a secret key
	GetSecretKeyDetails(keyid uint64) ([]byte, bool, error)
}

// gpgClient contains generic gpg client information
type gpgClient struct {
	gpgHomeDir string
}

// gpgv2Client is a gpg2 client
type gpgv2Client struct {
	gpgClient
}

// gpgv1Client is a gpg client
type gpgv1Client struct {
	gpgClient
}

// GuessGPGVersion guesses the version of gpg. Defaults to gpg2 if exists, if
// not defaults to regular gpg.
func GuessGPGVersion() GPGVersion {
	if err := exec.Command("gpg2", "--version").Run(); err == nil {
		return GPGv2
	} else if err := exec.Command("gpg", "--version").Run(); err == nil {
		return GPGv1
	} else {
		return GPGVersionUndetermined
	}
}

// NewGPGClient creates a new GPGClient object representing the given version
// and using the given home directory
func NewGPGClient(version *GPGVersion, homedir string) (GPGClient, error) {
	var gpgVersion GPGVersion
	if version != nil {
		gpgVersion = *version
	} else {
		gpgVersion = GuessGPGVersion()
	}

	switch gpgVersion {
	case GPGv1:
		return &gpgv1Client{
			gpgClient: gpgClient{gpgHomeDir: homedir},
		}, nil
	case GPGv2:
		return &gpgv2Client{
			gpgClient: gpgClient{gpgHomeDir: homedir},
		}, nil
	case GPGVersionUndetermined:
		return nil, fmt.Errorf("Unable to determine GPG version")
	default:
		return nil, fmt.Errorf("Unhandled case: NewGPGClient")
	}
}

// GetGPGPrivateKey gets the bytes of a specified keyid, supplying a passphrase
func (gc *gpgv2Client) GetGPGPrivateKey(keyid uint64, passphrase string) ([]byte, error) {
	var args []string

	if gc.gpgHomeDir != "" {
		args = append(args, []string{"--homedir", gc.gpgHomeDir}...)
	}

	args = append(args, []string{"--pinentry-mode", "loopback", "--batch", "--passphrase", passphrase, "--export-secret-key", fmt.Sprintf("0x%x", keyid)}...)

	cmd := exec.Command("gpg2", args...)

	return runGPGGetOutput(cmd)
}

// ReadGPGPubRingFile reads the GPG public key ring file
func (gc *gpgv2Client) ReadGPGPubRingFile() ([]byte, error) {
	var args []string

	if gc.gpgHomeDir != "" {
		args = append(args, []string{"--homedir", gc.gpgHomeDir}...)
	}
	args = append(args, []string{"--batch", "--export"}...)

	cmd := exec.Command("gpg2", args...)

	return runGPGGetOutput(cmd)
}

// GetSecretKeyDetails retrives the secret key details of key with keyid.
// returns a byte array of the details and a bool if the key exists
func (gc *gpgv2Client) GetSecretKeyDetails(keyid uint64) ([]byte, bool, error) {
	var args []string

	if gc.gpgHomeDir != "" {
		args = append([]string{"--homedir", gc.gpgHomeDir})
	}
	args = append(args, "-K", fmt.Sprintf("0x%x", keyid))

	cmd := exec.Command("gpg2", args...)

	keydata, err := runGPGGetOutput(cmd)
	return keydata, err == nil, err
}

// GetGPGPrivateKey gets the bytes of a specified keyid, supplying a passphrase
func (gc *gpgv1Client) GetGPGPrivateKey(keyid uint64, _ string) ([]byte, error) {
	var args []string

	if gc.gpgHomeDir != "" {
		args = append(args, []string{"--homedir", gc.gpgHomeDir}...)
	}
	args = append(args, []string{"--batch", "--export-secret-key", fmt.Sprintf("0x%x", keyid)}...)

	cmd := exec.Command("gpg", args...)

	return runGPGGetOutput(cmd)
}

// ReadGPGPubRingFile reads the GPG public key ring file
func (gc *gpgv1Client) ReadGPGPubRingFile() ([]byte, error) {
	var args []string

	if gc.gpgHomeDir != "" {
		args = append(args, []string{"--homedir", gc.gpgHomeDir}...)
	}
	args = append(args, []string{"--batch", "--export"}...)

	cmd := exec.Command("gpg", args...)

	return runGPGGetOutput(cmd)
}

// GetSecretKeyDetails retrives the secret key details of key with keyid.
// returns a byte array of the details and a bool if the key exists
func (gc *gpgv1Client) GetSecretKeyDetails(keyid uint64) ([]byte, bool, error) {
	var args []string

	if gc.gpgHomeDir != "" {
		args = append([]string{"--homedir", gc.gpgHomeDir})
	}
	args = append(args, "-K", fmt.Sprintf("0x%x", keyid))

	cmd := exec.Command("gpg", args...)

	keydata, err := runGPGGetOutput(cmd)

	return keydata, err == nil, err
}

// runGPGGetOutput runs the GPG commandline and returns stdout as byte array
// and any stderr in the error
func runGPGGetOutput(cmd *exec.Cmd) ([]byte, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	stdoutstr, err2 := ioutil.ReadAll(stdout)
	stderrstr, _ := ioutil.ReadAll(stderr)

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("Error from %s: %s", cmd.Path, string(stderrstr))
	}

	return stdoutstr, err2
}
