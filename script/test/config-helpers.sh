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

# config-helpers.sh — TOML config-graph helpers and per-run config setup.
#
# Sourced by utils.sh after IS_WINDOWS, CONTAINERD_CONFIG_DIR, and
# CONTAINERD_CONFIG_FILE are fully resolved.  All variables and functions
# defined here share the sourcing shell's namespace.
#
# Bash 3.2+ compatible (macOS ships Bash 3.2; no associative arrays used).

# _canon_path PATH — lexically clean a path (mirrors filepath.Clean).
_canon_path() {
  if readlink -m / >/dev/null 2>&1; then
    readlink -m "$1"
    return
  fi
  local path="$1" result="" part
  local is_abs=0
  case "${path}" in /*) is_abs=1 ;; esac
  local IFS=/
  for part in ${path}; do
    case "${part}" in
      ""|.) ;;
      ..) if [ -n "${result}" ]; then result="${result%/*}"; else result=""; fi ;;
      *)  result="${result}/${part}" ;;
    esac
  done
  if [ ${is_abs} -eq 1 ]; then
    echo "/${result#/}"
  elif [ -n "${result}" ]; then
    echo "${result#/}"
  else
    echo "."
  fi
}

# _extract_imports FILE — prints one raw import path per line.
_extract_imports() {
  awk '
    /^[ \t]*imports[ \t]*=/ { in_imports=1 }
    in_imports {
      line = $0
      gsub(/#.*/, "", line)
      while (match(line, /["\047][^"\047]*["\047]/)) {
        val = substr(line, RSTART+1, RLENGTH-2)
        print val
        line = substr(line, RSTART+RLENGTH)
      }
      if (line ~ /\]/) { exit }
    }
  ' "$1"
}

# _array_contains NEEDLE [ELEMENT ...] — returns 0 if NEEDLE equals any ELEMENT.
_array_contains() {
  local needle="$1"; shift
  local item
  for item in "$@"; do [ "${item}" = "${needle}" ] && return 0; done
  return 1
}

# _resolve_import_paths CFG_DIR IMPORT_PATH — outputs matched existing files, one per line.
_resolve_import_paths() {
  local cfg_dir="$1" import_path="$2" resolved
  case "${import_path}" in
    /*) resolved="${import_path}" ;;
    *)  resolved="${cfg_dir}/${import_path}" ;;
  esac
  if [[ "${resolved}" == *\** || "${resolved}" == *\?* || "${resolved}" == *\[* ]]; then
    local IFS=
    local _prev_nullglob
    _prev_nullglob="$(shopt -p nullglob || true)"
    shopt -s nullglob
    local -a _matches=(${resolved})
    eval "${_prev_nullglob}"
    local f
    for f in "${_matches[@]}"; do
      [ -f "${f}" ] && echo "$(_canon_path "${f}")"
    done
  else
    resolved="$(_canon_path "${resolved}")"
    [ -f "${resolved}" ] && echo "${resolved}"
  fi
}

_grep_config() {
  local pattern="$1"
  local root_cfg="$2"
  local -a pending=("${root_cfg}")
  local -a visited=()

  while [ "${#pending[@]}" -gt 0 ]; do
    local cfg="${pending[0]}"
    pending=("${pending[@]:1}")
    _array_contains "${cfg}" "${visited[@]+"${visited[@]}"}" && continue
    visited+=("${cfg}")
    [ -f "${cfg}" ] || continue
    if grep -Eq "${pattern}" "${cfg}" 2>/dev/null; then
      return 0
    fi
    local cfg_dir
    cfg_dir="$(dirname "${cfg}")"
    local import_path resolved
    while IFS= read -r import_path; do
      while IFS= read -r resolved; do
        [ -n "${resolved}" ] && pending+=("${resolved}")
      done < <(_resolve_import_paths "${cfg_dir}" "${import_path}")
    done < <(_extract_imports "${cfg}")
  done

  return 1
}

FAILPOINT_CONTAINERD_RUNTIME="runc-fp.v1"
FAILPOINT_CNI_CONF_DIR=${FAILPOINT_CNI_CONF_DIR:-"/tmp/failpoint-cni-net.d"}
if [ $IS_WINDOWS -eq 0 ]; then
  if _grep_config '^[[:space:]]*\[plugins\.["'"'"'](io\.containerd\.cri\.v1\.runtime)["'"'"']' \
       "${CONTAINERD_CONFIG_FILE}"; then
    _cri_ns="io.containerd.cri.v1.runtime"
  elif _grep_config '^[[:space:]]*\[plugins\.["'"'"'](io\.containerd\.grpc\.v1\.cri)["'"'"']' \
       "${CONTAINERD_CONFIG_FILE}"; then
    _cri_ns="io.containerd.grpc.v1.cri"
  elif _grep_config '^[[:space:]]*version[[:space:]]*=[[:space:]]*[3-9][0-9]*' \
       "${CONTAINERD_CONFIG_FILE}"; then
    _cri_ns="io.containerd.cri.v1.runtime"
  else
    _cri_ns="io.containerd.grpc.v1.cri"
  fi
fi
_cri_ns_re_fp="$(echo "${_cri_ns:-io.containerd.grpc.v1.cri}" | sed 's/\./\\./g')"
if [ $IS_WINDOWS -eq 0 ] && \
   ! _grep_config "^[[:space:]]*\\[plugins\\.['\"](${_cri_ns_re_fp})['\"]\.containerd\.runtimes\.runc-fp\\][[:space:]]*(#.*)?$" \
     "${CONTAINERD_CONFIG_FILE}"; then
  mkdir -p "${FAILPOINT_CNI_CONF_DIR}"
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

_runc_opts_table_dq="[plugins.\"${_cri_ns:-io.containerd.grpc.v1.cri}\".containerd.runtimes.runc.options]"
_runc_opts_table_sq="[plugins.'${_cri_ns:-io.containerd.grpc.v1.cri}'.containerd.runtimes.runc.options]"

_find_runc_opts_file() {
  local root_cfg="$1"
  local -a pending=("${root_cfg}")
  local -a visited=()
  while [ "${#pending[@]}" -gt 0 ]; do
    local cfg="${pending[0]}"
    pending=("${pending[@]:1}")
    _array_contains "${cfg}" "${visited[@]+"${visited[@]}"}" && continue
    visited+=("${cfg}")
    [ -f "${cfg}" ] || continue
    if awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
         { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
         line == tbl_dq || line == tbl_sq { found=1; exit }
         END { exit !found }
       ' "${cfg}"; then
      echo "${cfg}"
      return 0
    fi
    local cfg_dir
    cfg_dir="$(dirname "${cfg}")"
    local import_path resolved
    while IFS= read -r import_path; do
      while IFS= read -r resolved; do
        [ -n "${resolved}" ] && pending+=("${resolved}")
      done < <(_resolve_import_paths "${cfg_dir}" "${import_path}")
    done < <(_extract_imports "${cfg}")
  done
  return 1
}

_systemd_cgroup_is_true() {
  local root_cfg="$1"
  local -a pending=("${root_cfg}")
  local -a visited=()
  while [ "${#pending[@]}" -gt 0 ]; do
    local cfg="${pending[0]}"
    pending=("${pending[@]:1}")
    _array_contains "${cfg}" "${visited[@]+"${visited[@]}"}" && continue
    visited+=("${cfg}")
    [ -f "${cfg}" ] || continue
    if awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
         { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
         line == tbl_dq || line == tbl_sq { in_table=1; next }
         in_table && /^[[:space:]]*\[/    { exit }
         in_table && /^[[:space:]]*#/     { next }
         in_table && line ~ /^["'"'"'"]?SystemdCgroup["'"'"'"]?[[:space:]]*=[[:space:]]*true([[:space:]]|$)/ { found=1; exit }
         END { exit !found }
       ' "${cfg}"; then
      return 0
    fi
    local cfg_dir
    cfg_dir="$(dirname "${cfg}")"
    local import_path resolved
    while IFS= read -r import_path; do
      while IFS= read -r resolved; do
        [ -n "${resolved}" ] && pending+=("${resolved}")
      done < <(_resolve_import_paths "${cfg_dir}" "${import_path}")
    done < <(_extract_imports "${cfg}")
  done
  return 1
}

_systemd_cgroup_is_false() {
  local root_cfg="$1"
  local -a pending=("${root_cfg}")
  local -a visited=()
  while [ "${#pending[@]}" -gt 0 ]; do
    local cfg="${pending[0]}"
    pending=("${pending[@]:1}")
    _array_contains "${cfg}" "${visited[@]+"${visited[@]}"}" && continue
    visited+=("${cfg}")
    [ -f "${cfg}" ] || continue
    if awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
         { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
         line == tbl_dq || line == tbl_sq { in_table=1; next }
         in_table && /^[[:space:]]*\[/    { exit }
         in_table && /^[[:space:]]*#/     { next }
         in_table && line ~ /^["'"'"'"]?SystemdCgroup["'"'"'"]?[[:space:]]*=[[:space:]]*false([[:space:]]|$)/ { found=1; exit }
         END { exit !found }
       ' "${cfg}"; then
      return 0
    fi
    local cfg_dir
    cfg_dir="$(dirname "${cfg}")"
    local import_path resolved
    while IFS= read -r import_path; do
      while IFS= read -r resolved; do
        [ -n "${resolved}" ] && pending+=("${resolved}")
      done < <(_resolve_import_paths "${cfg_dir}" "${import_path}")
    done < <(_extract_imports "${cfg}")
  done
  return 1
}

if [ $IS_WINDOWS -eq 0 ] && [ "${CGROUP_DRIVER:-}" = "systemd" ]; then
  # _script_owned_paths — indexed array of paths that the script may freely modify.
  _script_owned_paths=("${CONTAINERD_CONFIG_FILE}")
  # _copy_map_keys / _copy_map_vals — parallel arrays mapping orig path → copy path
  # (used by _copy_if_needed for glob expansion de-duplication).
  _copy_map_keys=()
  _copy_map_vals=()
  _import_copies=()
  # _glob_handled_keys — indexed array of "parent:glob" strings already processed.
  _glob_handled_keys=()
  # Output variable used by _make_cfg_copy and _copy_if_needed to avoid
  # command substitution (which would run them in a subshell and lose all
  # mutations to the above arrays).
  _copy_result=""

  # Copy ORIG into CONTAINERD_CONFIG_DIR, absolutising its relative imports.
  # Sets _copy_result to the path of the new copy.  Must NOT be called via $().
  _make_cfg_copy() {
    local orig="$1"
    local orig_dir
    orig_dir="$(cd -- "$(dirname -- "${orig}")" && pwd -P)"
    local copy
    copy="$(mktemp "${CONTAINERD_CONFIG_DIR}/containerd-import-copy-XXXXXX.toml")"
    # Rewrite relative import paths to absolute using positional replacement so
    # the path value is treated as a literal string, not a regex.
    awk -v base="${orig_dir}" '
      /^[[:space:]]*imports[[:space:]]*=/ { in_imports=1 }
      in_imports {
        rest = $0; gsub(/#.*/, "", rest)
        while (match(rest, /["\047][^"\047]*["\047]/)) {
          val = substr(rest, RSTART+1, RLENGTH-2)
          if (substr(val,1,1) != "/") {
            pos = index($0, val)
            if (pos > 0)
              $0 = substr($0,1,pos-1) base "/" val substr($0,pos+length(val))
          }
          rest = substr(rest, RSTART+RLENGTH)
        }
      }
      in_imports && /\]/ { in_imports=0 }
      { print }
    ' "${orig}" > "${copy}"
    _script_owned_paths+=("${copy}")
    _import_copies+=("${copy}")
    _copy_result="${copy}"
  }

  # _rewrite_import_in_parent PARENT OLD_RAW NEW_LITERAL [NEW_LITERAL2 ...]
  # Replace the first quoted occurrence of OLD_RAW in PARENT's imports array
  # with the given new path(s) using positional replacement (not regex).
  _rewrite_import_in_parent() {
    local parent="$1" old_raw="$2"
    shift 2
    local new_list
    if [ "$#" -eq 1 ]; then
      new_list="\"$1\""
    else
      local -a quoted=()
      local p; for p in "$@"; do quoted+=("\"${p}\""); done
      new_list="$(IFS=', '; echo "${quoted[*]}")"
    fi
    local tmp
    tmp="$(mktemp "${CONTAINERD_CONFIG_DIR}/containerd-parent-rewrite-XXXXXX.tmp")"
    awk -v old_dq="\"${old_raw}\"" -v old_sq="'${old_raw}'" \
        -v new_list="${new_list}" '
      /^[[:space:]]*imports[[:space:]]*=/ { in_imports=1 }
      in_imports && !done {
        pos = index($0, old_dq)
        if (pos > 0) {
          $0 = substr($0,1,pos-1) new_list substr($0,pos+length(old_dq))
          done=1
        } else {
          pos = index($0, old_sq)
          if (pos > 0) {
            $0 = substr($0,1,pos-1) new_list substr($0,pos+length(old_sq))
            done=1
          }
        }
      }
      in_imports && /\]/ { in_imports=0 }
      { print }
    ' "${parent}" > "${tmp}" && mv "${tmp}" "${parent}"
  }

  # Copy ORIG (or all files matched by a glob import) into CONTAINERD_CONFIG_DIR
  # and rewrite the parent's import entry to the copy paths.
  # Sets _copy_result to the copy path for ORIG.  Must NOT be called via $().
  _copy_if_needed() {
    local orig="$1" parent="$2" raw_import="$3"

    if [[ "${raw_import}" != *\** && "${raw_import}" != *\?* && "${raw_import}" != *\[* ]]; then
      _make_cfg_copy "${orig}"
      if [ -z "${parent}" ]; then
        # orig is the root config — there is no parent import entry to rewrite.
        # Point CONTAINERD_CONFIG_FILE at the copy so containerd uses it.
        CONTAINERD_CONFIG_FILE="${_copy_result}"
        _script_owned_paths+=("${_copy_result}")
      else
        _rewrite_import_in_parent "${parent}" "${raw_import}" "${_copy_result}"
      fi
      return
    fi

    # Glob: expand once per parent+glob, copy all matches, replace the entry.
    local glob_key="${parent}:${raw_import}"
    if _array_contains "${glob_key}" "${_glob_handled_keys[@]+"${_glob_handled_keys[@]}"}"; then
      # Return the previously recorded copy path for orig, or orig itself.
      local _ki _copy_for_orig="${orig}"
      for _ki in "${!_copy_map_keys[@]}"; do
        [ "${_copy_map_keys[_ki]}" = "${orig}" ] && _copy_for_orig="${_copy_map_vals[_ki]}" && break
      done
      _copy_result="${_copy_for_orig}"
      return
    fi
    _glob_handled_keys+=("${glob_key}")

    local parent_dir resolved_glob
    parent_dir="$(dirname "${parent}")"
    case "${raw_import}" in
      /*) resolved_glob="${raw_import}" ;;
      *)  resolved_glob="${parent_dir}/${raw_import}" ;;
    esac

    local -a new_paths=()
    local f _matches
    _matches=()
    # Safely expand glob without word-splitting paths containing whitespace.
    local IFS=
    local _prev_nullglob
    _prev_nullglob="$(shopt -p nullglob || true)"
    shopt -s nullglob
    # shellcheck disable=SC2206
    _matches=(${resolved_glob})
    eval "${_prev_nullglob}"

    for f in "${_matches[@]}"; do
      [ -f "${f}" ] || continue
      _make_cfg_copy "${f}"
      _copy_map_keys+=("${f}")
      _copy_map_vals+=("${_copy_result}")
      new_paths+=("${_copy_result}")
    done

    if [ "${#new_paths[@]}" -gt 0 ]; then
      _rewrite_import_in_parent "${parent}" "${raw_import}" "${new_paths[@]}"
    fi

    # Return the copy path for orig, or orig itself if not found.
    local _ki _copy_for_orig="${orig}"
    for _ki in "${!_copy_map_keys[@]}"; do
      [ "${_copy_map_keys[_ki]}" = "${orig}" ] && _copy_for_orig="${_copy_map_vals[_ki]}" && break
    done
    _copy_result="${_copy_for_orig}"
  }

  # Set SystemdCgroup = true in every import-graph file with the runc.options
  # table. Returns 0 if at least one such file was found, 1 otherwise.
  _set_systemd_cgroup_true() {
    local root_cfg="$1"
    local -a pending=("${root_cfg}")
    # parent_of, raw_import_of, effective_of: parallel key/value arrays.
    local -a pof_keys=() pof_vals=()
    local -a riof_keys=() riof_vals=()
    local -a eof_keys=() eof_vals=()
    local -a visited=()
    local found_table=0

    # _map_get KEYS_VAR VALS_VAR KEY — prints the value for KEY, or "".
    # Uses nameref-free eval; safe because keys are file paths we control.
    _map_get() {
      local _mg_keys_var="$1" _mg_vals_var="$2" _mg_key="$3"
      local _mg_i _mg_result=""
      eval "local _mg_n=\"\${#${_mg_keys_var}[@]}\""
      local _mg_idx=0
      while [ "${_mg_idx}" -lt "${_mg_n}" ]; do
        eval "local _mg_k=\"\${${_mg_keys_var}[${_mg_idx}]}\""
        if [ "${_mg_k}" = "${_mg_key}" ]; then
          eval "_mg_result=\"\${${_mg_vals_var}[${_mg_idx}]}\""
          break
        fi
        _mg_idx=$(( _mg_idx + 1 ))
      done
      printf '%s' "${_mg_result}"
    }

    # _map_set KEYS_VAR VALS_VAR KEY VALUE — sets KEY=VALUE, appending if new.
    _map_set() {
      local _ms_keys_var="$1" _ms_vals_var="$2" _ms_key="$3" _ms_val="$4"
      local _ms_i _ms_n
      eval "_ms_n=\"\${#${_ms_keys_var}[@]}\""
      local _ms_idx=0
      while [ "${_ms_idx}" -lt "${_ms_n}" ]; do
        eval "local _ms_k=\"\${${_ms_keys_var}[${_ms_idx}]}\""
        if [ "${_ms_k}" = "${_ms_key}" ]; then
          eval "${_ms_vals_var}[${_ms_idx}]=\"\${_ms_val}\""
          return
        fi
        _ms_idx=$(( _ms_idx + 1 ))
      done
      eval "${_ms_keys_var}+=(\"${_ms_key}\")"
      eval "${_ms_vals_var}+=(\"${_ms_val}\")"
    }

    while [ "${#pending[@]}" -gt 0 ]; do
      local cfg="${pending[0]}"
      pending=("${pending[@]:1}")
      _array_contains "${cfg}" "${visited[@]+"${visited[@]}"}" && continue
      visited+=("${cfg}")
      [ -f "${cfg}" ] || continue

      if awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
           { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
           line == tbl_dq || line == tbl_sq { found=1; exit }
           END { exit !found }
         ' "${cfg}"; then
        found_table=1
        if ! awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
               { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
               line == tbl_dq || line == tbl_sq { in_table=1; next }
               in_table && /^[[:space:]]*\[/    { exit }
               in_table && /^[[:space:]]*#/     { next }
               in_table && line ~ /^["'"'"'"]?SystemdCgroup["'"'"'"]?[[:space:]]*=[[:space:]]*true([[:space:]]|$)/ { found=1; exit }
               END { exit !found }
             ' "${cfg}"; then
          local write_cfg
          local _parent; _parent="$(_map_get pof_keys pof_vals "${cfg}")"
          local _raw;    _raw="$(_map_get riof_keys riof_vals "${cfg}")"
          # Ensure every ancestor in the import chain has a script-owned copy
          # before we rewrite its import entry; otherwise _rewrite_import_in_parent
          # would modify the user's original file.
          local _anc="${_parent}"
          while [ -n "${_anc}" ] && \
                ! _array_contains "${_anc}" "${_script_owned_paths[@]+"${_script_owned_paths[@]}"}" && \
                [ -z "$(_map_get eof_keys eof_vals "${_anc}")" ]; do
            local _anc_parent; _anc_parent="$(_map_get pof_keys pof_vals "${_anc}")"
            local _anc_eff_parent; _anc_eff_parent="$(_map_get eof_keys eof_vals "${_anc_parent}")"
            [ -z "${_anc_eff_parent}" ] && _anc_eff_parent="${_anc_parent}"
            local _anc_raw; _anc_raw="$(_map_get riof_keys riof_vals "${_anc}")"
            _copy_if_needed "${_anc}" "${_anc_eff_parent}" "${_anc_raw}"
            _map_set eof_keys eof_vals "${_anc}" "${_copy_result}"
            _anc="${_anc_parent}"
          done
          local _eff_parent; _eff_parent="$(_map_get eof_keys eof_vals "${_parent}")"
          [ -z "${_eff_parent}" ] && _eff_parent="${_parent}"
          if _array_contains "${cfg}" "${_script_owned_paths[@]+"${_script_owned_paths[@]}"}"; then
            write_cfg="${cfg}"
          else
            _copy_if_needed "${cfg}" "${_eff_parent}" "${_raw}"
            write_cfg="${_copy_result}"
            _map_set eof_keys eof_vals "${cfg}" "${write_cfg}"
          fi
          local tmp
          tmp="$(mktemp "${CONTAINERD_CONFIG_DIR}/containerd-systemd-cgroup-XXXXXX.tmp")"
          awk -v tbl_dq="${_runc_opts_table_dq}" -v tbl_sq="${_runc_opts_table_sq}" '
            { line=$0; sub(/^[[:space:]]+/, "", line); sub(/[[:space:]]+$/, "", line); sub(/[[:space:]]+#.*$/, "", line) }
            line == tbl_dq || line == tbl_sq { in_table=1; print; next }
            in_table && /^[[:space:]]*\[/ { if (!done) print "  SystemdCgroup = true"; done=1; in_table=0 }
            in_table && line ~ /^["'"'"'"]?SystemdCgroup["'"'"'"]?[[:space:]]*=/ {
              sub(/=[[:space:]]*(true|false)/, "= true")
              done=1; print; next
            }
            { print }
            END { if (in_table && !done) print "  SystemdCgroup = true" }
          ' "${write_cfg}" > "${tmp}" && mv "${tmp}" "${write_cfg}"
        fi
      fi

      local eff_cfg; eff_cfg="$(_map_get eof_keys eof_vals "${cfg}")"
      [ -z "${eff_cfg}" ] && eff_cfg="${cfg}"
      local cfg_dir
      cfg_dir="$(dirname "${eff_cfg}")"
      local import_path resolved
      while IFS= read -r import_path; do
        while IFS= read -r resolved; do
          if [ -n "${resolved}" ]; then
            pending+=("${resolved}")
            _map_set pof_keys pof_vals "${resolved}" "${cfg}"
            _map_set riof_keys riof_vals "${resolved}" "${import_path}"
          fi
        done < <(_resolve_import_paths "${cfg_dir}" "${import_path}")
      done < <(_extract_imports "${eff_cfg}")
    done

    [ "${found_table}" -eq 1 ]
  }

  if ! _set_systemd_cgroup_true "${CONTAINERD_CONFIG_FILE}"; then
    if [ -s "${CONTAINERD_CONFIG_FILE}" ] && \
       [ "$(tail -c1 "${CONTAINERD_CONFIG_FILE}" | wc -l)" -eq 0 ]; then
      echo >> "${CONTAINERD_CONFIG_FILE}"
    fi
    cat >> "${CONTAINERD_CONFIG_FILE}" <<EOF
[plugins."${_cri_ns:-io.containerd.grpc.v1.cri}".containerd.runtimes.runc.options]
  SystemdCgroup = true
EOF
  fi
fi

if [ $IS_WINDOWS -eq 0 ]; then
  if ! _grep_config '^[[:space:]]*\[plugins\.["'"'"'](io\.containerd\.nri\.v1\.nri)["'"'"']\][[:space:]]*(#.*)?$' \
       "${CONTAINERD_CONFIG_FILE}"; then
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
