#!/usr/bin/env bash
#
# End-to-end smoke test for a running todo-api deployment.
#
#   1. CRUD happy path and error responses
#   2. Consistency: every replica sees the same data (shared PostgreSQL)
#   3. Failure drills under continuous load:
#        a. delete an API pod       -> expect zero failed requests
#        b. rolling restart         -> expect zero failed requests
#        c. restart PostgreSQL      -> expect data to survive
#
# Usage:  ./scripts/smoke.sh                 (or: make smoke)
# Env:    BASE_URL    API base URL           (default http://localhost:8080)
#         KUBECTL     kubectl command        (default "kubectl")
#         NAMESPACE   Kubernetes namespace   (default todo)
#         SKIP_DRILLS set to 1 to run only the API checks (no kubectl needed)
# Needs:  curl, jq

set -euo pipefail

BASE_URL=${BASE_URL:-http://localhost:8080}
KUBECTL=${KUBECTL:-kubectl}
NAMESPACE=${NAMESPACE:-todo}
SKIP_DRILLS=${SKIP_DRILLS:-0}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"; jobs -p | xargs -r kill 2>/dev/null || true' EXIT

passed=0
failed=0
green() { printf '\033[32m%s\033[0m\n' "$*"; }
red()   { printf '\033[31m%s\033[0m\n' "$*"; }
step()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()    { passed=$((passed + 1)); green "  ✓ $*"; }
bad()   { failed=$((failed + 1)); red   "  ✗ $*"; }
k()     { $KUBECTL -n "$NAMESPACE" "$@"; }

# request METHOD PATH [JSON_BODY] -> sets $status and $body
request() {
  local method=$1 path=$2 data=${3:-}
  local args=(-s -o "$TMP/body" -w '%{http_code}' -X "$method" --max-time 5)
  [[ -n $data ]] && args+=(-H 'Content-Type: application/json' -d "$data")
  status=$(curl "${args[@]}" "$BASE_URL$path" || echo 000)
  body=$(cat "$TMP/body" 2>/dev/null || true)
}

expect_status() {
  local want=$1 desc=$2
  if [[ $status == "$want" ]]; then ok "$desc ($status)"; else bad "$desc: got $status, want $want - $body"; fi
}

expect_jq() {
  local filter=$1 desc=$2
  if jq -e "$filter" >/dev/null 2>&1 <<<"$body"; then ok "$desc"; else bad "$desc: $filter failed on $body"; fi
}

# load_start: hammer GET /todos until load_stop, recording status codes.
load_start() {
  : >"$TMP/load"
  (
    while [[ ! -f $TMP/stop ]]; do
      curl -s -o /dev/null -w '%{http_code}\n' --max-time 3 "$BASE_URL/todos?limit=1" >>"$TMP/load" || echo 000 >>"$TMP/load"
      sleep 0.05
    done
  ) &
  load_pid=$!
}

load_stop() {
  touch "$TMP/stop"
  wait "$load_pid" 2>/dev/null || true
  rm -f "$TMP/stop"
  local total errors
  total=$(wc -l <"$TMP/load" | tr -d ' ')
  errors=$(grep -cv '^200$' "$TMP/load" || true)
  if [[ $errors -eq 0 ]]; then
    ok "$1: $total requests, 0 failed"
  else
    bad "$1: $errors of $total requests failed (codes: $(grep -v '^200$' "$TMP/load" | sort | uniq -c | tr -s ' ' | tr '\n' ',' ))"
  fi
}

wait_ready() {
  for _ in $(seq 1 60); do
    request GET /readyz
    [[ $status == 200 ]] && return 0
    sleep 1
  done
  red "service at $BASE_URL did not become ready"; exit 1
}

# ---------------------------------------------------------------------------
step "Waiting for $BASE_URL to be ready"
wait_ready
ok "readiness probe"

step "CRUD happy path"
request POST /todos '{"title":"  smoke test todo  "}'
expect_status 201 "create"
expect_jq '.title == "smoke test todo" and .completed == false' "title trimmed, completed defaults to false"
id=$(jq -r .id <<<"$body")

request GET "/todos/$id"
expect_status 200 "get by id"

request PUT "/todos/$id" '{"title":"smoke test todo (done)","completed":true}'
expect_status 200 "replace"
expect_jq '.completed == true and .updated_at > .created_at' "replace updates fields and updated_at"

request PATCH "/todos/$id" '{"completed":false}'
expect_status 200 "partial update (PATCH)"
expect_jq '.completed == false and .title == "smoke test todo (done)"' "PATCH changes only the given field"
request PATCH "/todos/$id" '{"completed":true}'

request GET "/todos?limit=5&offset=0"
expect_status 200 "list"
expect_jq '.limit == 5 and .offset == 0 and (.items | type == "array") and .total >= 1' "list envelope with pagination"

request GET "/todos?completed=true&limit=100"
expect_status 200 "list filtered by completed=true"
expect_jq '(.items | length) >= 1 and all(.items[]; .completed == true)' "filter returns only completed todos"

step "Error handling"
request POST /todos '{"title":""}'
expect_status 422 "blank title rejected"
expect_jq '.errors[0].field == "title"' "problem+json lists the invalid field"
request POST /todos '{"title":'
expect_status 400 "malformed JSON rejected"
request GET /todos/00000000-0000-0000-0000-000000000000
expect_status 404 "unknown id"
request GET "/todos?limit=1000"
expect_status 422 "limit above maximum"
request PATCH "/todos/$id" '{"title":null}'
expect_status 422 "PATCH null title rejected"

step "Consistency across replicas (shared PostgreSQL)"
request POST /todos '{"title":"consistency probe"}'
probe_id=$(jq -r .id <<<"$body")
seen=0
for _ in $(seq 1 20); do
  request GET "/todos/$probe_id"
  [[ $status == 200 ]] && seen=$((seen + 1))
done
if [[ $seen -eq 20 ]]; then ok "20/20 load-balanced reads found the new todo"; else bad "only $seen/20 reads found the new todo"; fi

if [[ $SKIP_DRILLS != 1 ]]; then
  step "Drill A: delete one API pod under load"
  victim=$(k get pods -l app.kubernetes.io/name=todo-api -o jsonpath='{.items[0].metadata.name}')
  load_start
  sleep 1
  k delete pod "$victim" --wait=false >/dev/null
  echo "  deleted $victim; waiting for replacement..."
  sleep 3
  k rollout status deployment/todo-api --timeout=120s >/dev/null
  sleep 2
  load_stop "pod deletion"

  step "Drill B: rolling restart under load"
  load_start
  sleep 1
  k rollout restart deployment/todo-api >/dev/null
  k rollout status deployment/todo-api --timeout=180s >/dev/null
  sleep 2
  load_stop "rolling restart"

  step "Drill C: restart PostgreSQL, data must survive"
  k delete pod postgres-0 >/dev/null
  k rollout status statefulset/postgres --timeout=180s >/dev/null
  wait_ready
  request GET "/todos/$id"
  expect_status 200 "todo created before the DB restart still exists"
  expect_jq '.title == "smoke test todo (done)"' "data intact after DB restart"
fi

step "Cleanup"
for del in "$id" "$probe_id"; do
  request DELETE "/todos/$del"
  expect_status 204 "delete $del"
done
request GET "/todos/$id"
expect_status 404 "deleted todo is gone"

printf '\n'
if [[ $failed -eq 0 ]]; then
  green "All $passed checks passed."
else
  red "$failed of $((passed + failed)) checks failed."
  exit 1
fi
