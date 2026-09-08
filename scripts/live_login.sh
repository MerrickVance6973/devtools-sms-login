#!/usr/bin/env bash
# End-to-end pass against a running otpd: text a code, then verify what you got.
set -euo pipefail

: "${LOGIN_PHONE:?set LOGIN_PHONE, e.g. export LOGIN_PHONE=+15551230000}"
BASE="${OTPD_BASE:-http://localhost:8080}"

echo "requesting a code for $LOGIN_PHONE"
curl -sS -X POST "$BASE/login/start" \
  -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$LOGIN_PHONE\",\"request_id\":\"release-$(date +%s)\"}"
echo

read -r -p "code from the SMS: " CODE
curl -sS -X POST "$BASE/login/check" \
  -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$LOGIN_PHONE\",\"code\":\"$CODE\"}"
echo
