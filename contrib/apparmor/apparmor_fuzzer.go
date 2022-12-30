//go:build gofuzz

// Copyright 2022 ADA Logics Ltd
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package apparmor

import (
	"os"
)

func FuzzLoadDefaultProfile(data []byte) int {
	f, err := os.Create("fuzz_file")
	if err != nil {
		return 0
	}
	defer os.Remove("fuzz_file")
	_, err = f.Write(data)
	if err != nil {
		return 0
	}
	_ = LoadDefaultProfile("fuzz_file")
	return 1
}
