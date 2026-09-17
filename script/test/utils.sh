#!/bin/bash

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

# USE_HYPERV configures containerd to spawn Hyper-V isolated containers
# when running on Windows.
USE_HYPERV=${USE_HYPERV:-0}

IS_WINDOWS=0
if [ -v "OS" ] && [ "${OS}" == "Windows_NT" ]; then
  IS_WINDOWS=1
fi

# RESTART_WAIT_PERIOD is the period to wait before restarting containerd.
RESTART_WAIT_PERIOD=${RESTART_WAIT_PERIOD:-10}

if [ $IS_WINDOWS -eq 0 ]; then
  CONTAINERD_CONFIG_DIR=${CONTAINERD_CONFIG_DIR:-"/tmp"}
else
  CONTAINERD_CONFIG_DIR=${CONTAINERD_CONFIG_DIR:-"c:/Windows/Temp"}
fi

# Use a configuration file for containerd.
CONTAINERD_CONFIG_FILE=${CONTAINERD_CONFIG_FILE:-""}
# The runtime to use (ignored when CONTAINERD_CONFIG_FILE is set)
CONTAINERD_RUNTIME=${CONTAINERD_RUNTIME:-""}
# _config_copy holds the path of the temp copy created for a pre-supplied config
# so that test_teardown can remove it.
_config_copy=""
if [ -n "${CONTAINERD_CONFIG_FILE}" ] && [ $IS_WINDOWS -eq 0 ]; then
  # A pre-supplied config is caller-owned and may be read-only or reused across
  # runs.  Copy it to a test-owned temp file so that test-only settings (NRI,
  # etc.) can be appended without mutating the original or causing permission
  # errors on root-owned paths.
  #
  # Prefer placing the copy in the same directory as the original so that
  # relative imports entries (e.g. ./conf.d/*.toml) continue to resolve
  # correctly against the same base directory that containerd would use.
  # If the original directory is not writable (e.g. /etc/containerd owned by
  # root), fall back to CONTAINERD_CONFIG_DIR — but only when the config
  # contains no relative imports entries.
  #
  # containerd resolves every non-absolute import path relative to the config
  # file's directory (cmd/containerd/server/config/config.go:resolveImports),
  # so bare paths like "conf.d/*.toml" are also relative — not just "./…".
  # Use awk to extract quoted strings from the imports array and check each one.
  _config_orig_dir="$(dirname "${CONTAINERD_CONFIG_FILE}")"
  _has_relative_imports() {
    # \047 is octal for single-quote, safe inside awk's single-quoted program.
    # Match each quoted string in the imports array; treat any value that does
    # not start with / as a relative path (mirrors resolveImports behaviour).
    # Use [ \t] instead of \s — POSIX awk does not support \s in regex.
    awk '
      /^[ \t]*imports[ \t]*=/ { in_imports=1 }
      in_imports {
        line = $0
        gsub(/#.*/, "", line)
        # Match complete quoted strings (opening quote, contents, closing quote).
        # Without the closing quote in the pattern the scanner advances one byte
        # too far and the inter-value punctuation is misread as the next value.
        while (match(line, /["\047][^"\047]*["\047]/)) {
          val = substr(line, RSTART+1, RLENGTH-2)
          if (substr(val,1,1) != "/") { found=1; exit }
          line = substr(line, RSTART+RLENGTH)
        }
        if (/\]/) { exit }
      }
      END { exit !found }
    ' "$1"
  }
  if [ -w "${_config_orig_dir}" ]; then
    # Use a .tmp extension so the temp copy is not picked up by wildcard import
    # globs in the original config (e.g. imports = ["*.toml"]) — containerd
    # loads the copy directly via --config so the extension is irrelevant to it.
    _config_copy="$(mktemp "${_config_orig_dir}/containerd-config-cri-XXXXXX.tmp")"
  else
    if _has_relative_imports "${CONTAINERD_CONFIG_FILE}"; then
      echo "error: CONTAINERD_CONFIG_FILE '${CONTAINERD_CONFIG_FILE}' is in a" \
           "non-writable directory and contains relative imports entries." \
           "Resolve imports to absolute paths or set CONTAINERD_CONFIG_DIR to" \
           "the config's directory." >&2
      exit 1
    fi
    _config_copy="$(mktemp "${CONTAINERD_CONFIG_DIR}/containerd-config-cri-XXXXXX.tmp")"
  fi
  # Use cat redirection rather than cp so that the temp file retains the
  # permissions set by mktemp (0600) regardless of the source file's mode.
  # A caller-supplied config with mode 0444 would otherwise produce a
  # read-only copy, causing subsequent edits (NRI, SystemdCgroup, etc.) to fail.
  cat "${CONTAINERD_CONFIG_FILE}" > "${_config_copy}"
  CONTAINERD_CONFIG_FILE="${_config_copy}"
fi
if [ -z "${CONTAINERD_CONFIG_FILE}" ]; then
  config_file="${CONTAINERD_CONFIG_DIR}/containerd-config-cri.toml"
  truncate --size 0 "${config_file}"
  # TODO(fuweid): if the config.Imports supports patch update, it will be easy
  # to write the integration test case with different configuration, like:
  #
  # 1. write configuration into importable containerd config path.
  # 2. restart containerd
  # 3. verify the behaviour
  # 4. delete the configuration
  # 5. restart containerd
  cat >>${config_file} <<EOF
version=2

[plugins."io.containerd.grpc.v1.cri"]
  drain_exec_sync_io_timeout = "10s"
EOF
  if command -v sestatus >/dev/null 2>&1; then
    cat >>${config_file} <<EOF
  enable_selinux = true
EOF
  fi

  cat >>${config_file} <<EOF
# Userns requires idmap mount support for overlayfs (added in 5.19)
# Let's opt-in for a recursive chown, so we can always test this even in old distros.
# Note that if idmap mounts support is present, we will use that, so it is harmless to keep this
# here.
[plugins."io.containerd.snapshotter.v1.overlayfs"]
    slow_chown = true
EOF

  if command -v sestatus >/dev/null 2>&1; then
    cat >>${config_file} <<EOF
  enable_selinux = true
EOF
  fi
  if [ -n "${CONTAINERD_RUNTIME}" ]; then
    cat >>${config_file} <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
runtime_type = "${CONTAINERD_RUNTIME}"
EOF
  fi

  CONTAINERD_CONFIG_FILE="${config_file}"
fi

# Append the runc-fp failpoint runtime to whichever config is in use, unless it
# is already present.  The failpoint tests skip themselves under runsc via
# t.Skip, so appending the table to a runsc config is harmless; callers that
# supply their own config still need the runtime for the failpoint test suite.
#
# containerd 2.x configs (version = 3) use io.containerd.cri.v1.runtime while
# 1.x configs (version = 2) use io.containerd.grpc.v1.cri.  Detect which is
# active and emit the runc-fp table under the correct namespace.
FAILPOINT_CONTAINERD_RUNTIME="runc-fp.v1"
FAILPOINT_CNI_CONF_DIR=${FAILPOINT_CNI_CONF_DIR:-"/tmp/failpoint-cni-net.d"}
# Determine the CRI plugin namespace used by this config.
# Prefer the explicit v3 namespace if present; fall back to checking the version
# field; otherwise default to the legacy v2 namespace for generated configs.
if [ $IS_WINDOWS -eq 0 ]; then
  if grep -Eq '^[[:space:]]*\[plugins\.["'"'"'](io\.containerd\.cri\.v1\.runtime)["'"'"']' \
       "${CONTAINERD_CONFIG_FILE}"; then
    _cri_ns="io.containerd.cri.v1.runtime"
  elif grep -Eq '^[[:space:]]*version[[:space:]]*=[[:space:]]*3' \
       "${CONTAINERD_CONFIG_FILE}"; then
    _cri_ns="io.containerd.cri.v1.runtime"
  else
    _cri_ns="io.containerd.grpc.v1.cri"
  fi
fi
if [ $IS_WINDOWS -eq 0 ] && \
   ! grep -Eq '^[[:space:]]*\[plugins\.["'"'"'](io\.containerd\.grpc\.v1\.cri|io\.containerd\.cri\.v1\.runtime)["'"'"']\.containerd\.runtimes\.runc-fp\][[:space:]]*(#.*)?$' \
     "${CONTAINERD_CONFIG_FILE}"; then
  mkdir -p "${FAILPOINT_CNI_CONF_DIR}"
  # Ensure the file ends with a newline before appending.
  if [ -s "${CONTAINERD_CONFIG_FILE}" ] && \
     [ "$(tail -c1 "${CONTAINERD_CONFIG_FILE}" | wc -l)" -eq 0 ]; then
    echo >> "${CONTAINERD_CONFIG_FILE}"
  fi
  cat >> "${CONTAINERD_CONFIG_FILE}" <<EOF
[plugins."${_cri_ns}".containerd.runtimes.runc-fp]
  cni_conf_dir = "${FAILPOINT_CNI_CONF_DIR}"
  cni_max_conf_num = 1
  pod_annotations = ["io.containerd.runtime.v2.shim.failpoint.*"]
  runtime_type = "${FAILPOINT_CONTAINERD_RUNTIME}"
EOF
  cat << EOF | tee "${FAILPOINT_CNI_CONF_DIR}/10-containerd-net.conflist"
{
  "cniVersion": "1.0.0",
  "name": "containerd-net-failpoint",
  "plugins": [
    {
      "type": "cni-bridge-fp",
      "bridge": "cni-fp",
      "isGateway": true,
      "ipMasq": true,
      "promiscMode": true,
      "ipam": {
        "type": "host-local",
        "ranges": [
          [{
            "subnet": "10.88.0.0/16"
          }],
          [{
            "subnet": "2001:4860:4860::/64"
          }]
        ],
        "routes": [
          { "dst": "0.0.0.0/0" },
          { "dst": "::/0" }
        ]
      },
      "capabilities": {
        "io.kubernetes.cri.pod-annotations": true
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
EOF
fi

# Ensure SystemdCgroup = true is in effect under the runc.options table when
# CGROUP_DRIVER=systemd is set.  Three cases to handle:
#   1. Key is absent from the table → insert it (append or inject after header).
#   2. Key is present with value = true → nothing to do.
#   3. Key is present with value = false → overwrite it to true.
# Both double-quoted and single-quoted TOML key spellings are recognised.
# A pure grep would be fooled by commented-out keys or keys in other tables.
# _cri_ns is set above (in the failpoint block) to the active CRI plugin
# namespace: io.containerd.cri.v1.runtime (v3) or io.containerd.grpc.v1.cri (v2).
_runc_opts_table_dq="[plugins.\"${_cri_ns:-io.containerd.grpc.v1.cri}\".containerd.runtimes.runc.options]"
_runc_opts_table_sq="[plugins.'${_cri_ns:-io.containerd.grpc.v1.cri}'.containerd.runtimes.runc.options]"
# Returns 0 (true) when SystemdCgroup = true is already active in the table.
_systemd_cgroup_is_true() {
  awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
    { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
    line == tbl_dq || line == tbl_sq { in_table=1; next }
    in_table && /^[[:space:]]*\[/    { exit }
    in_table && /^[[:space:]]*#/     { next }
    in_table && line ~ /SystemdCgroup[[:space:]]*=[[:space:]]*true/ { found=1; exit }
    END { exit !found }
  ' "$1"
}
# Returns 0 (true) when SystemdCgroup = false is active in the table.
_systemd_cgroup_is_false() {
  awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
    { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
    line == tbl_dq || line == tbl_sq { in_table=1; next }
    in_table && /^[[:space:]]*\[/    { exit }
    in_table && /^[[:space:]]*#/     { next }
    in_table && line ~ /SystemdCgroup[[:space:]]*=[[:space:]]*false/ { found=1; exit }
    END { exit !found }
  ' "$1"
}
if [ $IS_WINDOWS -eq 0 ] && [ "${CGROUP_DRIVER:-}" = "systemd" ]; then
  # Escape dots in _cri_ns for use in sed/grep regex addresses.
  _cri_ns_re="$(echo "${_cri_ns:-io.containerd.grpc.v1.cri}" | sed 's/\./\\./g')"
  if _systemd_cgroup_is_false "${CONTAINERD_CONFIG_FILE}"; then
    # Key exists but is set to false — overwrite the value in-place, restricted
    # to the runc.options table range so other runtimes are not affected.
    # The sed address is a prefix match (no $), so a trailing inline comment on
    # the header line does not prevent the range from being entered.
    sed -i \
      -e "/^[[:space:]]*\[plugins\.\"${_cri_ns_re}\"\.containerd\.runtimes\.runc\.options\][[:space:]]*/,/^[[:space:]]*\[/s/\(SystemdCgroup[[:space:]]*=[[:space:]]*\)false/\1true/" \
      -e "/^[[:space:]]*\[plugins\.'${_cri_ns_re}'\.containerd\.runtimes\.runc\.options\][[:space:]]*/,/^[[:space:]]*\[/s/\(SystemdCgroup[[:space:]]*=[[:space:]]*\)false/\1true/" \
      "${CONTAINERD_CONFIG_FILE}"
  elif ! _systemd_cgroup_is_true "${CONTAINERD_CONFIG_FILE}"; then
    # Key is absent — insert it.
    # Ensure the file ends with a newline before appending.
    if [ -s "${CONTAINERD_CONFIG_FILE}" ] && \
       [ "$(tail -c1 "${CONTAINERD_CONFIG_FILE}" | wc -l)" -eq 0 ]; then
      echo >> "${CONTAINERD_CONFIG_FILE}"
    fi
    # If the runc.options table header already exists (but lacks SystemdCgroup),
    # insert the key after the header line.  The grep and sed patterns use the
    # character class ['"'"'"] to match either TOML quote style ("…" or '…').
    # Otherwise append a new double-quoted table block.
    if grep -Eq "^[[:space:]]*\\[plugins\\.['\"]${_cri_ns_re}['\"]\.containerd\.runtimes\.runc\.options\\][[:space:]]*(#.*)?$" "${CONTAINERD_CONFIG_FILE}"; then
      sed -i "/^[[:space:]]*\\[plugins\\.['\"]${_cri_ns_re}['\"]\.containerd\.runtimes\.runc\.options\\][[:space:]]*/a\\
  SystemdCgroup = true" "${CONTAINERD_CONFIG_FILE}"
    else
      cat >> "${CONTAINERD_CONFIG_FILE}" <<EOF
[plugins."${_cri_ns:-io.containerd.grpc.v1.cri}".containerd.runtimes.runc.options]
  SystemdCgroup = true
EOF
    fi
  fi
fi

# Append the NRI test config to whichever config file is in use (self-generated
# or pre-supplied).  This must run for both paths so that NRI integration tests
# work regardless of which runtime is under test.
# Only add the NRI table if the config does not already contain it; a duplicate
# TOML table causes containerd to reject the config on startup.
# Ensure the file ends with a newline before appending so that the table header
# is never concatenated onto an existing value line (which produces invalid TOML).
if [ $IS_WINDOWS -eq 0 ]; then
  if ! grep -Eq '^[[:space:]]*\[plugins\.("io\.containerd\.nri\.v1\.nri"|'"'"'io\.containerd\.nri\.v1\.nri'"'"')\][[:space:]]*(#.*)?$' "${CONTAINERD_CONFIG_FILE}"; then
    # Add a newline if the file does not already end with one.
    if [ -s "${CONTAINERD_CONFIG_FILE}" ] && \
       [ "$(tail -c1 "${CONTAINERD_CONFIG_FILE}" | wc -l)" -eq 0 ]; then
      echo >> "${CONTAINERD_CONFIG_FILE}"
    fi
    cat >> "${CONTAINERD_CONFIG_FILE}" <<EOF
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  socket_path = "/var/run/nri-test.sock"
  plugin_path = "/no/pre-launched/nri/plugins"
EOF
  fi
fi

if [ ${IS_WINDOWS} -eq 1 -a ${USE_HYPERV} -eq 1 ];then
  cat >> ${CONTAINERD_CONFIG_FILE} << EOF
version = 2
[plugins]
    [plugins."io.containerd.grpc.v1.cri".containerd]
      default_runtime_name = "runhcs-wcow-hyperv"
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes]

       [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runhcs-wcow-hyperv]
        runtime_type = "io.containerd.runhcs.v1"
        [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runhcs-wcow-hyperv.options]
          Debug = true
          DebugType = 2
          SandboxPlatform = "windows/amd64"
          SandboxIsolation = 1
EOF
fi
# To allow the cri-integration test to run via CLI without explicitly setting CGROUP_DRIVER
if [ $IS_WINDOWS -eq 0 ] && [ ! -v CGROUP_DRIVER ]; then
  echo "CGROUP_DRIVER is unset"
fi

# CONTAINERD_TEST_SUFFIX is the suffix appended to the root/state directory used
# by test containerd.
CONTAINERD_TEST_SUFFIX=${CONTAINERD_TEST_SUFFIX:-"-test"}
if [ $IS_WINDOWS -eq 0 ]; then
  # The containerd root directory.
  CONTAINERD_ROOT=${CONTAINERD_ROOT:-"/var/lib/containerd${CONTAINERD_TEST_SUFFIX}"}
  # The containerd state directory.
  CONTAINERD_STATE=${CONTAINERD_STATE:-"/run/containerd${CONTAINERD_TEST_SUFFIX}"}
  # The containerd socket address.
  CONTAINERD_SOCK=${CONTAINERD_SOCK:-unix://${CONTAINERD_STATE}/containerd.sock}
  TRIMMED_CONTAINERD_SOCK="${CONTAINERD_SOCK#unix://}"
else
  # $ProgramData holds the Windows path to the ProgramData folder in standard Windows
  # format. The backslash in the path may be interpreted by bash, so we convert the
  # Windows path to POSIX path using cygpath.exe. The end result should be something
  # similar to /c/ProgramData/.
  POSIX_PROGRAM_DATA="$(cygpath.exe $ProgramData)"

  CONTAINERD_ROOT=${CONTAINERD_ROOT:-"$POSIX_PROGRAM_DATA/containerd/root${CONTAINERD_TEST_SUFFIX}"}
  CONTAINERD_STATE=${CONTAINERD_STATE:-"$POSIX_PROGRAM_DATA/containerd/state${CONTAINERD_TEST_SUFFIX}"}

  # Remove drive letter
  PIPE_STATE="${CONTAINERD_STATE#*:/}"
  # Remove leading slash
  PIPE_STATE="${PIPE_STATE#/}"
  # Replace empty space with dash
  PIPE_STATE="${PIPE_STATE// /"-"}"
  CONTAINERD_SOCK=${CONTAINERD_SOCK:-npipe://./pipe/${PIPE_STATE}/containerd}
  TRIMMED_CONTAINERD_SOCK="${CONTAINERD_SOCK#npipe:}"
fi

# The containerd binary name.
EXE_SUFFIX=""
if [ $IS_WINDOWS -eq 1 ]; then
  EXE_SUFFIX=".exe"
fi
CONTAINERD_BIN=${CONTAINERD_BIN:-"containerd"}${EXE_SUFFIX}

pid=

# NOTE: We don't have the sudo command on Windows.
sudo=""
if [ "$(id -u)" -ne 0 ] && command -v sudo &> /dev/null; then
  sudo="sudo PATH=${PATH}"
fi


# The run_containerd function is a wrapper that will run the appropriate
# containerd command based on the OS we're running the tests on. This wrapper
# is needed if we plan to run the containerd command as part of a retry cycle
# as is the case on Linux, where we use the keepalive function. Using a wrapper
# allows us to avoid the need for eval, while allowing us to quote the paths
# to the state and root folders. This allows us to use paths that have spaces
# in them without erring out.
run_containerd() {
  # not used on linux
  if [ $# -gt 0 ]; then
    local report_dir=$1
  fi
  CMD=""
  if [ -n "${sudo}" ]; then
    CMD+="${sudo} "
  fi
  CMD+="${PWD}/bin/containerd"

  if [ $IS_WINDOWS -eq 0 ]; then
    $CMD --log-level=debug \
      --config "${CONTAINERD_CONFIG_FILE}" \
      --address "${TRIMMED_CONTAINERD_SOCK}" \
      --state "${CONTAINERD_STATE}" \
      --root "${CONTAINERD_ROOT}"
  else
    # Note(gsamfira): On Windows, we register a containerd-test service which will run under
    # LocalSystem. This user is part of the local Administrators group and should have all
    # required permissions to successfully start containers.
    # The --register-service parameter will do this for us.
    $CMD --log-level=debug \
      --config "${CONTAINERD_CONFIG_FILE}" \
      --address "${TRIMMED_CONTAINERD_SOCK}" \
      --state "${CONTAINERD_STATE}" \
      --root "${CONTAINERD_ROOT}" \
      --log-file "${report_dir}/containerd.log" \
      --service-name containerd-test \
      --register-service
  fi
}

# test_setup starts containerd.
test_setup() {
  local report_dir=$1
  # Start containerd
  if [ ! -x "bin/containerd" ]; then
    echo "containerd is not built"
    exit 1
  fi
  set -m
  # Create containerd in a different process group
  # so that we can easily clean them up.
  if [ $IS_WINDOWS -eq 0 ]; then
    keepalive run_containerd \
      "${RESTART_WAIT_PERIOD}" &> "${report_dir}/containerd.log" &
    pid=$!
  else
    if [ ! -d "${CONTAINERD_ROOT}" ]; then
      # Create the containerd ROOT dir and set full access to be inherited for "CREATOR OWNER"
      # on all subfolders and files.
      mkdir -p "${CONTAINERD_ROOT}"
      cmd.exe /c 'icacls.exe "'$(cygpath -w "${CONTAINERD_ROOT}")'" /grant "CREATOR OWNER":(OI)(CI)(IO)F /T'
    fi

    run_containerd "$report_dir"

    # Set failure flag on the test service. This will restart the service
    # in case of failure.
    sc.exe failure containerd-test reset=0 actions=restart/1000
    sc.exe failureflag containerd-test 1

    # it might still result in SERVICE_START_PENDING, but we can ignore it.
    sc.exe start containerd-test || true
    pid="1"  # for teardown
  fi
  set +m

  # Wait for containerd to be running by using the containerd client ctr to check the version
  # of the containerd server. Wait an increasing amount of time after each of five attempts
  local -r crictl_path=$(which crictl)
  if [ -z "${crictl_path}" ]; then
    echo "crictl is not in PATH"
    exit 1
  fi
  readiness_check run_ctr
  readiness_check run_crictl
  # Show the config about cri plugin in log when it's ready
  run_crictl
}

# test_teardown kills containerd and removes any temp config copy.
test_teardown() {
  if [ -n "${pid}" ]; then
    if [ $IS_WINDOWS -eq 1 ]; then
      # Mark service for deletion. It will be deleted as soon as the service stops.
      sc.exe delete containerd-test
      # Stop the service
      sc.exe stop containerd-test || true
    else
      pgid=$(ps -o pgid= -p "${pid}" || true)
      if [ ! -z "${pgid}" ]; then
        ${sudo} pkill -g ${pgid}
      else
        echo "pid(${pid}) not found, skipping pkill"
      fi
    fi
  fi
  # Remove the temp copy of a pre-supplied config created at sourcing time.
  if [ -n "${_config_copy}" ]; then
    ${sudo} rm -f "${_config_copy}"
    _config_copy=""
  fi
}

run_ctr() {
  ${sudo} ${PWD}/bin/ctr --address "${TRIMMED_CONTAINERD_SOCK}" version
}

run_crictl() {
  ${sudo} ${crictl_path} --runtime-endpoint="${CONTAINERD_SOCK}" info
}

# keepalive runs a command and keeps it alive.
# keepalive process is eventually killed in test_teardown.
keepalive() {
  # The command may return non-zero and we want to continue this script.
  # e.g. containerd receives SIGKILL
  set +e
  local command=$1
  echo "${command}"
  local wait_period=$2
  while true; do
    ${command}
    sleep "${wait_period}"
  done
}

# readiness_check checks readiness of a daemon with specified command.
readiness_check() {
  local command=$1
  local MAX_ATTEMPTS=5
  local attempt_num=1
  until ${command} &>/dev/null || (( attempt_num == MAX_ATTEMPTS ))
  do
      echo "$attempt_num attempt \"$command\"! Trying again in $attempt_num seconds..."
      sleep $(( attempt_num++ ))
  done
  set -x
  cat "${report_dir}/containerd.log"
  cat "${CONTAINERD_CONFIG_FILE}"
  set +x
}
