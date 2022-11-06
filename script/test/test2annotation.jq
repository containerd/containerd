#!/usr/bin/env -S jq -s -r -f

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

# Takes the raw test2json as input and groups each item by package name + test name
#
# Example
# Input:
#   {...}
#   {...}
#   {...}
# Output:
#  [[{...},{...},{...}]]
def group_tests: reduce (.[] | select(.Test != null)) as $item (
    {}; .["\($item.Package).\($item.Test)"] += [$item]
);

# Filter a group of tests by action.
# The way this works is for a group of tests,
# if none of the tests have an action matching the provided value, the group is
# dropped.
def filter_group_action(a): map(select(.[].Action == a));

# Filter a group of tests to just the tests that failed
def only_failed: filter_group_action("fail");

def merge_strings(s1; s2): if s1 == null then s2 else "\(s1)\(s2)" end;

# Matches an output string to find the test file and line number.
# The actual string looks something like:
#  "test_foo.go:12: some failure message\n"
# There may be arbitrary whitespace at the beginning of the string.
def capture_fail_details: capture("^\\s*(?<file>\\w+_test\\.go):(?<line>\\d+): "; "x");

# Filter out all the undesirable test line outputs
def filter_test_item:
    select(.Output != null)
    | select(.Output | test("^\\s*=== RUN")| not)
    | select(.Output | test("^\\s*=== PAUSE")| not)
    | select(.Output | test("^\\s*=== CONT")| not)
    | select(.Output | test("^\\s*--- FAIL:")| not)
;

# Take a list of test groups and make a map of tests keyed on the package name +
# test name with all the output concatenated.
#
# This uses the last test log message to get the the file/line number for the test (as .Details)
def merge_output: reduce (.[][] | filter_test_item) as $item (
    {};  (
        "\($item.Package).\($item.Test)" as $key
        | merge_strings(.[$key].Output; $item.Output) as $merged
        | .[$key] += {
            Output: $merged | sub("^\\s*"; ""; "x"),
            Test: $item.Test,
            Package: $item.Package,
            Details: (($item.Output | capture_fail_details) // .[$key].Details),  # "//" is an operator that returns the first true/non-null value
        }
    ) // .
);

# Used as the "title" field for error annotations
def err_title: "Failed: \(.Package).\(.Test)";

# Split the containerd root package path to get the directory name that a test belongs to.
def file_dir: .Package | split("github.com/containerd/containerd/")[-1];

def err_file: if .Details != null and .Details.file != null then "\(file_dir)/\(.Details.file)" else "" end;

# Some cases there may not be any extra details because they got filtered out.
# This can happen, for instance, if the failure log was create from a file that is not a `_test.go` file.
# In this case we'd still want the annotation but we just can't get the file/line number (without allowing non-test files to be the source of the failure).
def details_to_string: if .Details != null  and .Details.file != null then ",file=\(err_file),line=\(.Details.line)" else "" end;

# Replace all occurrences of "\n" with the url-encoded version
# This allows multi-line strings to work with github annotations.
# https://github.com/actions/toolkit/issues/193#issuecomment-605394935
def encode_output: gsub("\n"; "%0A");

# Creates github actions error annotations for each input
# It is expected that the input is the output of merge_output.
def to_error: reduce .[] as $item (
    ""; . += "::error title=\($item | err_title)\($item | details_to_string)::\($item.Output | encode_output)\n"
);

group_tests | only_failed | merge_output | to_error