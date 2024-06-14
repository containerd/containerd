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

FROM ubuntu
RUN mkdir -p /test_dir && \
    chown -R nobody:nogroup /test_dir
# NOTE: The volume is required by ../../container_volume_linux_test.go#TestRunContainerWithVolatileOption.
VOLUME /test_dir
