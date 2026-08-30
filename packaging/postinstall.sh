#!/bin/sh
# The unit is installed but never enabled or started, the operator
# configures /etc/beanstore/config.yaml first.
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi
