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

package ioutil

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCloseInformer(t *testing.T) {
	original := &writeCloser{}
	wci, close := NewWriteCloseInformer(original)
	data := "test"

	n, err := wci.Write([]byte(data))
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, original.buf.String())
	assert.NoError(t, err)

	select {
	case <-close:
		assert.Fail(t, "write closer closed")
	default:
	}

	wci.Close()
	assert.True(t, original.closed)

	select {
	case <-close:
	default:
		assert.Fail(t, "write closer not closed")
	}
}

func TestSerialWriteCloser(t *testing.T) {
	const (
		// Test 10 times to make sure it always pass.
		testCount = 10

		goroutine = 10
		dataLen   = 100000
	)
	for n := 0; n < testCount; n++ {
		testData := make([][]byte, goroutine)
		for i := 0; i < goroutine; i++ {
			testData[i] = []byte(repeatNumber(i, dataLen) + "\n")
		}

		f, err := os.Create(filepath.Join(t.TempDir(), "serial-write-closer"))
		require.NoError(t, err)
		t.Cleanup(func() {
			f.Close()
		})
		wc := NewSerialWriteCloser(f)
		defer wc.Close()

		// Write data in parallel
		var wg sync.WaitGroup
		wg.Add(goroutine)
		for i := 0; i < goroutine; i++ {
			go func(id int) {
				n, err := wc.Write(testData[id])
				assert.NoError(t, err)
				assert.Equal(t, dataLen+1, n)
				wg.Done()
			}(i)
		}
		wg.Wait()
		wc.Close()

		// Check test result
		content, err := os.ReadFile(f.Name())
		require.NoError(t, err)
		resultData := strings.Split(strings.TrimSpace(string(content)), "\n")
		require.Len(t, resultData, goroutine)
		sort.Strings(resultData)
		for i := 0; i < goroutine; i++ {
			expected := repeatNumber(i, dataLen)
			assert.Equal(t, expected, resultData[i])
		}
	}
}

func repeatNumber(num, count int) string {
	return strings.Repeat(strconv.Itoa(num), count)
}
