---
name: tui-snapshot
description: mux 의 TUI·패널을 격리된 tmux 서버에서 실제로 띄우고 키를 보내 화면을 글자로 찍는다. 렌더링·키 처리·레이아웃을 고친 뒤 «실제 화면에서 맞는지» 확인할 때, 또는 /run 으로 mux 를 띄워 보라는 요청에 쓴다.
---

# mux 화면 확인 — `scripts/tui-snapshot.sh`

단위 테스트와 골든(`go test ./ui -run TestGolden`)은 목킹한 Model 을 그린다. 이 스크립트는 **진짜 tmux
위에서 작업 트리 빌드가 그리는 화면**을 평문으로 돌려준다. 렌더링·키 처리를 고쳤으면 커밋 전에 한 번 본다.

```bash
scripts/tui-snapshot.sh                                  # 기본 픽스처, 120x30
scripts/tui-snapshot.sh --size 80x24 --keys 'j j Tab'    # 키는 tmux send-keys 이름, 공백 구분
scripts/tui-snapshot.sh --keys '/ a l Enter'             # 필터
scripts/tui-snapshot.sh --cmd watch --size 40x30         # 패널(mux watch)
scripts/tui-snapshot.sh --ansi                           # 색까지
```

픽스처: `alpha`(창 2, pane 분할) · `beta`(출력 몇 줄) · `a-session-with-a-rather-long-name`, 그리고 mux 자신이
도는 `zz-snapshot`. 나이 컬럼은 실제 시각이라 매번 다르다 — 글자 비교가 필요하면 골든 테스트를 쓴다.

**사용자의 tmux 서버에서 mux 를 직접 띄우지 않는다.** 시작할 때 `~/.tmux.conf` 의 mux 영역을 현재
바이너리로 «고치고»(tmux/repair.go) `~/.config/mux/server.json` 을 쓴다. 이 스크립트는 전용 소켓 서버 +
임시 `HOME`·`XDG_CONFIG_HOME` 으로 그 셋을 막는다 — 격리를 빼는 수정을 하지 말 것.
`scripts/test-fixture.sh` 는 실제 서버에 세션을 만드는 사람용 도구라 여기서 쓰지 않는다.
