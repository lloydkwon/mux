#!/usr/bin/env bash
# mux 가 herdr 에 대해 "이렇게 동작한다"고 가정하고 있는 것들을 실제로 확인한다.
#
# herdr 는 스스로 업데이트하고(`herdr update`), 포맷은 사설·비버전이다. 여기 적힌
# 것이 바뀌면 mux --herdr 는 에러 없이 이상해진다 — 배지가 사라지거나, 나이
# 컬럼이 비거나, 목록이 통째로 빈다. 그래서 버전이 바뀔 때마다 이걸 돌린다.
#
#   scripts/herdr-assumptions.sh                     # PATH 의 herdr 로
#   MUX_HERDR_BIN=/path/to/herdr scripts/...         # 특정 바이너리로
#
# tmux-assumptions.sh 와 달리 «격리 서버를 띄우지 않는다». herdr 는 사용자의
# 워크스페이스가 곧 서버이고 그것을 새로 만들 방법이 -L 처럼 싸지 않다. 대신
# 여기 있는 것은 전부 읽기다 — 아무것도 만들지 않고 아무것도 바꾸지 않는다.
# 그러므로 돌리는 쪽은 herdr 안이어야 한다.

set -uo pipefail

HERDR_BIN=${MUX_HERDR_BIN:-herdr}
VERBOSE=${1:-}
FAILED=0
CHECKED=0

ok() {
	CHECKED=$((CHECKED + 1))
	if [ "$2" = "$3" ]; then
		printf '  ok    %s\n' "$1"
	else
		FAILED=$((FAILED + 1))
		printf '  FAIL  %s\n        기대: %s\n        실제: %s\n' "$1" "$2" "$3"
	fi
}

okc() {
	CHECKED=$((CHECKED + 1))
	case "$3" in
	*"$2"*) printf '  ok    %s\n' "$1" ;;
	*)
		FAILED=$((FAILED + 1))
		printf '  FAIL  %s\n        포함해야 함: %s\n' "$1" "$2"
		[ "$VERBOSE" = "-v" ] && printf '        실제: %s\n' "$3"
		;;
	esac
}

if ! command -v "$HERDR_BIN" >/dev/null 2>&1 && [ ! -x "$HERDR_BIN" ]; then
	echo "herdr 를 찾을 수 없습니다 ($HERDR_BIN). MUX_HERDR_BIN 으로 지정하세요."
	exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
	echo "jq 가 필요합니다."
	exit 1
fi
if [ "${HERDR_ENV:-}" != "1" ]; then
	echo "herdr 안에서 돌려야 합니다 (HERDR_ENV=1 이 아닙니다)."
	exit 1
fi

VERSION=$("$HERDR_BIN" --version 2>/dev/null)
echo "herdr: $VERSION"
echo

WS=$("$HERDR_BIN" workspace list 2>/dev/null)
PANES=$("$HERDR_BIN" pane list 2>/dev/null)
AGENTS=$("$HERDR_BIN" agent list 2>/dev/null)

echo "1. 봉투 — herdr/client.go 의 call() 이 이 모양을 판다"
ok "workspace list 가 result 를 준다" "true" \
	"$(printf '%s' "$WS" | jq 'has("result")')"
ok "result.type 이 workspace_list 다" "workspace_list" \
	"$(printf '%s' "$WS" | jq -r '.result.type')"
ok "pane list 의 배열 키가 panes 다" "true" \
	"$(printf '%s' "$PANES" | jq '.result | has("panes")')"
ok "agent list 의 배열 키가 agents 다" "true" \
	"$(printf '%s' "$AGENTS" | jq '.result | has("agents")')"

echo
echo "2. 워크스페이스 필드 — herdr/session.go 의 sessionsFrom 이 읽는 것"
for f in workspace_id number label focused pane_count agent_status; do
	ok "workspace 에 $f 가 있다" "true" \
		"$(printf '%s' "$WS" | jq --arg f "$f" 'all(.result.workspaces[]; has($f))')"
done
# git_branch 는 git 저장소가 아닌 워크스페이스에는 없다 — 없어도 되는 것이 맞다.
okc "적어도 한 워크스페이스가 git_branch 를 싣는다" "true" \
	"$(printf '%s' "$WS" | jq 'any(.result.workspaces[]; has("git_branch"))')"

echo
echo "3. 워크스페이스에 «없는» 것 — Created 를 합성해야 하는 이유"
# 이게 생기면 createdFor 의 합성을 걷어내고 진짜 시각을 쓸 수 있다.
ok "workspace 에 cwd 가 없다" "false" \
	"$(printf '%s' "$WS" | jq 'any(.result.workspaces[]; has("cwd"))')"
ok "workspace 에 created 가 없다" "false" \
	"$(printf '%s' "$WS" | jq 'any(.result.workspaces[]; has("created"))')"
ok "workspace 에 activity 가 없다" "false" \
	"$(printf '%s' "$WS" | jq 'any(.result.workspaces[]; has("activity"))')"

echo
echo "4. pane 필드와 채번 — windowsFrom 이 Window.Index 를 여기서 뽑는다"
for f in pane_id workspace_id tab_id focused; do
	ok "pane 에 $f 가 있다" "true" \
		"$(printf '%s' "$PANES" | jq --arg f "$f" 'all(.result.panes[]; has($f))')"
done
ok "pane_id 가 w<id>:p<n> 꼴이다" "true" \
	"$(printf '%s' "$PANES" | jq 'all(.result.panes[]; .pane_id | test("^[A-Za-z0-9]+:p[0-9]+$"))')"
# pane 번호가 워크스페이스 스코프라는 것이 탭 계층을 버릴 수 있었던 근거다.
ok "한 워크스페이스 안에서 pane 번호가 유일하다" "true" \
	"$(printf '%s' "$PANES" | jq '[.result.panes[] | .pane_id] | (length == (unique | length))')"

echo
echo "5. 에이전트 상태 — mapState 가 이 다섯 개만 안다"
ok "agent_status 가 아는 값만 온다" "true" \
	"$(printf '%s' "$PANES" | jq '[.result.panes[].agent_status] | all(. as $s | ["idle","working","blocked","done","unknown"] | index($s) != null)')"
okc "적어도 한 pane 이 state_changed_at_unix_ms 를 싣는다" "true" \
	"$(printf '%s' "$PANES" | jq 'any(.result.panes[]; has("state_changed_at_unix_ms"))')"
# 초가 아니라 밀리초라는 것: 초로 읽으면 나이가 1970 년대로 나온다.
ok "state_changed_at_unix_ms 가 밀리초다" "true" \
	"$(printf '%s' "$PANES" | jq --argjson now "$(date +%s)" '[.result.panes[] | select(has("state_changed_at_unix_ms")) | .state_changed_at_unix_ms] | all(. > ($now * 500))')"

echo
echo "6. pane 과 agent 의 조인 — 이름은 agent 목록에만 있다"
ok "agent 는 pane 의 부분집합이다" "true" \
	"$(jq -n --argjson p "$PANES" --argjson a "$AGENTS" \
		'[$a.result.agents[].pane_id] - [$p.result.panes[].pane_id] | length == 0')"
ok "agent 마다 조인 키(pane_id)가 있다" "true" \
	"$(printf '%s' "$AGENTS" | jq 'all(.result.agents[]; has("pane_id"))')"
# 이름 없는 agent 가 실제로 나온다는 것이 windowName 의 폴백 체인이 있는 이유다.
# 세는 것이지 단언이 아니다 — 전부 이름이 있는 순간이 있을 수 있고, 그건 폴백을
# 걷어내도 된다는 뜻이 아니다.
printf '  info  이름 없는 agent: %s / %s\n' \
	"$(printf '%s' "$AGENTS" | jq '[.result.agents[] | select(has("name") | not)] | length')" \
	"$(printf '%s' "$AGENTS" | jq '.result.agents | length')"

echo
echo "7. agent_session — kind 가 id 일 때만 세션 식별자다"
ok "agent_session.kind 는 id 아니면 path 다" "true" \
	"$(printf '%s' "$AGENTS" | jq '[.result.agents[] | select(has("agent_session")) | .agent_session.kind] | all(. == "id" or . == "path")')"

echo
echo "8. pane read — 이 명령만 JSON 봉투가 아니다"
FIRST_PANE=$(printf '%s' "$PANES" | jq -r '.result.panes[0].pane_id')
READ_OUT=$("$HERDR_BIN" pane read "$FIRST_PANE" --source visible --format ansi 2>/dev/null | head -c 400)
ok "pane read 는 화면 텍스트를 그대로 낸다 (JSON 아님)" "no" \
	"$(printf '%s' "$READ_OUT" | jq -e . >/dev/null 2>&1 && echo yes || echo no)"

echo
if [ "$FAILED" -eq 0 ]; then
	echo "$CHECKED 개 전부 통과 — $VERSION 에서 mux 의 가정이 유지됩니다."
	exit 0
fi
echo "$CHECKED 개 중 $FAILED 개 실패 — $VERSION 에서 동작이 바뀌었습니다."
echo "herdr/ 패키지의 주석에 왜 그렇게 가정했는지 적혀 있습니다."
[ "$VERBOSE" = "-v" ] || echo "(-v 로 다시 돌리면 더 자세히 나옵니다)"
exit 1
