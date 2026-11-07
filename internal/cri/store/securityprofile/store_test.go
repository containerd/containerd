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

package securityprofile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	merger "sigs.k8s.io/security-profiles-merger/seccomp"

	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestStoreLifecycle(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(root)
	require.NoError(t, err)
	d := digest.FromString("manifest")
	ref := "registry.example/profiles/test@" + d.String()
	alias := "mirror.example/profiles/test@" + d.String()
	cached, err := s.Remember(ref)
	require.NoError(t, err)
	require.False(t, cached)
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	require.NoError(t, s.Put(ref, data))
	cached, err = s.Remember(alias)
	require.NoError(t, err)
	require.False(t, cached)
	_, err = s.Get(alias)
	require.True(t, errdefs.IsNotFound(err))
	require.NoError(t, s.Put(alias, data))
	// Reopen: verified profile references survive daemon restart.
	s, err = NewStore(root)
	require.NoError(t, err)
	profile, err := s.Get(alias)
	require.NoError(t, err)
	require.Equal(t, "SCMP_ACT_ALLOW", string(profile.DefaultAction))
	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.ElementsMatch(t, []string{ref, alias}, list[0].Refs)
	require.Equal(t, uint64(len(data)), list[0].Size)
	require.NoError(t, s.Remove(d.String()))
	require.NoError(t, s.Remove(d.String()))
	_, err = s.Get(ref)
	require.True(t, errdefs.IsNotFound(err))
}

func TestConcurrentReferences(t *testing.T) {
	s, err := NewStore(t.TempDir())
	require.NoError(t, err)
	d := digest.FromString("manifest")
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Put(fmt.Sprintf("registry.example/p%d@%s", i, d), data)
			if err != nil {
				t.Error(err)
			}
			count, size := s.Usage()
			if count != 1 || size != uint64(len(data)) {
				t.Errorf("unexpected usage: count=%d size=%d", count, size)
			}
		}()
	}
	wg.Wait()
	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Len(t, list[0].Refs, 20)
}

func TestRejectArtifact(t *testing.T) {
	for name, data := range map[string]string{
		"repeated":          `{"defaultAction":"SCMP_ACT_ALLOW","defaultAction":"SCMP_ACT_ERRNO"}`,
		"unknown":           `{"defaultAction":"SCMP_ACT_ALLOW","typo":1}`,
		"case":              `{"DefaultAction":"SCMP_ACT_ALLOW"}`,
		"trailing":          `{"defaultAction":"SCMP_ACT_ALLOW"} {}`,
		"utf8":              "{\"defaultAction\":\"\xff\"}",
		"conflicting rules": `{"defaultAction":"SCMP_ACT_KILL_PROCESS","syscalls":[{"names":["read"],"action":"SCMP_ACT_ALLOW"},{"names":["read"],"action":"SCMP_ACT_ERRNO","args":[{"index":0,"value":1,"op":"SCMP_CMP_EQ"}]}]}`,
		"notify":            `{"defaultAction":"SCMP_ACT_NOTIFY"}`,
		"listener":          `{"defaultAction":"SCMP_ACT_ALLOW","listenerPath":"/run/listener"}`,
		"metadata":          `{"defaultAction":"SCMP_ACT_ALLOW","listenerMetadata":"test"}`,
		"wait":              `{"defaultAction":"SCMP_ACT_ALLOW","flags":["SECCOMP_FILTER_FLAG_WAIT_KILLABLE_RECV"]}`,
		"errno":             `{"defaultAction":"SCMP_ACT_ERRNO","defaultErrnoRet":4096}`,
		"size":              strings.Repeat(" ", MaxSize+1),
		"tar":               "not a JSON blob",
	} {
		t.Run(name, func(t *testing.T) { _, err := Decode([]byte(data)); require.Error(t, err) })
	}
	for _, ref := range []string{"registry.example/profile:latest", "", "../profile"} {
		_, err := ReferenceDigest(ref)
		require.Error(t, err)
	}
}

func TestArtifactValidationLimits(t *testing.T) {
	tests := map[string]*specs.LinuxSeccomp{}
	entries := &specs.LinuxSeccomp{DefaultAction: specs.ActErrno}
	for i := 0; i <= merger.MaxArtifactEntriesPerSyscall; i++ {
		entries.Syscalls = append(entries.Syscalls, specs.LinuxSyscall{Names: []string{"read"}, Action: specs.ActAllow})
	}
	tests["entries per syscall"] = entries
	names := &specs.LinuxSeccomp{DefaultAction: specs.ActErrno, Syscalls: []specs.LinuxSyscall{{Action: specs.ActAllow}}}
	for i := 0; i <= merger.MaxArtifactNamesPerEntry; i++ {
		names.Syscalls[0].Names = append(names.Syscalls[0].Names, fmt.Sprintf("syscall%d", i))
	}
	tests["names per entry"] = names
	clauses := &specs.LinuxSeccomp{DefaultAction: specs.ActErrno, Syscalls: []specs.LinuxSyscall{{Names: []string{"read"}, Action: specs.ActAllow}}}
	for i := 0; i <= merger.MaxArtifactClausesPerSyscall; i++ {
		clauses.Syscalls[0].Args = append(clauses.Syscalls[0].Args, specs.LinuxSeccompArg{Index: 0, Value: uint64(i), Op: specs.OpEqualTo})
	}
	tests["rules per syscall"] = clauses
	total := &specs.LinuxSeccomp{DefaultAction: specs.ActErrno}
	for i := 0; i <= merger.MaxArtifactClauses/merger.MaxArtifactNamesPerEntry; i++ {
		entry := specs.LinuxSyscall{Action: specs.ActAllow}
		for j := 0; j < merger.MaxArtifactNamesPerEntry; j++ {
			entry.Names = append(entry.Names, fmt.Sprintf("syscall%d", i*merger.MaxArtifactNamesPerEntry+j))
		}
		total.Syscalls = append(total.Syscalls, entry)
	}
	tests["rules per profile"] = total
	tests["32 bit argument"] = &specs.LinuxSeccomp{DefaultAction: specs.ActErrno, Architectures: []specs.Arch{specs.ArchX86}, Syscalls: []specs.LinuxSyscall{{Names: []string{"read"}, Action: specs.ActAllow, Args: []specs.LinuxSeccompArg{{Index: 0, Value: 1 << 32, Op: specs.OpEqualTo}}}}}
	expected := map[string]error{
		"entries per syscall": merger.ErrTooManyEntries,
		"names per entry":     merger.ErrTooManyNames,
		"rules per syscall":   merger.ErrTooManyClauses,
		"rules per profile":   merger.ErrTooManyProfileClauses,
		"32 bit argument":     merger.ErrValueTooWide,
	}
	for name, profile := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(profile)
			require.NoError(t, err)
			require.Less(t, len(data), MaxSize)
			_, err = Decode(data)
			require.ErrorIs(t, err, expected[name])
		})
	}
}

func requireUsage(t *testing.T, s *Store, count, size uint64) {
	t.Helper()
	actualCount, actualSize := s.Usage()
	require.Equal(t, count, actualCount)
	require.Equal(t, size, actualSize)
}

func TestUsageLifecycle(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(root)
	require.NoError(t, err)
	requireUsage(t, s, 0, 0)
	d := digest.FromString("first manifest")
	ref := "registry.example/profile@" + d.String()
	data := []byte("  { \"defaultAction\": \"SCMP_ACT_ALLOW\" } ")
	compact, err := json.Marshal(json.RawMessage(data))
	require.NoError(t, err)
	size := uint64(len(compact))
	require.NoError(t, s.Put(ref, data))
	requireUsage(t, s, 1, size)
	require.NoError(t, s.Put(ref, data))
	requireUsage(t, s, 1, size)
	alias := "mirror.example/profile@" + d.String()
	cached, err := s.Remember(alias)
	require.NoError(t, err)
	require.False(t, cached)
	require.NoError(t, s.Put(alias, data))
	requireUsage(t, s, 1, size)
	require.NoError(t, s.Put("other.example/profile@"+d.String(), data))
	requireUsage(t, s, 1, size)
	second := digest.FromString("second manifest")
	require.NoError(t, s.Put("registry.example/second@"+second.String(), data))
	requireUsage(t, s, 2, 2*size)
	s, err = NewStore(root)
	require.NoError(t, err)
	requireUsage(t, s, 2, 2*size)
	require.NoError(t, s.Remove(d.String()))
	requireUsage(t, s, 1, size)
	require.NoError(t, s.Remove(d.String()))
	requireUsage(t, s, 1, size)
	require.NoError(t, s.Remove(second.String()))
	requireUsage(t, s, 0, 0)
}

func TestReferenceLimit(t *testing.T) {
	s, err := NewStore(t.TempDir())
	require.NoError(t, err)
	d := digest.FromString("reference-limit")
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	for i := 0; i < MaxRefsPerProfile; i++ {
		ref := fmt.Sprintf("registry.example/p%d@%s", i, d)
		require.NoError(t, s.Put(ref, data))
	}
	err = s.Put(fmt.Sprintf("registry.example/overflow@%s", d), data)
	require.True(t, errdefs.IsResourceExhausted(err))
	profiles, err := s.List()
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.Len(t, profiles[0].Refs, MaxRefsPerProfile)
}

func TestStorePreservesLargeHTMLSensitiveProfile(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(root)
	require.NoError(t, err)
	d := digest.FromString("html-sensitive-profile")
	name := strings.Repeat("&<>", 60_000)
	data := fmt.Appendf(nil, `{"defaultAction":"SCMP_ACT_ALLOW","syscalls":[{"names":[%q],"action":"SCMP_ACT_ERRNO"}]}`, name)
	require.Less(t, len(data), MaxSize)
	ref := "registry.example/profile@" + d.String()
	require.NoError(t, s.Put(ref, data))
	info, err := os.Stat(s.path(d))
	require.NoError(t, err)
	require.Less(t, info.Size(), int64(MaxSize))

	s, err = NewStore(root)
	require.NoError(t, err)
	_, err = s.Get(ref)
	require.NoError(t, err)
	profiles, err := s.List()
	require.NoError(t, err)
	require.Len(t, profiles, 1)
}

func TestStartupQuarantinesCorruptAndLegacyRecords(t *testing.T) {
	root := t.TempDir()
	digests := []digest.Digest{digest.FromString("corrupt"), digest.FromString("legacy")}
	corruptPath := filepath.Join(root, digests[0].Algorithm().String()+"-"+digests[0].Encoded()+".json")
	legacyPath := filepath.Join(root, digests[1].Algorithm().String()+"-"+digests[1].Encoded()+".json")
	legacy := fmt.Sprintf(`{"digest":%q,"refs":[%q],"data":{"defaultAction":"SCMP_ACT_ALLOW"}}`, digests[1], "registry.example/profile@"+digests[1].String())
	require.NoError(t, os.WriteFile(corruptPath, []byte("{"), 0600))
	require.NoError(t, os.WriteFile(legacyPath, []byte(legacy), 0600))

	s, err := NewStore(root)
	require.NoError(t, err)
	profiles, err := s.List()
	require.NoError(t, err)
	require.Empty(t, profiles)
	_, err = s.Get("registry.example/profile@" + digests[1].String())
	require.True(t, errdefs.IsNotFound(err))
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	for _, entry := range entries {
		require.NotEqual(t, ".json", filepath.Ext(entry.Name()))
	}
}

func TestStartupRemovesOnlyStaleAtomicTempFiles(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "abandoned-write")
	recent := filepath.Join(root, "recent-write")
	unrelated := filepath.Join(root, "old-shared-file")
	quarantined := filepath.Join(root, "sha256-deadbeef.json.corrupt-123")
	require.NoError(t, os.WriteFile(stale, []byte("partial"), 0600))
	require.NoError(t, os.WriteFile(recent, []byte("partial"), 0600))
	require.NoError(t, os.WriteFile(unrelated, []byte("keep"), 0644))
	require.NoError(t, os.WriteFile(quarantined, []byte("preserve"), 0600))
	old := time.Now().Add(-staleTempFileAge - time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))
	require.NoError(t, os.Chtimes(unrelated, old, old))
	require.NoError(t, os.Chtimes(quarantined, old, old))

	_, err := NewStore(root)
	require.NoError(t, err)
	_, err = os.Stat(stale)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(recent)
	require.NoError(t, err)
	_, err = os.Stat(unrelated)
	require.NoError(t, err)
	_, err = os.Stat(quarantined)
	require.NoError(t, err)
}

func TestRememberQuarantinesInvalidCachedProfile(t *testing.T) {
	s, err := NewStore(t.TempDir())
	require.NoError(t, err)
	d := digest.FromString("invalid-cached-profile")
	ref := "registry.example/profile@" + d.String()
	good := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	require.NoError(t, s.Put(ref, good))
	badRecord, err := json.Marshal(record{
		Version: currentRecordVersion,
		Digest:  d,
		Refs:    []string{ref},
		Data:    json.RawMessage(`{"defaultAction":"NOT_AN_ACTION"}`),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(s.path(d), badRecord, 0600))
	profiles, err := s.List()
	require.NoError(t, err)
	require.Empty(t, profiles)

	cached, err := s.Remember(ref)
	require.NoError(t, err)
	require.False(t, cached)
	_, err = s.Get(ref)
	require.True(t, errdefs.IsNotFound(err))
	require.NoError(t, s.Put(ref, good))
	_, err = s.Get(ref)
	require.NoError(t, err)
}

func TestUsageIgnoresFailedMutations(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(root)
	require.NoError(t, err)
	d := digest.FromString("manifest")
	ref := "registry.example/profile@" + d.String()
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	require.NoError(t, s.Put(ref, data))
	size := uint64(len(data))
	require.Error(t, s.Put("registry.example/invalid@"+digest.FromString("invalid").String(), []byte("invalid")))
	requireUsage(t, s, 1, size)
	// Missing directories make publication fail without depending on file modes
	// (the suite can run as root).
	s.root = filepath.Join(root, "missing")
	require.Error(t, s.Put("registry.example/other@"+digest.FromString("other").String(), data))
	requireUsage(t, s, 1, size)
	s.root = root
	path := s.path(d)
	require.NoError(t, os.Rename(path, path+".backup"))
	require.NoError(t, os.Mkdir(path, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "block"), nil, 0600))
	require.Error(t, s.Remove(d.String()))
	requireUsage(t, s, 1, size)
	require.NoError(t, os.Remove(filepath.Join(path, "block")))
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Rename(path+".backup", path))
	require.NoError(t, s.Remove(d.String()))
	requireUsage(t, s, 0, 0)
}

func TestUsageDoesNotScanOrWaitForOperations(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	s, err := NewStore(root)
	require.NoError(t, err)
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	require.NoError(t, s.Put("registry.example/profile@"+digest.FromString("manifest").String(), data))
	// A metrics read must work even when the directory cannot be scanned and an
	// operation holds the store mutex.
	require.NoError(t, os.Rename(root, root+".moved"))
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(chan storeUsage, 1)
	go func() { count, size := s.Usage(); result <- storeUsage{count: count, size: size} }()
	select {
	case usage := <-result:
		require.Equal(t, uint64(1), usage.count)
		require.Equal(t, uint64(len(data)), usage.size)
	case <-time.After(5 * time.Second):
		t.Fatal("usage read blocked on the store operation lock")
	}
}
