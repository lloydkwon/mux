#!/usr/bin/env bash
# mux 가 tmux 에 대해 "이렇게 동작한다"고 가정하고 있는 것들을 실제로 확인한다.
#
# tmux 는 apt/brew 로 조용히 올라가고, 여기 적힌 것들이 바뀌면 mux 는 에러 없이
# 이상해진다 — 패널이 안 열리거나, 파싱이 어긋나거나, 훅이 헛돈다. 그래서 버전이
# 바뀔 때마다 사람이 기억해 내는 대신 이 스크립트를 돌린다.
#
#   scripts/tmux-assumptions.sh                  # PATH 의 tmux 로
#   scripts/tmux-assumptions.sh -v               # 실패 항목을 더 자세히
#   TMUX_BIN=/path/to/tmux scripts/...           # 특정 바이너리로
#
# TMUX_BIN 이 있는 이유: 올리기 *전에* 확인하려는 것이다. 새 tmux 를 PATH 에
# 올리는 순간 클라이언트만 새 버전이 되고 돌고 있는 서버는 옛 버전이라
# 프로토콜이 어긋나 모든 tmux 호출이 거부된다 — 서버를 재시작해야 풀리고,
# 그건 열려 있는 작업을 전부 끊는 일이다. 후보 바이너리를 PATH 밖에 두고
# 여기에만 물려 보면 그 대가를 치르기 전에 답을 얻는다.
#
# 검사 대상은 mux 소스가 실제로 실행하는 명령과 파싱하는 포맷이다. 새 가정을
# 코드에 넣으면 여기에도 한 줄 넣는다 — 안 그러면 다음 업그레이드 때 그 하나만
# 조용히 깨진다.
#
# 전용 소켓(-L)과 -f /dev/null 로 돈다. 사용자의 서버에 붙으면 훅이 패널을 만들고
# 세션을 건드리므로, 격리는 편의가 아니라 요구사항이다.

set -uo pipefail

SOCKET="mux-assume-$$"
VERBOSE=${1:-}
TMUX_BIN=${TMUX_BIN:-tmux}
FAILED=0
CHECKED=0

t() { "$TMUX_BIN" -L "$SOCKET" -f /dev/null "$@"; }

# kill-server 는 서버만 죽이고 소켓 파일은 남긴다 — 돌릴 때마다 /tmp 에 하나씩
# 쌓이므로 파일까지 지운다.
cleanup() {
	"$TMUX_BIN" -L "$SOCKET" kill-server 2>/dev/null || true
	rm -f "${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)/$SOCKET"
}
trap cleanup EXIT

# ok <설명> <기대> <실제>
ok() {
	CHECKED=$((CHECKED + 1))
	if [ "$2" = "$3" ]; then
		printf '  ok    %s\n' "$1"
	else
		FAILED=$((FAILED + 1))
		printf '  FAIL  %s\n        기대: %s\n        실제: %s\n' "$1" "$2" "$3"
	fi
}

# okc <설명> <실제가 포함해야 할 것> <실제>
okc() {
	CHECKED=$((CHECKED + 1))
	case "$3" in
	*"$2"*) printf '  ok    %s\n' "$1" ;;
	*)
		FAILED=$((FAILED + 1))
		printf '  FAIL  %s\n        포함해야: %s\n        실제: %s\n' "$1" "$2" "$3"
		;;
	esac
}

if ! command -v "$TMUX_BIN" >/dev/null 2>&1 && [ ! -x "$TMUX_BIN" ]; then
	echo "tmux 를 찾을 수 없습니다: $TMUX_BIN" >&2
	exit 2
fi
VERSION=$("$TMUX_BIN" -V)
echo "$VERSION 로 검사합니다 (소켓 $SOCKET)"
echo

# ── 1. 버전 파싱 ─────────────────────────────────────────────────────────────
# tmux/popup.go 의 getTmuxVersion 이 "tmux 3.4" / "tmux 3.2a" 를 파싱해 3.2 미만이면
# 팝업 모드를 거절한다. 이 문자열 모양이 바뀌면 팝업이 통째로 막힌다.
echo "버전"
okc "$TMUX_BIN -V 가 'tmux <숫자>' 로 시작한다" "tmux " "$VERSION"
NUM=$(printf '%s' "$VERSION" | sed 's/^tmux //; s/[a-z-].*$//')
ok "3.2 이상 (display-popup 요구사항)" "yes" \
	"$(awk -v v="$NUM" 'BEGIN{print (v+0 >= 3.2) ? "yes" : "no"}')"

t new-session -d -s a -c /tmp -x 200 -y 40
t new-session -d -s b -c /tmp -x 200 -y 40
PANE=$(t list-panes -t a -F '#{pane_id}' | head -1)
# 창은 ID 로 잡는다. 이 서버는 -f /dev/null 로 뜨므로 base-index 가 사용자 설정이
# 아니라 tmux 기본값이고, a:1 같은 인덱스 표기는 여기서 존재하지 않는다.
WIN=$(t display-message -p -t "$PANE" '#{window_id}')

# ── 2. 세션 목록 포맷 ────────────────────────────────────────────────────────
# tmux/session.go 의 listFormat. parseLine 이 '|' 로 11칸을 기대하고, 마지막이
# @project_dir 이라 비어 있어도 칸은 있어야 한다.
echo
echo "세션 목록 (tmux/session.go listFormat)"
LF='#{session_name}|#{session_windows}|#{session_created}|#{session_attached}|#{pane_current_path}|#{session_activity}|#{pane_current_command}|#{pane_pid}|#{window_index}|#{pane_index}|#{@project_dir}'
LINE=$(t list-sessions -F "$LF" | head -1)
ok "필드 11칸" "11" "$(printf '%s' "$LINE" | awk -F'|' '{print NF}')"
ok "첫 칸이 세션 이름" "a" "$(printf '%s' "$LINE" | cut -d'|' -f1)"

# ── 3. 사용자 옵션 ───────────────────────────────────────────────────────────
# @project_dir / @mux_panel_off / @mux_panel_min_width 이 전부 이 규칙에 얹혀 있다.
echo
echo "사용자 옵션 (@...)"
ok "없는 옵션은 빈 문자열, 에러 아님" "0:" "$(t show-options -qv -t a @nope >/dev/null 2>&1; printf '%s:%s' "$?" "$(t show-options -qv -t a @nope 2>/dev/null)")"
t set-option -t a @project_dir /tmp/proj
ok "세션 옵션 읽기" "/tmp/proj" "$(t show-options -qv -t a @project_dir)"
ok "세션 옵션이 pane 대상 포맷에서 해석된다" "/tmp/proj" \
	"$(t display-message -p -t "$PANE" '#{@project_dir}')"
ok "list-sessions 포맷에서도 해석된다" "/tmp/proj" \
	"$(t list-sessions -F '#{@project_dir}' -f '#{==:#{session_name},a}')"
ok "다른 세션에는 새지 않는다" "" "$(t show-options -qv -t b @project_dir)"
t set-option -w -t "$WIN" @mux_panel_off 1
ok "창 옵션 -w 쓰기/읽기" "1" "$(t show-options -wqv -t "$WIN" @mux_panel_off)"
t set-option -wu -t "$WIN" @mux_panel_off
ok "창 옵션 -wu 로 해제" "" "$(t show-options -wqv -t "$WIN" @mux_panel_off)"

# ── 4. pane 조회 포맷 ────────────────────────────────────────────────────────
# panelWindow / findPanelPane / WindowShape 이 이 세 포맷의 모양에 의존한다.
echo
echo "pane 조회 포맷 (tmux/panel.go)"
WD=$(t display-message -p -t "$PANE" '#{window_id} #{pane_current_path}')
ok "'<window_id> <path>' 이고 @ 로 시작" "yes" \
	"$(case "$WD" in @*' '/*) echo yes ;; *) echo no ;; esac)"
WS=$(t display-message -p -t "$PANE" '#{window_width} #{window_panes}')
ok "'<width> <panes>' 가 숫자 둘" "200 1" "$WS"

# ── 5. split-window 로 정확한 폭 ─────────────────────────────────────────────
# 패널은 열릴 때 폭을 -l 로 지정한다. 나중에 리사이즈하면 잘못된 크기로 한 번
# 깜빡이기 때문이다. -d 는 포커스를 원래 pane 에 남긴다.
echo
echo "패널 열기 (split-window)"
BEFORE=$(t display-message -p -t a '#{pane_id}')
t split-window -d -f -h -l 48 -c /tmp -t "$WIN"
NEW=$(t list-panes -t "$WIN" -F '#{pane_id} #{pane_width}'  | tail -1)
ok "-l 48 이 정확히 48칸" "48" "$(printf '%s' "$NEW" | awk '{print $2}')"
ok "-d 라서 활성 pane 이 그대로" "$BEFORE" "$(t display-message -p -t a '#{pane_id}')"

# ── 6. pane 시작 명령 ────────────────────────────────────────────────────────
# 패널을 알아보는 유일한 표식이다 (tmux/panel.go panelCommand). 마커를 따로 두지
# 않는 이유가 tmux 가 이걸 기억해 주기 때문이라, 사라지면 패널을 못 찾는다.
echo
echo "pane 시작 명령 (패널 식별)"
t split-window -d -t "$WIN" 'sleep 300' 
okc "list-panes 가 시작 명령을 돌려준다" "sleep 300" \
	"$(t list-panes -t "$WIN" -F '#{pane_id} #{pane_start_command}'  | tr '\n' ' ')"
okc "list-panes -a 로 서버 전체 조회" "sleep 300" \
	"$(t list-panes -a -F '#{pane_start_command}' | tr '\n' ' ')"

# ── 7. 배치 캡처 ─────────────────────────────────────────────────────────────
# tmux/screen.go 는 여러 pane 의 화면을 한 번의 fork 로 가져온다 (세 번 호출 대비
# 4ms 대 12ms). 명령 목록과 구분자 출력이 이 구조의 전제다.
echo
echo "배치 캡처 (tmux/screen.go)"
SEP='@@@mux-capture-sep@@@'
BATCH=$(t capture-pane -p -t "$PANE" ';' display-message -p "$SEP" ';' capture-pane -p -t "$PANE")
ok "한 번의 호출에 구분자가 정확히 1개" "1" "$(printf '%s\n' "$BATCH" | grep -c "^$SEP$")"
# -J 는 접힌 줄을 이어 붙인다. 없으면 좁은 pane 에서 긴 경로가 두 줄로 쪼개진다.
t capture-pane -p -J -S -5 -t "$PANE" >/dev/null 2>&1
ok "capture-pane -J (접힌 줄 잇기) 를 받는다" "0" "$?"

# ── 8. 훅과 run-shell 의 포맷 확장 ───────────────────────────────────────────
# `bind a run-shell "... -t #{pane_id}"` 와 패널 훅 전체가 여기 달려 있다.
# run-shell 이 포맷을 확장하지 않으면 mux 는 리터럴 "#{pane_id}" 를 받는다.
echo
echo "훅 / run-shell 포맷 확장"
OUT=$(mktemp); t run-shell "echo '#{pane_id}' > $OUT"
for _ in $(seq 1 20); do [ -s "$OUT" ] && break; sleep 0.1; done
okc "run-shell 이 #{pane_id} 를 확장한다" "%" "$(cat "$OUT")"

t set-hook -g after-new-session "run-shell \"echo hook:'#{pane_id}' > $OUT\""
: >"$OUT"; t new-session -d -s hooked -c /tmp
for _ in $(seq 1 20); do [ -s "$OUT" ] && break; sleep 0.1; done
okc "after-new-session 훅이 실제로 뜬다" "hook:%" "$(cat "$OUT")"
rm -f "$OUT"

# ── 9. 클라이언트 조회 ───────────────────────────────────────────────────────
# SessionOnlyInVSCode 가 client_pid 로 /proc 을 읽는다. 붙은 클라이언트가 없으면
# 빈 출력이어야 하고, 그걸 mux 는 "판단 불가"로 읽는다.
echo
echo "클라이언트 조회 (VS Code 판별)"
ok "붙은 클라이언트가 없으면 빈 출력" "" "$(t list-clients -t a -F '#{client_pid}')"

echo
if [ "$FAILED" -eq 0 ]; then
	echo "$CHECKED 개 전부 통과 — $VERSION 에서 mux 의 가정이 유지됩니다."
	exit 0
fi
echo "$CHECKED 개 중 $FAILED 개 실패 — $VERSION 에서 동작이 바뀌었습니다."
echo "해당 항목이 걸린 코드를 확인하세요. 주석에 왜 그렇게 가정했는지 적혀 있습니다."
[ "$VERBOSE" = "-v" ] || echo "(-v 로 다시 돌리면 더 자세히 나옵니다)"
exit 1
