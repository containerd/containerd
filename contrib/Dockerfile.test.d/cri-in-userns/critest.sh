#!/usr/bin/env bash

cat > /etc/systemd/system/critest.service << EOF
[Unit]
Description=critest script

[Service]
Type=simple
ExecStart=/bin/bash /docker-entrypoint.sh
ExecStop=/bin/sh -c 'sleep 10 && /bin/systemctl poweroff'
StandardOutput=/dev/stdout
StandardError=/dev/stderr
[Install]
WantedBy=default.target
EOF
systemctl enable critest.service
# shellcheck disable=SC2093
journalctl -f &
exec /lib/systemd/systemd