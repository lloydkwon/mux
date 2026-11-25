#!/usr/bin/env bash
# 커밋 하나(기본 HEAD)를 CI 와 같은 단계로 검증하고, 통과하면 그 sha 에 통과 기록을 남긴다.
# scripts/git-hooks/pre-push 는 이 기록이 있는 커밋만 내보낸다(`make hooks` 로 건다).
#
# 작업 트리가 아니라 그 커밋을 임시 worktree 에 풀어서 돌린다. push 되는 것은 커밋인데, 작업 트리에는
# 커밋 안 된 변경이나 추적 안 된 .go 파일이 섞일 수 있어서, 거기서 통과해도 그 커밋이 통과한다는 뜻이 아니다.
# 단계의 명령도 그 커밋 안의 것(scripts/tmux-assumptions.sh, e2e)을 쓴다.
#
# 첫 실패에서 멈춘다(CI 와 같다). 테스트 단계는 실제로 돈 테스트 수를 세고, 0개면 실패로 본다 —
# e2e 의 TestMain 은 tmux 나 script(1) 가 없으면 아무것도 안 돌리고 exit 0 으로 끝난다.
#
# 사용:  scripts/verify.sh [커밋]        (make verify)
# 기록:  <git 공통 디렉터리>/mux-verify/<sha>.log   — 매번
#        <git 공통 디렉터리>/mux-verify/<sha>.ok    — 통과했을 때만. 첫 줄이 요약이다
# 종료:  0 통과 · 1 실패 · 2 판정 불가(커밋을 못 찾음, worktree 를 못 만듦 등)
set -uo pipefail

die() { echo "verify: $*" >&2; exit 2; }

repo=$(git rev-parse --show-toplevel 2>/dev/null) || die "git 저장소 안에서 돌린다"
cd "$repo" || die "$repo 로 못 간다"
rev=${1:-HEAD}
sha=$(git rev-parse --verify --quiet "$rev^{commit}") || die "커밋을 못 찾는다: $rev"
common=$(cd "$(git rev-parse --git-common-dir)" && pwd) || die "git 공통 디렉터리를 못 찾는다"
stamps="$common/mux-verify"
mkdir -p "$stamps" || die "$stamps 를 못 만든다"
log="$stamps/$sha.log"
ok="$stamps/$sha.ok"
rm -f "$ok"
: > "$log" || die "$log 에 못 쓴다"

# 기준: 원격 main 과 갈라진 지점. 그 뒤의 커밋만 메시지를 본다(옛 상류 커밋은 형식이 다르다).
base=""
if git rev-parse --verify --quiet origin/main >/dev/null; then
	base=$(git merge-base "$sha" origin/main 2>/dev/null || true)
fi

wt=$(mktemp -d "${TMPDIR:-/tmp}/mux-verify.XXXXXX") || die "임시 디렉터리를 못 만든다"
cleanup() {
	git -C "$repo" worktree remove --force "$wt" >/dev/null 2>&1 || rm -rf "$wt"
	git -C "$repo" worktree prune >/dev/null 2>&1
}
trap cleanup EXIT
trap 'exit 130' INT TERM
git worktree add --detach --quiet "$wt" "$sha" >>"$log" 2>&1 || die "worktree 를 못 만든다 (로그: $log)"

echo "검증: ${sha:0:12} — $(git log -1 --format=%s "$sha")"
dirty=$(git status --porcelain | wc -l)
[ "$dirty" -gt 0 ] && echo "  (작업 트리의 커밋 안 된 변경 ${dirty}개는 이 검증에 들어가지 않는다)"

results=()   # 단계마다 한 줄 — .ok 에 그대로 남는다
skipped=()   # 건너뛴 단계와 이유

# 단계 하나를 돌린다: run_step <이름> <명령...>. 출력은 로그로, 끝난 뒤 한 줄 요약을 찍는다.
# 요약 줄은 단계 뒤에 note 변수로 덧붙일 수 있다.
step_out=""
run_step() {
	local name=$1; shift
	local t0=$SECONDS rc
	step_out=$(mktemp "${TMPDIR:-/tmp}/mux-verify-step.XXXXXX")
	echo "=== $name: $*" >>"$log"
	(cd "$wt" && "$@") >"$step_out" 2>&1
	rc=$?
	cat "$step_out" >>"$log"
	STEP_RC=$rc
	STEP_SECS=$((SECONDS - t0))
}

finish_step() {   # finish_step <이름> <판정 0|1> <요약>
	local name=$1 verdict=$2 note=$3
	local mark="✓"; [ "$verdict" -ne 0 ] && mark="✗"
	local line="$mark $name (${STEP_SECS}s)${note:+ — $note}"
	echo "  $line"
	results+=("$line")
	rm -f "$step_out"
	if [ "$verdict" -ne 0 ]; then
		echo "  실패한 단계의 출력 끝부분:"
		grep -v '^=== ' "$log" | tail -n 25 | sed 's/^/    /'
		echo "  전체 로그: $log"
		exit 1
	fi
}

# go test -v 출력에서 최상위 테스트의 통과·실패·건너뜀을 센다(하위 테스트는 들여쓰기가 있어 빠진다).
count_tests() {
	local f=$1
	T_PASS=$(grep -c '^--- PASS: ' "$f")
	T_FAIL=$(grep -c '^--- FAIL: ' "$f")
	T_SKIP=$(grep -c '^--- SKIP: ' "$f")
}

# 건너뛴 최상위 테스트의 이름과 이유를 skipped 에 더한다. go test -v 는 t.Skip 의 문구를 «--- SKIP:» 줄
# 앞에 들여 써서 찍는다(=== RUN 다음, 결과 줄 전). 그래서 결과 줄 직전의 들여 쓴 로그 줄을 이유로 잡는다.
collect_skips() {
	local f=$1 label=$2 line
	while IFS= read -r line; do
		skipped+=("$label $line")
	done < <(awk '
		/^=== /                     { reason = ""; next }
		/^ +--- /                   { next }
		/^ +[^ ]/                   { reason = $0; sub(/^ +/, "", reason); next }
		/^--- SKIP: /               { print $3 " — " (reason == "" ? "이유가 찍히지 않았다" : reason); reason = ""; next }
		/^--- /                     { reason = "" }
	' "$f")
}

# 1. 커밋 메시지 — Conventional Commits 접두어와 한국어 설명
if [ -z "$base" ]; then
	skipped+=("커밋 메시지 — origin/main 이 없어 비교할 기준이 없다")
else
	STEP_SECS=0
	step_out=$(mktemp "${TMPDIR:-/tmp}/mux-verify-step.XXXXXX")
	subjects=$(git log --no-merges --format='%h %s' "$base..$sha") || die "커밋 목록을 못 읽는다 ($base..$sha)"
	n=$(git rev-list --no-merges --count "$base..$sha") || die "커밋 수를 못 센다 ($base..$sha)"
	bad=""
	if [ -n "$subjects" ]; then
		# \p{Hangul} 은 UTF-8 로캘에서만 글자 단위로 맞는다 — 훅·CI 의 C 로캘에서도 같게 고정한다.
		# grep 종료 코드: 0 안 맞는 줄 있음 · 1 없음 · 2 판정 불가(로캘이나 -P 가 없다). 판정 불가를 통과로 두지 않는다.
		bad=$(printf '%s\n' "$subjects" |
			LC_ALL=C.UTF-8 grep -vP '^\S+ (feat|fix|refactor|docs|test|chore|perf|ci)(\([^)]+\))?!?: .*\p{Hangul}')
		grep_rc=$?
		[ "$grep_rc" -gt 1 ] && die "커밋 메시지를 판정하지 못했다 (grep 종료 코드 $grep_rc — C.UTF-8 로캘과 grep -P 가 있어야 한다)"
	fi
	if [ -n "$bad" ]; then
		{ echo "=== 커밋 메시지: 형식에 안 맞는 커밋"; echo "$bad"; } >>"$log"
		finish_step "커밋 메시지" 1 "$n개 중 $(echo "$bad" | wc -l)개가 '<type>(범위): 한국어 설명' 이 아니다"
	fi
	finish_step "커밋 메시지" 0 "origin/main 뒤 $n개 모두 형식에 맞음"
fi

# 2. gofmt — 추적되는 .go 파일 전부
run_step gofmt bash -c 'git ls-files -z "*.go" | xargs -0 gofmt -l'
nfiles=$(cd "$wt" && git ls-files '*.go' | wc -l)
if [ "$STEP_RC" -ne 0 ]; then finish_step gofmt 1 "gofmt 가 실패했다"; fi
if [ -s "$step_out" ]; then finish_step gofmt 1 "정렬 안 된 파일 $(wc -l <"$step_out")개"; fi
finish_step gofmt 0 ".go 파일 ${nfiles}개"

# 3. go vet
run_step "go vet" go vet ./...
finish_step "go vet" "$STEP_RC" ""

# 4. golangci-lint — 있을 때만. 없으면 건너뜀으로 남기고 통과 판정에는 넣지 않는다(CI 도 돌리지 않는다).
if command -v golangci-lint >/dev/null 2>&1; then
	run_step golangci-lint golangci-lint run ./...
	finish_step golangci-lint "$STEP_RC" ""
else
	skipped+=("golangci-lint — 이 기계에 설치되어 있지 않다")
fi

# 5. 빌드 — CI 와 같이 linux/amd64 와 arm64
run_step build go build -o /dev/null ./cmd/mux
finish_step build "$STEP_RC" ""
run_step "build arm64" env GOARCH=arm64 go build -o /dev/null ./cmd/mux
finish_step "build arm64" "$STEP_RC" ""

# 6. 단위 테스트
run_step "go test" go test -v ./...
count_tests "$step_out"
pkgs_ok=$(grep -cE '^ok ' "$step_out")
collect_skips "$step_out" "go test:"
verdict=$STEP_RC
[ "$T_PASS" -eq 0 ] && verdict=1
finish_step "go test" "$verdict" "테스트 $((T_PASS + T_FAIL + T_SKIP))개 중 통과 $T_PASS · 실패 $T_FAIL · 건너뜀 $T_SKIP (패키지 ok ${pkgs_ok}개)"

# 7. tmux 자신의 동작 가정 — 그 커밋의 scripts/tmux-assumptions.sh. CI 처럼 5분 상한을 둔다
#    (2026-11-02 에 읽는 쪽 없는 FIFO 에 쓰다가 멈춘 적이 있다 — .github/workflows/ci.yml).
run_step "tmux 가정" timeout 300 ./scripts/tmux-assumptions.sh
tline=$(grep -E '개 (전부 통과|중 .* 실패)' "$step_out" | tail -1)
verdict=$STEP_RC
[ -z "$tline" ] && verdict=1 && tline="요약 줄을 못 찾았다"
finish_step "tmux 가정" "$verdict" "$tline · $(tmux -V)"

# 8. e2e — Makefile 의 e2e 와 같은 명령에 -v 만 더한다(돈 테스트 수를 세려고)
run_step e2e go test -tags e2e -count=1 -timeout 8m -v ./e2e/
count_tests "$step_out"
collect_skips "$step_out" "e2e:"
verdict=$STEP_RC
note="테스트 $((T_PASS + T_FAIL + T_SKIP))개 중 통과 $T_PASS · 실패 $T_FAIL · 건너뜀 $T_SKIP"
if [ "$T_PASS" -eq 0 ]; then
	verdict=1
	note="$note — 돈 테스트가 없다($(grep -m1 '건너뜀' "$step_out" || echo '이유 모름'))"
fi
finish_step e2e "$verdict" "$note"

# 통과 기록 — 같은 디렉터리의 임시 파일에 다 쓴 뒤 mv 로 바꿔 넣는다. 반쯤 쓴 .ok 를 훅이 읽지 않게 하고,
# 쓰지 못했으면 «통과» 라고 말하지 않는다(기록이 없으면 훅이 막으니 통과라고 하면 거짓이다).
tmp_ok=$(mktemp "$stamps/.ok.XXXXXX") || die "기록용 임시 파일을 못 만든다 ($stamps)"
if ! {
	echo "통과 $(date '+%Y-%m-%d %H:%M') · ${sha:0:12} · $(go version | awk '{print $3}') · $(tmux -V)"
	printf '%s\n' "${results[@]}"
	if [ "${#skipped[@]}" -gt 0 ]; then
		echo "건너뜀 ${#skipped[@]}개:"
		printf '  %s\n' "${skipped[@]}"
	fi
} > "$tmp_ok" || ! mv -f "$tmp_ok" "$ok"; then
	rm -f "$tmp_ok"
	die "검증은 통과했지만 기록을 쓰지 못했다: $ok — 다시 돌린다"
fi

echo "통과 — 기록: $ok"
if [ "${#skipped[@]}" -gt 0 ]; then
	echo "건너뜀 ${#skipped[@]}개:"
	printf '  %s\n' "${skipped[@]}"
fi
