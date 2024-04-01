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

ARG BASE

FROM $BASE
RUN sh -c "mkdir /test_dir; echo test_content > /test_dir/test_file"
RUN sh -c "mkdir -p /C:/weird_test_dir; echo test_content > /C:/weird_test_dir/weird_test_file"
RUN sh -c "mkdir /:colon_prefixed; echo test_content > /:colon_prefixed/colon_prefixed_file"

# We add a Windows style path here to ensure that for Linux, we don't mangle it in any way.
# The C: drive letter gets special treatment on Windows and is stripped by WithVolumes(). The remaining
# path is used to join with the volume mount point on the host. Colons are not allowed
# in Windows paths except when delimiting the drive letter, but on Linux such paths are
# perfectly valid and we must ensure we don't mutate them by accident.
#
# The C:/weird_test_dir volume is considered relative to "/" and will result in a mount point
# situated at /C:/weird_test_dir.
#
# The "/:colon_prefixed" volume is meant to test that we don't ignore copying existing contents
# to a volume containing a colon as the second character in the path on Linux, considering that
# on Windows, we skip this operation for any non-C volume defined here.
VOLUME ["/test_dir", "C:/weird_test_dir", "/:colon_prefixed"]
