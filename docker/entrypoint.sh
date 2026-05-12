#!/bin/sh
set -e

chown -R appuser:appuser /app/data 2>/dev/null || true
chown -R appuser:appuser /opt/stacks 2>/dev/null || true

exec su-exec appuser "$@"
