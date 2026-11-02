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
# tmux/session.go 의 listFormat. parseLine 은 최소 11칸을 요구하고 12칸까지 읽으며,
# 마지막이 @mux_note 라 비어 있어도 칸은 있어야 한다. 메모가 맨 뒤인 것은 그 값만
# 사용자가 직접 치는 것이라, SplitN 의 잔여값 자리여야 '|' 가 들어가도 안전하기
# 때문이다.
echo
echo "세션 목록 (tmux/session.go listFormat)"
LF='#{session_name}|#{session_windows}|#{session_created}|#{session_attached}|#{pane_current_path}|#{session_activity}|#{pane_current_command}|#{pane_pid}|#{window_index}|#{pane_index}|#{@project_dir}|#{@mux_note}'
LINE=$(t list-sessions -F "$LF" | head -1)
ok "필드 12칸" "12" "$(printf '%s' "$LINE" | awk -F'|' '{print NF}')"
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
# @mux_note. 세션 메모는 이 왕복 하나에 전부 얹혀 있다 — 쓰고, listFormat 으로
# 돌아오고, 지우면 사라진다. 파이프가 든 메모까지 확인하는 이유는 그게 마지막
# 필드여서 안전하다는 것이 parseLine 의 전제이기 때문이다.
t set-option -t a @mux_note '라벨링 후 진행 | 2차'
ok "세션 메모 쓰기/읽기" "라벨링 후 진행 | 2차" "$(t show-options -qv -t a @mux_note)"
ok "메모가 list-sessions 포맷 마지막 칸으로 온다" "라벨링 후 진행 | 2차" \
	"$(t list-sessions -F "$LF" -f '#{==:#{session_name},a}' | cut -d'|' -f12-)"
t set-option -ut a @mux_note
ok "세션 옵션 -ut 로 해제" "" "$(t show-options -qv -t a @mux_note)"
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

# ── 10. 서버 특정과 소켓 되살리기 ────────────────────────────────────────────
# tmux/revive.go 전체가 여기 달려 있다. 2026-09-01 /tmp 이 비워지며 소켓이
# unlink 됐고, 서버는 살아 있는데 아무도 닿을 수 없어 세션 7개가 통째로 사라진
# 것처럼 보였다. man 이 문서화한 SIGUSR1 복구가 그 답인데, 이 세 가지가 전부
# 실제로 그렇게 동작해야 성립한다.
echo
echo "서버 특정과 소켓 되살리기"
SOCK="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)/$SOCKET"
SPID=$(t display-message -p '#{pid}')
ok "#{pid} 가 살아 있는 프로세스" "yes" \
	"$(kill -0 "$SPID" 2>/dev/null && echo yes || echo no)"
ok "#{pid} 가 tmux 서버다 (comm 으로 확인)" "tmux: server" \
	"$(cat /proc/"$SPID"/comm 2>/dev/null)"
ok "#{socket_path} 가 실제 소켓 경로" "$SOCK" "$(t display-message -p '#{socket_path}')"

# 소켓을 지우면 서버는 살아 있어도 아무도 닿지 못한다 — 사고의 핵심.
rm -f "$SOCK"
t list-sessions >/dev/null 2>&1
ok "소켓이 없으면 호출이 실패한다" "no" \
	"$(t has-session -t a 2>/dev/null && echo yes || echo no)"
ok "그래도 서버는 살아 있다" "yes" \
	"$(kill -0 "$SPID" 2>/dev/null && echo yes || echo no)"

# man: "the SIGUSR1 signal may be sent to the tmux server process to recreate it".
kill -USR1 "$SPID" 2>/dev/null
for _ in $(seq 1 20); do [ -S "$SOCK" ] && break; sleep 0.1; done
ok "SIGUSR1 이 소켓을 다시 만든다" "yes" "$([ -S "$SOCK" ] && echo yes || echo no)"
ok "그리고 세션이 그대로 돌아온다" "yes" \
	"$(t has-session -t a 2>/dev/null && echo yes || echo no)"

# ── 11. 클라이언트 목록과 유령 분리 ──────────────────────────────────────────
# tmux/client.go 의 clientListFormat 과 StaleClients. 패널이 이 목록만 보고
# detach-client 를 부르므로, 여기서 어긋나면 «붙어 있는 터미널이 끊긴다» —
# 이 저장소에서 잘못됐을 때 가장 비싼 축에 드는 동작이다.
#
# #{client_activity} 를 유닉스 초로 읽는 것이 핵심 가정이다. #{t:client_activity}
# 는 "Thu Sep  3 18:58:18 2026" 처럼 서버 로케일에 따라 나와 파싱할 수 없다.
echo
echo "클라이언트 목록 (tmux/client.go)"

# 클라이언트가 있어야 답이 나오는 절이라 pty 를 하나 붙인다. script(1) 이 없으면
# 이 절만 건너뛴다 — 나머지 검사를 통째로 못 돌리게 만들 이유가 없다.
if command -v script >/dev/null 2>&1; then
	script -qfc "$TMUX_BIN -L $SOCKET -f /dev/null attach-session -t a" /dev/null \
		>/dev/null 2>&1 &
	SCRIPT_PID=$!
	for _ in $(seq 1 30); do
		[ -n "$(t list-clients -F '#{client_tty}' 2>/dev/null)" ] && break
		sleep 0.1
	done

	CF='#{client_tty}|#{client_width}|#{client_height}|#{client_session}|#{client_activity}'
	CLINE=$(t list-clients -F "$CF" | head -1)
	ok "필드 5칸" "5" "$(printf '%s' "$CLINE" | awk -F'|' '{print NF}')"
	okc "첫 칸이 tty 경로" "/dev/" "$(printf '%s' "$CLINE" | cut -d'|' -f1)"
	ok "폭이 숫자" "yes" \
		"$(printf '%s' "$CLINE" | awk -F'|' '{print ($2 ~ /^[0-9]+$/) ? "yes" : "no"}')"
	ok "붙은 세션 이름이 실린다" "a" "$(printf '%s' "$CLINE" | cut -d'|' -f4)"
	# 로케일 문자열이 아니라 유닉스 초여야 한다. 파싱에 실패하면 parseClientLine 이
	# 그 줄을 버리므로 조용히 «유령이 하나도 없다» 가 된다.
	# 반복 {9,} 은 쓰지 않는다 — mawk 1.3.4 는 그것을 몰라 값과 상관없이 «no» 였다
	# (2026-10-20 이 기계의 awk 가 gawk 에서 mawk 로 바뀐 뒤 드러났다).
	ok "client_activity 가 유닉스 초" "yes" \
		"$(printf '%s' "$CLINE" | awk -F'|' '{print ($5 ~ /^[0-9]+$/ && length($5) >= 9) ? "yes" : "no"}')"
	ok "그리고 최근이다 (10분 안)" "yes" \
		"$(printf '%s' "$CLINE" | awk -F'|' -v n="$(date +%s)" \
			'{print (n - $5 < 600) ? "yes" : "no"}')"

	# detach-client -t <tty> 가 그 클라이언트만 끊고 세션은 남긴다.
	TTY=$(printf '%s' "$CLINE" | cut -d'|' -f1)
	t detach-client -t "$TTY" 2>/dev/null
	for _ in $(seq 1 30); do
		[ -z "$(t list-clients -F '#{client_tty}' 2>/dev/null)" ] && break
		sleep 0.1
	done
	ok "detach-client -t <tty> 가 그 클라이언트를 끊는다" "" \
		"$(t list-clients -F '#{client_tty}' 2>/dev/null)"
	ok "세션은 살아 있다" "yes" \
		"$(t has-session -t a 2>/dev/null && echo yes || echo no)"
	kill "$SCRIPT_PID" 2>/dev/null || true
	wait "$SCRIPT_PID" 2>/dev/null || true
else
	echo "  SKIP  script(1) 이 없어 pty 를 못 붙였습니다"
fi

# ── 12. 복원 직후 활성 pane 바로잡기 ─────────────────────────────────────────
# tmux/resurrect.go 의 ResurrectPostRestore. resurrect 는 활성 pane 을
# `switch-client -t 세션:창` 뒤 `select-pane -t <번호>` 로 고르는데, continuum 이
# 서버 시작 때 돌리면 붙은 클라이언트가 없어 switch-client 가 실패하고 번호만 준
# select-pane 은 엉뚱한 창을 가리킨다 — 그래서 창마다 마지막에 만든 pane(옛 패널
# 자리)이 활성으로 남았다. mux 는 `=세션:창.pane` 전체 주소로 다시 고르는데, 그게
# 세션의 현재 창을 건드리지 않아야 복원된 «현재 창» 이 그대로다.
echo
echo "복원 직후 활성 pane (tmux/resurrect.go)"
t new-session -d -s r -x 100 -y 20
FIRST=$(t display-message -p -t r '#{window_index}')
t new-window -t r
SECOND=$(t display-message -p -t r '#{window_index}')
t split-window -t "=r:$SECOND"
t split-window -t "=r:$SECOND"
t select-window -t "=r:$FIRST"
t select-pane -t "=r:$SECOND.0"
ok "다른 창에 select-pane -t =세션:창.pane 해도 현재 창은 그대로" "$FIRST" \
	"$(t display-message -p -t r '#{window_index}')"
ok "그 창의 활성 pane 은 바뀐다" "0" "$(t display-message -p -t "=r:$SECOND" '#{pane_index}')"
ok "#{window_panes} 가 창의 pane 수" "3" "$(t display-message -p -t "=r:$SECOND.2" '#{window_panes}')"

# ── 13. mouse off 에서 패널 클릭이 닿는 조건 ─────────────────────────────────
# ui/watch.go 의 패널 클릭과 mouseOff 안내. `mouse off` 에서 tmux 는 바깥 터미널의
# 마우스 보고를 «활성 pane» 의 마우스 모드에 맞춰 켜고 끈다. 그래서 claude 처럼
# 마우스를 켜는 프로그램이 활성인 창에서만 패널 클릭이 도착하고, 셸이 활성인 창에서는
# 터미널이 클릭을 보내지 않아 패널이 아무것도 받지 못한다. 바깥으로 나가는 바이트를
# script(1) 의 기록으로 받아 `ESC[?1000h` 가 나가는지 본다.
echo
echo "mouse off 와 바깥 마우스 보고 (ui/watch.go 패널 클릭)"
if command -v script >/dev/null 2>&1; then
	# 3.8 부터 mouse 의 기본값이 on 이다(CHANGES 3.7c→3.8). -f /dev/null 로 도는 이 서버가 3.4 처럼
	# off 로 시작한다고 기대하면, 3.8 에서는 바깥 마우스 보고가 처음부터 켜져 있어 아래 단언이 헛돈다.
	t set -g mouse off
	t new-session -d -s m -x 100 -y 20 "sleep 300"
	t split-window -h -t m "printf '\033[?1000h\033[?1006h'; sleep 300"
	MOUSE_PANE=$(t display-message -p -t m '#{pane_id}')
	SHELL_PANE=$(t list-panes -t m -F '#{pane_id}' | grep -vx "$MOUSE_PANE")
	MLOG=$(mktemp)
	script -qfc "$TMUX_BIN -L $SOCKET -f /dev/null attach-session -t m" "$MLOG" \
		>/dev/null 2>&1 &
	MPID=$!
	for _ in $(seq 1 30); do
		[ -n "$(t list-clients -t m -F '#{client_tty}' 2>/dev/null)" ] && break
		sleep 0.1
	done
	sleep 0.5
	ON=$(printf '\033[?1000h')
	# mouse_on_after <명령…>: 명령 뒤에 바깥으로 «마우스 보고 켜기» 가 나갔는지.
	mouse_on_after() {
		local mark
		mark=$(wc -c <"$MLOG")
		"$@"
		sleep 0.5
		tail -c +$((mark + 1)) "$MLOG" | grep -qF -- "$ON" && echo yes || echo no
	}
	ok "mouse off, 셸 pane 이 활성이면 마우스 보고를 켜지 않는다" "no" \
		"$(mouse_on_after t select-pane -t "$SHELL_PANE")"
	ok "mouse off, 마우스를 켠 pane 이 활성이면 켠다" "yes" \
		"$(mouse_on_after t select-pane -t "$MOUSE_PANE")"
	t select-pane -t "$SHELL_PANE"
	sleep 0.3
	ok "mouse on 이면 셸 pane 이 활성이어도 켠다" "yes" \
		"$(mouse_on_after t set -g mouse on)"
	t set -g mouse off
	kill "$MPID" 2>/dev/null || true
	wait "$MPID" 2>/dev/null || true
	rm -f "$MLOG"
else
	echo "  SKIP  script(1) 이 없어 pty 를 못 붙였습니다"
fi

# ── 14. 쓰는 터미널이 바뀔 때 ────────────────────────────────────────────────
# tmux/viewport.go 는 창을 «가장 최근에 쓴 터미널» 크기로 고정하고, tmux.conf 는
# 그 밖의 창에 window-size latest 를 쓴다. 다시 맞추는 계기는 tmux/setuppanel.go 의
# client-active·client-detached 훅이다. 하나라도 안 돌면 밖에서 ssh 로 쓰다 집에
# 돌아와 kitty 에 입력해도 창이 ssh 크기에 머문다.
#
# 클라이언트 둘을 script(1) 로 붙이고, 각자의 표준입력(FIFO)에 글자를 써서 «그
# 클라이언트에서 입력했다»를 만든다.
echo
echo "쓰는 터미널 따라가기 (tmux/viewport.go, tmux/setuppanel.go)"

if command -v script >/dev/null 2>&1 && command -v mkfifo >/dev/null 2>&1; then
	AT=$(mktemp -d)
	mkfifo "$AT/in1" "$AT/in2"
	sleep 60 >"$AT/in1" &
	HOLD1=$!
	sleep 60 >"$AT/in2" &
	HOLD2=$!
	t set -g window-size latest
	t set-hook -g client-active "run-shell 'echo active #{client_tty} >> $AT/log'"
	t set-hook -g client-detached "run-shell 'echo detached #{pane_id} >> $AT/log'"

	SHELL=/bin/sh script -qfc "stty rows 40 cols 200; exec $TMUX_BIN -L $SOCKET -f /dev/null attach-session -t a" \
		/dev/null <"$AT/in1" >/dev/null 2>&1 &
	BIG=$!
	sleep 0.5
	SHELL=/bin/sh script -qfc "stty rows 25 cols 120; exec $TMUX_BIN -L $SOCKET -f /dev/null attach-session -t a" \
		/dev/null <"$AT/in2" >/dev/null 2>&1 &
	SMALL=$!
	for _ in $(seq 1 30); do
		[ "$(t list-clients -F x 2>/dev/null | wc -l)" -ge 2 ] && break
		sleep 0.1
	done
	TTY_BIG=$(t list-clients -F '#{client_height} #{client_tty}' | awk '$1==40{print $2}')
	TTY_SMALL=$(t list-clients -F '#{client_height} #{client_tty}' | awk '$1==25{print $2}')
	: >"$AT/log"

	printf 'x' >"$AT/in1"
	sleep 0.5
	ok "latest: 창이 방금 입력한 큰 클라이언트 크기 (200x39)" "200x39" \
		"$(t display -p -t a '#{window_width}x#{window_height}')"
	printf 'y' >"$AT/in2"
	sleep 0.5
	ok "latest: 작은 쪽에서 입력하면 그 크기 (120x24)" "120x24" \
		"$(t display -p -t a '#{window_width}x#{window_height}')"
	printf 'z' >"$AT/in2"
	sleep 0.5
	# 바뀔 때만 돈다 — 같은 클라이언트가 계속 입력해도 한 줄씩 쌓이지 않는다.
	ok "client-active 는 최근 클라이언트가 바뀔 때마다 돈다" \
		"active $TTY_BIG|active $TTY_SMALL" "$(paste -sd'|' "$AT/log")"

	# 끊긴 터미널: 클라이언트 프로세스를 죽인다(ssh 연결이 끊긴 것과 같은 모양).
	kill "$(t list-clients -F '#{client_tty} #{client_pid}' | awk -v t="$TTY_SMALL" '$1==t{print $2}')" 2>/dev/null
	sleep 0.7
	okc "client-detached 는 프로세스가 죽어도 돈다, #{pane_id} 도 펼쳐진다" "detached %" \
		"$(command grep detached "$AT/log")"
	ok "남은 클라이언트가 최근이 되며 client-active 가 한 번 더 돈다" "active $TTY_BIG" \
		"$(command grep active "$AT/log" | tail -1)"

	kill "$BIG" "$SMALL" "$HOLD1" "$HOLD2" 2>/dev/null || true
	wait "$BIG" "$SMALL" "$HOLD1" "$HOLD2" 2>/dev/null || true
	rm -rf "$AT"
else
	echo "  SKIP  script(1) 이나 mkfifo 가 없어 클라이언트를 붙이지 못했습니다"
fi

# ── 15. 에이전트 프롬프트 ───────────────────────────────────────────────────
# tmux/agent_prompt.go 는 다른 Claude 의 입력창에 글을 친다(`mux agent prompt`). 글은
# send-keys -l -- 로 한 번 치고, Enter 는 따로 보내고, 어긋나면 친 글자 수만큼 BSpace 로
# 되돌린다. 아래가 그 전제다 — cat 이 도는 pane 에서 tty 에코로 확인한다.
echo
echo "에이전트 프롬프트 (tmux/agent_prompt.go)"
AP=$(t new-session -d -P -F '#{pane_id}' -s agentapi -x 120 -y 30 cat)
sleep 0.3
apcount() { sleep 0.4; t capture-pane -p -t "$AP" | grep -c -F -- "$1"; }

t send-keys -t "$AP" -l -- '-n Up Space C-u lit1'
t send-keys -t "$AP" Enter
ok "send-keys -l -- 는 키 이름·플래그 모양 글도 글자 그대로 보낸다, Enter 는 따로 보내도 제출된다" "2" \
	"$(apcount '-n Up Space C-u lit1')"
OUT=$(t send-keys -t "$AP" -l '-n lit2' 2>&1)
okc "-- 가 없으면 '-n' 을 플래그로 읽는다 (그래서 늘 -- 를 넣는다)" "-n" "$OUT"
OUT=$(t send-keys -t "$AP" -l -- 'semi;' bogus-cmd 2>&1)
okc "«;» 로 끝나는 인자는 명령을 끊는다 (뒤 인자가 새 명령이 된다)" "unknown command" "$OUT"
t send-keys -t "$AP" -N 3 BSpace # 위에서 친 'semi' 의 일부가 남았을 수 있다 — 줄을 비운다
t send-keys -t "$AP" C-u
t send-keys -t "$AP" -l -- 'lit3\;'
t send-keys -t "$AP" Enter
ok "«\;» 로 끝나면 «;» 가 글자로 간다" "2" "$(apcount 'lit3;')"
t send-keys -t "$AP" -l -- 'lit4abcdef'
t send-keys -t "$AP" -N 3 BSpace
t send-keys -t "$AP" Enter
# 지운 줄의 에코와 cat 의 출력, 두 줄 모두 'lit4abc' 이고 'def' 는 어디에도 없어야 한다.
ok "send-keys -N 3 BSpace 는 세 글자를 지운다" "2 0" "$(apcount 'lit4abc') $(apcount 'lit4abcd')"
OUT=$(LANG=C LC_ALL=C "$TMUX_BIN" -L "$SOCKET" send-keys -t "$AP" -l -- '한글 ❯ lit5' 2>&1)
t send-keys -t "$AP" Enter
ok "LANG=C 클라이언트가 보낸 한글·❯ 도 그대로 간다" "2" "$(apcount '한글 ❯ lit5')"
t send-keys -t "$AP" -l -- "$(printf '\xe2\x9d\xaf\xc2\xa0lit6')"
sleep 0.4
ok "LANG=C 클라이언트의 capture-pane -p 도 ❯+U+00A0 를 UTF-8 바이트로 낸다" "1" \
	"$(LANG=C LC_ALL=C "$TMUX_BIN" -L "$SOCKET" capture-pane -p -t "$AP" | grep -c "$(printf '\xe2\x9d\xaf\xc2\xa0lit6')")"
t send-keys -t "$AP" C-u
t copy-mode -t "$AP"
ok "#{pane_in_mode} 는 복사 모드에서 1" "1" "$(t display-message -p -t "$AP" '#{pane_in_mode}')"
t send-keys -t "$AP" -X cancel
ok "send-keys -X cancel 로 복사 모드를 빠져나온다" "0" "$(t display-message -p -t "$AP" '#{pane_in_mode}')"
t set-window-option -t "$AP" synchronize-panes on
ok "#{pane_synchronized} 는 synchronize-panes 가 켜진 창에서 1" "1" "$(t display-message -p -t "$AP" '#{pane_synchronized}')"
t set-window-option -t "$AP" synchronize-panes off
ok "#{pane_synchronized} 는 꺼진 창에서 0" "0" "$(t display-message -p -t "$AP" '#{pane_synchronized}')"

echo
if [ "$FAILED" -eq 0 ]; then
	echo "$CHECKED 개 전부 통과 — $VERSION 에서 mux 의 가정이 유지됩니다."
	exit 0
fi
echo "$CHECKED 개 중 $FAILED 개 실패 — $VERSION 에서 동작이 바뀌었습니다."
echo "해당 항목이 걸린 코드를 확인하세요. 주석에 왜 그렇게 가정했는지 적혀 있습니다."
[ "$VERBOSE" = "-v" ] || echo "(-v 로 다시 돌리면 더 자세히 나옵니다)"
exit 1
