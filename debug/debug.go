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

package debug

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Smaps prints the smaps to a file
func Smaps(note, file string) error {
	smaps, err := getMaps(os.Getpid())
	if err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "%s: rss %d\n", note, smaps["rss"])
	fmt.Fprintf(f, "%s: pss %d\n", note, smaps["pss"])
	return nil
}

func getMaps(pid int) (map[string]int, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/smaps", pid))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var (
		smaps = make(map[string]int)
		s     = bufio.NewScanner(f)
	)
	for s.Scan() {
		var (
			fields = strings.Fields(s.Text())
			name   = fields[0]
		)
		name = strings.TrimSuffix(strings.ToLower(name), ":")
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		smaps[name] += n
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return smaps, nil
}
