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

// Package securityprofile stores validated OCI seccomp artifacts independently of images.
package securityprofile

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/v2/pkg/atomicfile"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/distribution/reference"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	merger "sigs.k8s.io/security-profiles-merger/seccomp"
)

// MaxSize bounds an untrusted seccomp document before decoding it.
const (
	MaxSize              = 1 << 20
	MaxRefsPerProfile    = 256
	MaxRecordSize        = 2 << 20
	currentRecordVersion = 1
	staleTempFileAge     = 24 * time.Hour
)

var errCorruptRecord = errors.New("corrupt security profile record")

func corruptRecord(err error) error {
	return fmt.Errorf("%w: %v", errCorruptRecord, err)
}

// Store has no eviction policy: the kubelet owns profile lifetime.
// Registry operations must happen outside its lock.
type Store struct {
	mu   sync.RWMutex
	root string
	// sizes and size are updated under mu only after successful disk mutations.
	sizes map[digest.Digest]uint64
	size  uint64
	usage atomic.Pointer[storeUsage]
}

type storeUsage struct {
	count uint64
	size  uint64
}

type record struct {
	Version int             `json:"version,omitempty"`
	Digest  digest.Digest   `json:"digest"`
	Refs    []string        `json:"refs"`
	Data    json.RawMessage `json:"data"`
}

// NewStore opens the profile directory without registering it for image garbage collection.
func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	if err := removeStaleTempFiles(root); err != nil {
		return nil, err
	}
	s := &Store{root: root, sizes: make(map[digest.Digest]uint64)}
	// Recover usage once at startup rather than on each metrics scrape.
	profiles, err := s.list(true)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		s.sizes[digest.Digest(profile.Digest)] = profile.Size
		s.size += profile.Size
	}
	s.publishUsage()
	return s, nil
}

func removeStaleTempFiles(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-staleTempFileAge)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) == ".json" || strings.Contains(name, ".json.corrupt-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
		log.L.WithField("path", filepath.Join(root, entry.Name())).Warn("removed stale security profile temporary file")
	}
	return nil
}

// ReferenceDigest requires a named, digest-pinned reference. Tags alone are mutable.
func ReferenceDigest(ref string) (digest.Digest, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", err
	}
	canonical, ok := named.(reference.Canonical)
	if !ok {
		return "", errors.New("security profile reference must be digest-pinned")
	}
	d := canonical.Digest()
	if err := d.Validate(); err != nil {
		return "", err
	}
	return d, nil
}

// Decode validates the untrusted document on both pull and use, including cached pulls.
func Decode(data []byte) (*specs.LinuxSeccomp, error) {
	if len(data) > MaxSize {
		return nil, fmt.Errorf("security profile exceeds %d bytes", MaxSize)
	}
	var profile specs.LinuxSeccomp
	if err := merger.UnmarshalStrict(data, &profile); err != nil {
		return nil, err
	}
	if err := merger.ValidateArtifact(&profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *Store) path(d digest.Digest) string {
	return filepath.Join(s.root, d.Algorithm().String()+"-"+d.Encoded()+".json")
}

func (s *Store) readRecordFile(path string) (*record, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("security profile: %w", errdefs.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, corruptRecord(errors.New("record is not a regular file"))
	}
	if info.Size() > MaxRecordSize {
		return nil, corruptRecord(fmt.Errorf("record exceeds %d bytes", MaxRecordSize))
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxRecordSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRecordSize {
		return nil, corruptRecord(fmt.Errorf("record exceeds %d bytes", MaxRecordSize))
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, corruptRecord(err)
	}
	if err := r.Digest.Validate(); err != nil {
		return nil, corruptRecord(err)
	}
	if r.Version != currentRecordVersion {
		return nil, corruptRecord(fmt.Errorf("unsupported record version %d", r.Version))
	}
	if len(r.Refs) == 0 {
		return nil, corruptRecord(errors.New("record has no references"))
	}
	if len(r.Refs) > MaxRefsPerProfile {
		return nil, corruptRecord(fmt.Errorf("reference limit (%d) exceeded", MaxRefsPerProfile))
	}
	seen := make(map[string]struct{}, len(r.Refs))
	for _, ref := range r.Refs {
		d, err := ReferenceDigest(ref)
		if err != nil || d != r.Digest {
			return nil, corruptRecord(errors.New("reference digest mismatch"))
		}
		if _, ok := seen[ref]; ok {
			return nil, corruptRecord(errors.New("duplicate reference"))
		}
		seen[ref] = struct{}{}
	}
	return &r, nil
}

func (s *Store) readRecord(d digest.Digest) (*record, error) {
	r, err := s.readRecordFile(s.path(d))
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("security profile %s: %w", d, errdefs.ErrNotFound)
		}
		return nil, err
	}
	if r.Digest != d {
		return nil, corruptRecord(errors.New("digest mismatch"))
	}
	return r, nil
}

// Usage returns a consistent in-memory snapshot without acquiring the store's
// operation lock, accessing disk, or decoding profiles.
func (s *Store) Usage() (count, size uint64) {
	if usage := s.usage.Load(); usage != nil {
		return usage.count, usage.size
	}
	return 0, 0
}

// publishUsage is called during initialization or with mu held.
func (s *Store) publishUsage() {
	s.usage.Store(&storeUsage{count: uint64(len(s.sizes)), size: s.size})
}

func (s *Store) write(r *record) error {
	// Compact without HTML escaping so storage cannot expand an accepted profile
	// past MaxSize when it is loaded after restart.
	var profileData bytes.Buffer
	if err := json.Compact(&profileData, r.Data); err != nil {
		return err
	}
	if profileData.Len() > MaxSize {
		return fmt.Errorf("security profile exceeds %d bytes", MaxSize)
	}
	r.Data = profileData.Bytes()
	r.Version = currentRecordVersion
	var recordData bytes.Buffer
	encoder := json.NewEncoder(&recordData)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(r); err != nil {
		return err
	}
	data := bytes.TrimSuffix(recordData.Bytes(), []byte("\n"))
	if len(data) > MaxRecordSize {
		return fmt.Errorf("security profile record exceeds %d bytes", MaxRecordSize)
	}
	f, err := atomicfile.New(s.path(r.Digest), 0600)
	if err != nil {
		return err
	}
	defer f.Cancel()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.size -= s.sizes[r.Digest]
	s.sizes[r.Digest] = uint64(profileData.Len())
	s.size += uint64(profileData.Len())
	s.publishUsage()
	return nil
}

// Get looks up the pod's original reference, regardless of which mirror served it.
func (s *Store) Get(ref string) (*specs.LinuxSeccomp, error) {
	d, err := ReferenceDigest(ref)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	r, err := s.readRecord(d)
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if !slices.Contains(r.Refs, ref) {
		return nil, fmt.Errorf("security profile reference %q: %w", ref, errdefs.ErrNotFound)
	}
	return Decode(r.Data)
}

// Remember reports a cache hit only for a reference that was previously verified and stored.
func (s *Store) Remember(ref string) (bool, error) {
	d, err := ReferenceDigest(ref)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	r, err := s.readRecord(d)
	s.mu.RUnlock()
	if err == nil {
		if _, decodeErr := Decode(r.Data); decodeErr != nil {
			err = corruptRecord(decodeErr)
		}
	}
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	if errors.Is(err, errCorruptRecord) {
		s.mu.Lock()
		r, err = s.readRecord(d)
		if err == nil {
			if _, decodeErr := Decode(r.Data); decodeErr != nil {
				err = corruptRecord(decodeErr)
			}
		}
		if errors.Is(err, errCorruptRecord) {
			quarantineErr := s.quarantine(s.path(d))
			s.mu.Unlock()
			if quarantineErr != nil && !os.IsNotExist(quarantineErr) {
				return false, quarantineErr
			}
			log.L.WithError(err).WithField("digest", d.String()).Warn("quarantined corrupt security profile; allowing pull to retry")
			return false, nil
		}
		s.mu.Unlock()
		if errdefs.IsNotFound(err) {
			return false, nil
		}
	}
	if err != nil {
		return false, err
	}
	if !slices.Contains(r.Refs, ref) {
		return false, nil
	}
	return true, nil
}

// Put publishes a fully downloaded and validated artifact atomically.
func (s *Store) Put(ref string, data []byte) error {
	d, err := ReferenceDigest(ref)
	if err != nil {
		return err
	}
	if _, err := Decode(data); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.readRecord(d)
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if r == nil {
		r = &record{Version: currentRecordVersion, Digest: d, Data: slices.Clone(data)}
	} else if _, err := Decode(r.Data); err != nil {
		return err
	}
	if !slices.Contains(r.Refs, ref) {
		if len(r.Refs) >= MaxRefsPerProfile {
			return fmt.Errorf("security profile reference limit (%d) exceeded: %w", MaxRefsPerProfile, errdefs.ErrResourceExhausted)
		}
		r.Refs = append(r.Refs, ref)
		slices.Sort(r.Refs)
	}
	return s.write(r)
}

// List reports stored profiles in digest order.
func (s *Store) List() ([]*runtime.SecurityProfileInfo, error) {
	return s.list(false)
}

func (s *Store) list(quarantineInvalid bool) ([]*runtime.SecurityProfileInfo, error) {
	s.mu.RLock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		s.mu.RUnlock()
		return nil, err
	}
	type result struct {
		path string
		r    *record
		err  error
	}
	results := make([]result, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		r, readErr := s.readRecordFile(path)
		if readErr == nil && filepath.Base(s.path(r.Digest)) != entry.Name() {
			readErr = corruptRecord(errors.New("filename does not match digest"))
		}
		results = append(results, result{path: path, r: r, err: readErr})
	}
	s.mu.RUnlock()

	profiles := make([]*runtime.SecurityProfileInfo, 0, len(entries))
	for _, result := range results {
		r, recordErr := result.r, result.err
		if recordErr == nil {
			_, recordErr = Decode(r.Data)
			if recordErr != nil {
				recordErr = corruptRecord(recordErr)
			}
		}
		if recordErr != nil {
			if !errors.Is(recordErr, errCorruptRecord) {
				return nil, recordErr
			}
			if !quarantineInvalid {
				log.L.WithError(recordErr).WithField("path", result.path).Warn("skipping invalid security profile record")
				continue
			}
			s.mu.Lock()
			quarantineErr := s.quarantine(result.path)
			s.mu.Unlock()
			if quarantineErr != nil && !os.IsNotExist(quarantineErr) {
				return nil, fmt.Errorf("quarantine invalid security profile record %q: %w", result.path, quarantineErr)
			}
			log.L.WithError(recordErr).WithField("path", result.path).Warn("quarantined invalid security profile record")
			continue
		}
		profiles = append(profiles, &runtime.SecurityProfileInfo{Digest: r.Digest.String(), Refs: slices.Clone(r.Refs), Size: uint64(len(r.Data))})
	}
	slices.SortFunc(profiles, func(a, b *runtime.SecurityProfileInfo) int { return cmp.Compare(a.Digest, b.Digest) })
	return profiles, nil
}

func (s *Store) quarantine(path string) error {
	quarantinePath := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
	return os.Rename(path, quarantinePath)
}

// Remove deletes a profile by manifest digest; an absent profile succeeds.
func (s *Store) Remove(value string) error {
	d, err := digest.Parse(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err = os.Remove(s.path(d))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s.size -= s.sizes[d]
	delete(s.sizes, d)
	s.publishUsage()
	return nil
}
