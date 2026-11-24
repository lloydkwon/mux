# herdr/ — 고치기 전에 읽을 설계 문서

이 패키지 전체가 [docs/design/herdr.md](../docs/design/herdr.md) 다. 읽기 전용이고, 목 러너는
`tmux/` 와 **인스턴스를 공유하지 않는다**(키가 충돌한다). 응답 모양 가정은
`scripts/herdr-assumptions.sh`(`make herdr-check`)가 실측하고, 테스트는 `testdata/` 의 캡처 픽스처로 hermetic 하다.
`holds.go`(`ClaudeHolds` — `mux --claude` 가 herdr 가 쥔 대화를 입양하지 않게)는 claude-mode.md «herdr 가 쥔 대화» 다.
