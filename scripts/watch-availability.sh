#!/usr/bin/env bash
#
# Continuously calls the API and visualizes availability: a green dot per
# successful request, the status code in red for any failure. Press Ctrl-C
# for a summary. Useful for watching a rolling restart or pod deletion.
#
# Usage: ./scripts/watch-availability.sh [BASE_URL]   (default http://localhost:8080)

set -uo pipefail

BASE_URL=${1:-http://localhost:8080}
total=0
failed=0

summary() {
  printf '\n\n%d requests, %d failed\n' "$total" "$failed"
  exit 0
}
trap summary INT TERM

echo "Calling $BASE_URL/todos every 100 ms (Ctrl-C to stop)"
while true; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$BASE_URL/todos?limit=1") || code=000
  total=$((total + 1))
  if [[ $code == 200 ]]; then
    printf '\033[32m.\033[0m'
  else
    failed=$((failed + 1))
    printf '\033[31m %s \033[0m' "$code"
  fi
  sleep 0.1
done
