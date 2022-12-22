#!/usr/bin/env python3

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

#
# verifies if the require and replace directives for two go.mod files are in sync
#
import subprocess
import sys
import json

def read_go_mod(cwd='.'):
  out = subprocess.run(['go', 'mod', 'edit', '-json'], cwd=cwd, capture_output=True)
  file = json.loads(out.stdout)
  requires = {}
  replaces = {}
  for mod in file['Require']:
    requires[mod['Path']] = mod['Version']
  for mod in file['Replace']:
    replaces[mod['Old']['Path']] = mod['New']['Path'] + (mod['New'].get('Version') or '')
  return requires, replaces

mod_dir = sys.argv[1]
requires1, replaces1 = read_go_mod()
requires2, replaces2 = read_go_mod(mod_dir)
errors = 0

# iterate through the second go.mod's require section and ensure that all items
# have the same values in the root go.mod require section
for k in requires2:
  if (k in requires1) and (requires1[k] != requires2[k]):
    print(f"{k} has different values in the go.mod files require section:" \
          f"{requires1[k]} in root go.mod {requires2[k]} in {mod_dir}/go.mod")
    errors += 1

# iterate through the second go.mod's replace section and ensure that all items
# have the same values in the root go.mod's replace section. Except for the
# containerd/containerd which we know will be different
for k in replaces2:
  if (k in replaces1) and (replaces1[k] != replaces2[k]):
    print(f"{k} has different values in the go.mod files replace section:" \
          f"{replaces1[k]} in root go.mod {replaces2[k]} in {mod_dir}/go.mod")
    errors += 1

# iterate through the root go.mod's replace section and ensure that all the
# same items are present in the second go.mod's replace section and nothing is missing
for k in replaces1:
  if not replaces2[k]:
    print(f"{k} has an entry in root go.mod replace section, but is missing from" \
          f" replace section in {mod_dir}/go.mod")
    errors += 1

sys.exit(errors != 0)