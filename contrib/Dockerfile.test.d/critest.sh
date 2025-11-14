#!/usr/bin/env bash

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
