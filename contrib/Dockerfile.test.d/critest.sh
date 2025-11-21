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



cat > /etc/systemd/system/critest.service << EOF
[Unit]
Description=critest script
[Service]
Type=simple
Environment="IS_SYSTEMD_CGROUP=true"
RemainAfterExit=yes
ExecStart=/bin/bash /docker-entrypoint.sh
StandardOutput=/dev/stdout
StandardError=/dev/stderr
[Install]
WantedBy=default.target
EOF


function echo_exit_code() {
    sleep 30
    log_str=`systemctl status critest.service|grep "SUCCESS!"`
    if [ -z "$log_str" ]; then
        echo 1 > /tmp/critest_exit_code.txt
        /bin/systemctl poweroff
    fi
    failed_count=$(echo "$log_str" | awk '{for(i=1;i<=NF;i++) if($i=="Failed") {print $(i-1); exit}}')
    if [ "$failed_count" -gt 0 ]; then
        echo 1 > /tmp/critest_exit_code.txt
    else
        echo 0 > /tmp/critest_exit_code.txt
    fi
    /bin/systemctl poweroff
}

function start(){
  systemctl enable critest.service
  journalctl -f &
  exec /lib/systemd/systemd
}

case $1 in
    start)
      start
      ;;
    exit)
      echo_exit_code
      ;;
esac
