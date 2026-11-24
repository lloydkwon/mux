# New worktree — 브랜치 이름 하나로 worktree 와 그 안의 셸 세션

목록 맨 위의 셋째 행 **New worktree** (`⌥`)에서 브랜치 이름을 치면 다음 셋이 한 번에 된다.

1. 원본 저장소 옆에 그 브랜치의 git worktree 를 만든다.
2. TUI 가 끝난 뒤 그 폴더에서 셸 세션을 연다.
3. 그 세션으로 들어간다.

그전에는 mux 가 worktree 를 알아보기만 했다(`⌥⌥`). 그래서 `git worktree add` 를 직접 한 뒤 New tmux session 의
`Dir:` 에 경로를 다시 쳐야 했다(2026-11-24 사용자 요청).

코드는 세 곳에 있다.
- `tmux/worktree.go` — git 쪽
- `ui/worktree.go` — 입력 화면
- `cmd/mux/main.go` 의 `runTUI` — `NewSessionRequest` 분기

## 자리 — 원본 저장소 옆

`<원본의 부모>/<원본 이름>-<브랜치의 / 를 - 로>` 이다. 예를 들어 `~/dev/projects/my/mux` 에서 `feat/x` 를 만들면
`~/dev/projects/my/mux-feat-x` 가 된다. 사용자가 정했다(2026-11-24). 다른 후보 둘을 버린 이유:
- **저장소 안 `.claude/worktrees/`** (`claude -w` 의 자리): 저장소마다 `.git/info/exclude` 가 없으면 `git status` 에 보인다.
  npm·pytest 같은 도구도 안으로 들어간다.
- **`.worktrees/`**: 같은 문제가 있다.

저장소 옆에 두면 폴더 이름에 저장소 이름이 들어가, 프로젝트 필터로도 찾힌다.

`Repo:` 가 이미 linked worktree 여도 새 worktree 는 **원본** 옆이다. 원본은 `git worktree list --porcelain -z` 의
첫 항목이다. `--git-common-dir` 은 쓰지 않는다. 하위 모듈에서는 `.git/modules/<이름>` 을 내놓고, 상대 경로로
답할 때도 있다(실측: 맨 위에서 `.git`, 하위 폴더에서 `../.git`). porcelain 의 첫 항목은 bare 저장소도 `bare` 줄로
가려 준다. bare 저장소는 지원하지 않는다.

`-z` 는 **git 2.36 이상**이 필요하다(git 의 RelNotes 2.36.0: «`--porcelain` 이 위험한 바이트가 든 경로를 제대로
C-quote 하지 못해 NUL 로 끝나는 `-z` 를 들였다»). 그보다 오래된 git 에서는 목록을 못 읽어 «git 저장소를 읽지 못했다»
로 거절한다 — 만들지는 않으니 안전하다. 이 기계는 2.56.1, CI 의 ubuntu-26.04 도 그보다 새것이다.

세션 이름은 폴더 이름을 `SafeSessionName` 에 거친 것이다. tmux 가 뜻을 주는 `.` 과 `:` 를 `-` 로 바꾼다.
이름이 겹치면 `CreateSessionWithCommand` 의 `freeSessionName` 이 `-2` 를 붙인다.

## 브랜치를 고르는 순서

1. 로컬 브랜치 `refs/heads/<b>` 가 있으면 → `git worktree add <path> <b>`
2. 원격 **하나에만** `<원격>/<b>` 가 있으면 → `git worktree add --track -b <b> <path> <원격>/<b>`
3. 그 밖의 경우 → `git worktree add -b <b> <path>`. 시작점은 `Repo:` 폴더(그 worktree)의 HEAD 다.

모두 `git -C <Repo>` 로 부른다. 그래야 3번의 시작점이 사람이 보던 checkout 이 된다.

- **원격 브랜치를 git 의 DWIM 에 맡기지 않는다.** DWIM 은 원격 설정이 있어야 돈다(실측: 설정 없는 원격 ref 는
  «잘못된 레퍼런스»). 게다가 어느 원격을 골랐는지가 명령줄에 남지 않는다.
- **원격 이름은 `git remote` 로 받는다.** `for-each-ref 'refs/remotes/*/x'` 로 찾으면 `*` 가 `/` 를 넘어서
  `refs/remotes/origin/feat/x` 가 `x` 에 맞는다(실측).
- **`for-each-ref` 의 출력은 정확히 비교한다.** 패턴이 앞부분 일치라 `refs/heads/feat` 이 `refs/heads/feat/x` 도 내놓는다.
- 원격 여럿에 있으면 고르지 않고 3번으로 간다. git 의 DWIM 도 그때는 고르지 않는다.

## 다시 쓰기와 거절

| 상황 | 결과 |
|---|---|
| 그 브랜치가 linked worktree 에 이미 있다 (`claude -w` 가 만든 것 포함) | 만들지 않고 그 폴더로 세션을 연다 (`Reused`) |
| 그 브랜치가 원본 작업 트리에 체크아웃돼 있다 | 거절한다. git 도 거절하지만(«already used by worktree»), 미리 말하는 편이 이유가 분명하다 |
| porcelain 에 `prunable` 이거나 폴더가 없다 | 거절하고 `git worktree prune` 을 권한다 |
| 만들 자리에 폴더가 이미 있다 | 거절한다 (덮지 않는다). `feat.x` 와 `feat/x` 처럼 폴더 이름이 같아지는 경우도 여기서 막힌다 |
| 이름이 `git check-ref-format --branch` 를 통과하지 못한다 | 거절한다. `-` 로 시작해 옵션으로 읽힐 이름도 여기서 막힌다 |
| `Repo:` 가 상대 경로다 | 거절한다. mux 를 띄운 폴더 기준으로 풀리면 사람이 본 것과 다른 저장소가 된다 (`~` 는 홈으로 푼다) |

worktree 를 지우는 기능은 범위 밖이다. 세션을 닫아도 worktree 는 남고, `git worktree remove` 는 사람이 한다.

## Repo 칸 — mux 를 띄운 세션

미리 채우는 순서:
1. VS Code·IntelliJ 에서 연 목록이면 그 프로젝트(`m.projectDir`)
2. 아니면 **mux 를 띄운 세션**의 `@project_dir`
3. 그것도 없으면 그 세션의 폴더(`Directory`)

mux popup 이면 2·3번의 세션은 그 아래의 세션이다. 세션 이름은 `tmux.CurrentSessionName` 이 클라이언트에 묻는다.
`#{client_pid}|#{session_name}` 을 읽고, pid 가 없으면 빈 값이다. 클라이언트 없이 물으면 tmux 가 다른 세션을 골라
답할 수 있기 때문이다(`ProjectScope` 와 같은 확인). 고르지 못하면 칸을 비워 두고, 사람이 그사이 친 것은 덮지 않는다.

- **«커서가 마지막으로 머문 세션» 으로 채우던 첫 안은 버렸다.** 키보드로 이 행까지 올라오면 그 위의 세션을 모두
  지나서, 언제나 맨 위 세션이 되었다. 시험이 잡았다(`TestWorktreePromptStartsFromTheSessionMuxWasOpenedFrom`).
- **`ProjectScope` 의 한 번 호출에 붙이지 않았다.** 세션 이름에는 `|` 가 들어갈 수 있어, 경로와 이름 두 가변 칸을
  한 줄에 담으면 경계가 모호해진다. 대신 입력 화면을 열 때만 Cmd 로 한 번 묻는다. 그래서 시작 비용은 그대로다.
- 그래서 행의 오른쪽 칸에는 저장소를 적지 않는다. 열기 전에는 아직 묻지 않았기 때문이다.

## 루프 규칙 — git 은 Cmd, 세션은 cmd/mux

- **git 은 `tea.Cmd` 안에서 돈다.** `ui/worktree.go` 의 `prepareWorktree`·`currentSessionName` 시임을 거친다.
  `Update` 는 Cmd 만 돌려준다(`TestWorktreeRunsOnlyInTheCmd`, park 의 `TestConfirmParkRunsOnlyInTheCmd` 와 같다).
  `go test ./ui` 가 디스크에 worktree 를 만들지 않게 하려는 것이다.
- git 을 TUI 밖으로 미루지 않은 이유가 있다. 거절(원본 작업 트리의 브랜치, 이미 있는 폴더)을 그 원인이 된 입력 화면에서
  보이고 다시 받기 위해서다. TUI 가 끝난 뒤라면 popup 이 닫히며 오류가 사라진다.
- **세션 생성과 attach 는 TUI 가 끝난 뒤 `cmd/mux` 가 한다**(`newSessionRequest`). `resumeRequest`·`unparkTarget` 과
  같은 자리다. 명령 없는 `CreateSessionWithCommand` 는 tmux 의 기본 셸이다.
- **`@project_dir` 태그를 달지 않는다.** 태그가 있으면 `mux panel --auto` 가 물러난다(tmux-config.md — VS Code 판별의
  1순위). 그러면 kitty 에서 만든 worktree 세션에 패널이 생기지 않는다. New tmux session 의 `CreateSessionWithDir` 가
  태그를 다는 것과 다르다. e2e 가 «태그 없음» 을 단언한다.
- git 이 도는 동안 `enter` 는 무시한다. 두 번째 enter 가 «이미 있는 폴더» 로 거꾸로 보고하기 때문이다.
  `esc` 는 닫는다. 늦게 온 결과는 상태 줄에 알린다(`worktree ready at … (not opened)`). 만든 것을 말없이 두지 않는다.
  결과는 그 일을 시킨 입력 화면의 번호(`worktreeModel.id`, 연 차례)를 달고 온다. 닫고 다시 연 화면에 첫 화면의 결과가
  오면 그것도 «늦은 결과» 다 — 번호 없이 «지금 입력 화면인가» 만 보면 둘째 화면에서 첫 브랜치의 세션을 열 뻔했다
  (검토에서 나옴, `TestWorktreeResultOfAClosedPromptDoesNotLandOnTheNextOne`).
- 제한 시간은 읽기 호출이 5초, `worktree add` 가 2분이다. 체크아웃과 post-checkout 훅이 함께 돈다.
  끝이 없으면 «creating worktree...» 가 영원히 남는다.
- 오버레이는 줄을 접지 않는다. 그래서 오류는 56칸에서 접는다. mux popup 의 가장 작은 안쪽 폭(68 − 6) 안이다.

## 시험

- **`tmux/worktree_test.go`** (목): 브랜치를 고르는 여섯 경우, 이름, linked worktree 에서 부른 경우, 다시 쓰기,
  거절 열 가지, `~`, porcelain 파서, `CurrentSessionName`
- **`ui/worktree_test.go`** (시임): 행의 자리, Repo 칸, «Cmd 에서만», busy, 오류, 늦은 결과(닫고 다시 연 화면 포함), 더블클릭, 미리보기
- **`e2e/worktree_e2e_test.go`** (격리 tmux + 진짜 git): 붙은 클라이언트가 있는 TUI 에서 행 → 브랜치 → 클라이언트가 새
  세션으로 간다.
  - 세션 폴더와 브랜치, 태그 없음을 본다.
  - 원본 브랜치 거절과 다시 쓰기(`-2`)를 본다.
  - `TestPrepareWorktreeAgainstRealGit` 은 로컬 브랜치, 원격 추적, linked worktree 의 HEAD, prune 안내를 진짜 git 으로 잰다.
    위의 «실측» 은 tmux 가 아니라 git 의 동작이라 `scripts/tmux-assumptions.sh` 가 아니라 여기서 고정한다.
- 처음 쓴 e2e 의 «태그 없음» 단언은 `show-options -t =이름` 이었다. pane 대상이라 조회가 실패하고, 빈 값으로 늘 통과했다.
  `=이름:` 으로 고쳤다(17절과 같은 이유). 고친 곳을 일부러 망가뜨린 여섯 경우를 모두 시험이 잡는지 확인한 덕에 찾았다.
