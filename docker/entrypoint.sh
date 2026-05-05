#!/bin/sh
set -e

chown appuser:appuser /app/data 2>/dev/null || true
chown appuser:appuser /opt/stacks 2>/dev/null || true

exec su-exec appuser "$@"
