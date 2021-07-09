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

compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzFiltersParse fuzz_filters_parse
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzPlatformsParse fuzz_platforms_parse

mv $SRC/containerd/contrib/fuzz/docker_fuzzer.go $SRC/containerd/remotes/docker/
compile_go_fuzzer github.com/containerd/containerd/remotes/docker FuzzFetcher fuzz_fetcher
mv $SRC/containerd/remotes/docker/docker_fuzzer.go $SRC/containerd/contrib/fuzz/
