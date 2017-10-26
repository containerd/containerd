#!/bin/bash
# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

for d in $(find . -type d -a \( -iwholename './pkg*' -o -iwholename './cmd*' \) -not -iwholename './pkg/api*'); do
	echo for directory ${d} ...
	gometalinter \
		 --exclude='error return value not checked.*(Close|Log|Print).*\(errcheck\)$' \
		 --exclude='.*_test\.go:.*error return value not checked.*\(errcheck\)$' \
		 --exclude='duplicate of.*_test.go.*\(dupl\)$' \
		 --exclude='.*/mock_.*\.go:.*\(golint\)$' \
		 --exclude='declaration of "err" shadows declaration.*\(vetshadow\)$' \
		 --exclude='struct of size .* could be .* \(maligned\)$' \
		 --disable=aligncheck \
		 --disable=gotype \
		 --disable=gas \
		 --cyclo-over=60 \
		 --dupl-threshold=100 \
		 --tests \
		 --deadline=300s "${d}"
done
