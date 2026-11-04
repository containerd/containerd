/*
Copyright The Kubernetes Authors.

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

package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
	"sigs.k8s.io/security-profiles-merger/spm"
)

// errNotAnObject is returned by Unmarshal for a document that is not a JSON
// object, the shape of every profile.
var errNotAnObject = errors.New("decode profile: not a JSON object")

// jsonSpace holds the bytes JSON allows between tokens.
const jsonSpace = " \t\r\n"

// Unmarshal decodes one JSON document into target and refuses what
// encoding/json accepts silently: a byte that is not valid UTF-8
// (spm.ErrInvalidUTF8), a member repeated within one object
// (spm.ErrDuplicateKey), a member the target type has no field for
// (spm.ErrUnknownField), a member that names a field only ignoring case
// (spm.ErrMisspelledField), a required member left out or null
// (spm.ErrMissingField), and anything but whitespace behind the document
// (spm.ErrUnexpectedData). A document that is not a JSON object is refused
// too: encoding/json decodes null into a struct as nothing at all, which
// would make it an empty profile.
//
// Every message is bounded (see merge.BoundedText), since the decoder quotes
// back literals whose length the document chooses.
//
// It reports the first kind of problem it finds, naming every member of that
// kind up to a bound, rather than collecting all kinds: a document that
// fails one check is not one whose other findings can be trusted. target is
// only written once every check has passed, so a caller that goes on after
// an error does not hold what the document said.
func Unmarshal[T any](data []byte, target *T) error {
	err := InvalidUTF8(data)
	if err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))

	var decoded T

	err = decoder.Decode(&decoded)
	if err != nil {
		return fmt.Errorf("decode profile: %w", merge.BoundedError(err))
	}

	// Decode stops at the end of the first value, so anything behind it
	// would otherwise be ignored.
	_, err = decoder.Token()
	if !errors.Is(err, io.EOF) {
		return spm.ErrUnexpectedData
	}

	// Any other value but an object fails to decode into a struct.
	if bytes.HasPrefix(bytes.TrimLeft(data, jsonSpace), []byte("null")) {
		return errNotAnObject
	}

	paths, omitted := DuplicateKeys(data)

	err = PathsError(spm.ErrDuplicateKey, paths, omitted)
	if err != nil {
		return err
	}

	paths, omitted = UnknownFieldsOf[T](data)

	err = PathsError(spm.ErrUnknownField, paths, omitted)
	if err != nil {
		return err
	}

	paths, omitted = MisspelledFieldsOf[T](data)

	err = PathsError(spm.ErrMisspelledField, paths, omitted)
	if err != nil {
		return err
	}

	paths, omitted = MissingFieldsOf(data, &decoded)

	err = PathsError(spm.ErrMissingField, paths, omitted)
	if err != nil {
		return err
	}

	*target = decoded

	return nil
}
