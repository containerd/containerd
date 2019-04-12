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

set -o xtrace
set -o errexit
set -o nounset
set -o pipefail

# CONTAINERD_HOME is the directory for containerd.
CONTAINERD_HOME="/home/containerd"
cd "${CONTAINERD_HOME}"
# KUBE_HOME is the directory for kubernetes.
KUBE_HOME="/home/kubernetes"

# fetch_metadata fetches metadata from GCE metadata server.
# Var set:
# 1. Metadata key: key of the metadata.
fetch_metadata() {
  local -r key=$1
  local -r attributes="http://metadata.google.internal/computeMetadata/v1/instance/attributes"
  if curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" "${attributes}/" | \
    grep -q "^${key}$"; then
    curl --fail --retry 5 --retry-delay 3 --silent --show-error -H "X-Google-Metadata-Request: True" \
      "${attributes}/${key}"
  fi
}

# fetch_env fetches environment variables from GCE metadata server
# and generate a env file under ${CONTAINERD_HOME}. It assumes that
# the environment variables in metadata are in yaml format.
fetch_env() {
  local -r env_file_name=$1
  (
    umask 077;
    local -r tmp_env_file="/tmp/${env_file_name}.yaml"
    tmp_env_content=$(fetch_metadata "${env_file_name}")
    if [ -z "${tmp_env_content}" ]; then
      echo "No environment variable is specified in ${env_file_name}"
      return
    fi
    echo "${tmp_env_content}" > "${tmp_env_file}"
    # Convert the yaml format file into a shell-style file.
    eval $(python -c '''
import pipes,sys,yaml
for k,v in yaml.load(sys.stdin).iteritems():
  print("readonly {var}={value}".format(var = k, value = pipes.quote(str(v))))
''' < "${tmp_env_file}" > "${CONTAINERD_HOME}/${env_file_name}")
    rm -f "${tmp_env_file}"
  )
}

# is_preloaded checks whether a package has been preloaded in the image.
is_preloaded() {
  local -r tar=$1
  local -r sha1=$2
  grep -qs "${tar},${sha1}" "${KUBE_HOME}/preload_info"
}

# KUBE_ENV_METADATA is the metadata key for kubernetes envs.
KUBE_ENV_METADATA="kube-env"
fetch_env ${KUBE_ENV_METADATA}
if [ -f "${CONTAINERD_HOME}/${KUBE_ENV_METADATA}" ]; then
  source "${CONTAINERD_HOME}/${KUBE_ENV_METADATA}"
fi

# CONTAINERD_ENV_METADATA is the metadata key for containerd envs.
CONTAINERD_ENV_METADATA="containerd-env"
fetch_env ${CONTAINERD_ENV_METADATA}
if [ -f "${CONTAINERD_HOME}/${CONTAINERD_ENV_METADATA}" ]; then
  source "${CONTAINERD_HOME}/${CONTAINERD_ENV_METADATA}"
fi

# CONTAINERD_PKG_PREFIX is the prefix of the cri-containerd tarball name.
# By default use the release tarball with cni built in.
pkg_prefix=${CONTAINERD_PKG_PREFIX:-"cri-containerd-cni"}
# Behave differently for test and production.
if [ "${CONTAINERD_TEST:-"false"}"  != "true" ]; then
    # CONTAINERD_DEPLOY_PATH is the gcs path where cri-containerd tarball is stored.
  deploy_path=${CONTAINERD_DEPLOY_PATH:-"cri-containerd-release"}
  # CONTAINERD_VERSION is the cri-containerd version to use.
  version=${CONTAINERD_VERSION:-""}
else
  deploy_path=${CONTAINERD_DEPLOY_PATH:-"cri-containerd-staging"}

  # PULL_REFS_METADATA is the metadata key of PULL_REFS from prow.
  PULL_REFS_METADATA="PULL_REFS"
  pull_refs=$(fetch_metadata "${PULL_REFS_METADATA}")
  if [ ! -z "${pull_refs}" ]; then
    deploy_dir=$(echo "${pull_refs}" | sha1sum | awk '{print $1}')
    deploy_path="${deploy_path}/${deploy_dir}"
  fi

  # TODO(random-liu): Put version into the metadata instead of
  # deciding it in cloud init. This may cause issue to reboot test.
  version=$(curl -f --ipv4 --retry 6 --retry-delay 3 --silent --show-error \
    https://storage.googleapis.com/${deploy_path}/latest)
fi

TARBALL_GCS_NAME="${pkg_prefix}-${version}.linux-amd64.tar.gz"
# TARBALL_GCS_PATH is the path to download cri-containerd tarball for node e2e.
TARBALL_GCS_PATH="https://storage.googleapis.com/${deploy_path}/${TARBALL_GCS_NAME}"
# TARBALL is the name of the tarball after being downloaded.
TARBALL="cri-containerd.tar.gz"
# CONTAINERD_TAR_SHA1 is the sha1sum of containerd tarball.
tar_sha1="${CONTAINERD_TAR_SHA1:-""}"

if [ -z "${version}" ]; then
  # Try using preloaded containerd if version is not specified.
  tarball_gcs_pattern="${pkg_prefix}-.*.linux-amd64.tar.gz"
  if is_preloaded "${tarball_gcs_pattern}" "${tar_sha1}"; then
    echo "CONTAINERD_VERSION is not set, use preloaded containerd"
  else
    echo "CONTAINERD_VERSION is not set, and containerd is not preloaded"
    exit 1
  fi
else
  if is_preloaded "${TARBALL_GCS_NAME}" "${tar_sha1}"; then
    echo "${TARBALL_GCS_NAME} is preloaded"
  else
    # Download and untar the release tar ball.
    curl -f --ipv4 -Lo "${TARBALL}" --connect-timeout 20 --max-time 300 --retry 6 --retry-delay 10 "${TARBALL_GCS_PATH}"
    tar xvf "${TARBALL}"
    rm -f "${TARBALL}"
  fi
fi

# Remove crictl shipped with containerd, use crictl installed
# by kube-up.sh.
rm -f "${CONTAINERD_HOME}/usr/local/bin/crictl"
rm -f "${CONTAINERD_HOME}/etc/crictl.yaml"

# Generate containerd config
config_path="${CONTAINERD_CONFIG_PATH:-"/etc/containerd/config.toml"}"
mkdir -p $(dirname ${config_path})
cni_bin_dir="${CONTAINERD_HOME}/opt/cni/bin"
cni_template_path="${CONTAINERD_HOME}/opt/containerd/cluster/gce/cni.template"
if [ "${KUBERNETES_MASTER:-}" != "true" ]; then
  if [ "${NETWORK_POLICY_PROVIDER:-"none"}" != "none" ] || [ "${ENABLE_NETD:-}" == "true" ]; then
    # Use Kubernetes cni daemonset on node if network policy provider is specified
    # or netd is enabled.
    cni_bin_dir="${KUBE_HOME}/bin"
    cni_template_path=""
  fi
fi
log_level="${CONTAINERD_LOG_LEVEL:-"info"}"
max_container_log_line="${CONTAINERD_MAX_CONTAINER_LOG_LINE:-16384}"
cat > ${config_path} <<EOF
# Kubernetes requires the cri plugin.
required_plugins = ["cri"]
# Kubernetes doesn't use containerd restart manager.
disabled_plugins = ["restart"]

[debug]
  level = "${log_level}"

[plugins.cri]
  stream_server_address = "127.0.0.1"
  stream_server_port = "0"
  max_container_log_line_size = ${max_container_log_line}
[plugins.cri.cni]
  bin_dir = "${cni_bin_dir}"
  conf_dir = "/etc/cni/net.d"
  conf_template = "${cni_template_path}"
[plugins.cri.registry.mirrors."docker.io"]
  endpoint = ["https://mirror.gcr.io","https://registry-1.docker.io"]
[plugins.cri.containerd.default_runtime]
  runtime_type = "io.containerd.runc.v1"
[plugins.cri.containerd.default_runtime.options]
  BinaryName = "${CONTAINERD_HOME}/usr/local/sbin/runc"
EOF
chmod 644 "${config_path}"

# containerd_extra_runtime_handler is the extra runtime handler to install.
containerd_extra_runtime_handler=${CONTAINERD_EXTRA_RUNTIME_HANDLER:-""}
if [[ -n "${containerd_extra_runtime_handler}" ]]; then
  cat >> ${config_path} <<EOF
[plugins.cri.containerd.runtimes.${containerd_extra_runtime_handler}]
  runtime_type = "${CONTAINERD_EXTRA_RUNTIME_TYPE:-io.containerd.runc.v1}"

[plugins.cri.containerd.runtimes.${containerd_extra_runtime_handler}.options]
${CONTAINERD_EXTRA_RUNTIME_OPTIONS:-}
EOF
fi

echo "export PATH=${CONTAINERD_HOME}/usr/local/bin/:${CONTAINERD_HOME}/usr/local/sbin/:\$PATH" > \
  /etc/profile.d/containerd_env.sh

# Run extra init script for test.
if [ "${CONTAINERD_TEST:-"false"}"  == "true" ]; then
  # EXTRA_INIT_SCRIPT is the name of the extra init script after being downloaded.
  EXTRA_INIT_SCRIPT="containerd-extra-init.sh"
  # EXTRA_INIT_SCRIPT_METADATA is the metadata key of init script.
  EXTRA_INIT_SCRIPT_METADATA="containerd-extra-init-sh"
  extra_init=$(fetch_metadata "${EXTRA_INIT_SCRIPT_METADATA}")
  # Return if containerd-extra-init-sh is not set.
  if [ -z "${extra_init}" ]; then
    exit 0
  fi
  echo "${extra_init}" > "${EXTRA_INIT_SCRIPT}"
  chmod 544 "${EXTRA_INIT_SCRIPT}"
  ./${EXTRA_INIT_SCRIPT}
fi
