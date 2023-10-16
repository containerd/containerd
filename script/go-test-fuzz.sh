#!/usr/bin/env bash

#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

# Running Go 1.18's fuzzing for 30 seconds each. While this would be too
# short to actually find issues, we want to make sure that these fuzzing
# tests are not fundamentally broken.

set -euo pipefail

fuzztime=30s
pkgs=$(git grep 'func Fuzz.*testing\.F' | grep -o '.*\/' | sort | uniq)

for pkg in $pkgs
do
    go test -fuzz=. ./$pkg -fuzztime=$fuzztime
done
