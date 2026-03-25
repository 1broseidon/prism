#!/bin/bash
# Tool: check-dns — resolve a hostname to IP addresses.
#
# Contract:
#   stdin  → {"hostname": "example.com"}
#   stdout → resolved IP addresses
#   exit 0 → success
#   exit 1 → error (stderr)
#
# Usage with prism-bridge:
#   prism-bridge tool --manifest examples/tools/check-dns.json \
#     --port 3001 -- bash examples/tools/check-dns.sh

set -euo pipefail

input=$(cat)
hostname=$(echo "$input" | grep -o '"hostname":"[^"]*"' | cut -d'"' -f4)

if [ -z "$hostname" ]; then
  echo "hostname is required" >&2
  exit 1
fi

getent hosts "$hostname" | awk '{print $1}' | sort -u
