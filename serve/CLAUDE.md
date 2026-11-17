# serve/ — 고치기 전에 읽을 설계 문서

`mux serve` — 폰 브라우저 화면. 루트 `CLAUDE.md` 의 «되돌리기 쉬운 것들» 이 먼저다(`mux serve` 줄).
이 패키지는 tmux 를 직접 부르지 않는다 — `backend` 인터페이스 뒤에서 tmux 함수를 그대로 부르고, 테스트는
가짜 backend 를 넣는다. 진짜 배선은 e2e(`e2e/serve_e2e_test.go`)가 본다.

| 파일 | 문서 (`docs/design/`) |
|---|---|
| `server.go` (설정·루프백 검사·서버 수명), `auth.go` (신원 헤더·Host·Origin·CSP) | serve.md «보안» |
| `api.go` (목록·보내기·대화·화면, 결과 갈래), `backend.go` | serve.md, agent-api.md (`PromptAgent`·종료 코드) |
| `dialog.go` (대화상자 읽기·답하기·여러 개 고르기·Esc) | serve.md «대화상자에 답하기» |
| `static/` (카드, 입력줄, 받아쓰기, 세션 페이지, 화면 탭의 ANSI→HTML) | serve.md |

화면 문구는 `static/app.js` 의 `RESULTS` 가 정한다 — 서버는 갈래(`result`)와 이유(`detail`)만 낸다. 글은 늘
`textContent` 로 넣고, `innerHTML` 은 고정된 아이콘 문자열에만 쓴다(CSP 와 함께 XSS 를 막는 두 겹).
