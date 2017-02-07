package content

import (
	"fmt"
	"github.com/opencontainers/go-digest"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
)

// // RefRegexp matches valid ref types.
var RefRegexp = regexp.MustCompile(`[a-zA-Z0-9-._]+`)

var (
	ErrRefFormat = fmt.Errorf("invalid ref format")
)

type Ref string

func (ref Ref) Validate() error {
	if !RefRegexp.MatchString(string(ref)) {
		return ErrRefFormat
	}
	return nil
}

func CreateOCILayout(path string, blobRoot string, ref Ref, manifestDigest digest.Digest) error {

	size, err := validateManifest(blobRoot, manifestDigest)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}

	if err := createLayoutFile(path); err != nil {
		return err
	}

	refsDir := filepath.Join(path, "refs")
	if err := os.MkdirAll(refsDir, 0755); err != nil {
		return err
	}
	descriptor := fmt.Sprintf(`{"size":%d,"digest":"%s","mediaType":"application/vnd.oci.image.manifest.list.v1+json"}`, size, manifestDigest.String())

	//Write ref event if it has already exists, as an update action.
	if err := ioutil.WriteFile(filepath.Join(refsDir, string(ref)), []byte(descriptor), 0644); err != nil {
		return err
	}
	return nil
}

func createLayoutFile(path string) error {
	layoutFile := filepath.Join(path, "oci-layout")
	if _, e := os.Stat(layoutFile); os.IsNotExist(e) {
		if err := ioutil.WriteFile(layoutFile, []byte(`{"imageLayoutVersion":"1.0.0"}`), 0644); err != nil {
			return err
		}
	}
	return nil
}

func validateManifest(blobRoot string, manifestDigest digest.Digest) (int64, error) {
	cs, err := Open(blobRoot)
	if err != nil {
		return 0, err
	}

	manifest := cs.blobPath(manifestDigest)

	stat, err := os.Stat(manifest)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("manifest %s is not exists.", manifestDigest)
		}
		return 0, err
	}

	//TODO check manifest format
	return stat.Size(), nil
}
