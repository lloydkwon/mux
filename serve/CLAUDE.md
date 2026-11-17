# serve/ — 고치기 전에 읽을 설계 문서

`mux serve` — 폰 브라우저 화면. 루트 `CLAUDE.md` 의 «되돌리기 쉬운 것들» 이 먼저다(`mux serve` 줄).
이 패키지는 tmux 를 직접 부르지 않는다 — `backend` 인터페이스 뒤에서 tmux 함수를 그대로 부르고, 테스트는
가짜 backend 를 넣는다. 진짜 배선은 e2e(`e2e/serve_e2e_test.go`)가 본다.

| 파일 | 문서 (`docs/design/`) |
|---|---|
| `server.go` (설정·루프백 검사·서버 수명), `auth.go` (신원 헤더·Host·Origin·CSP) | serve.md «보안» |
| `api.go` (목록·보내기·대화·화면, 결과 갈래), `backend.go` | serve.md, agent-api.md (`PromptAgent`·종료 코드) |
| `dialog.go` (대화상자 읽기·답하기·여러 개 고르기·Esc) | serve.md «대화상자에 답하기» |
| `passkey.go` (패스키 파일·등록 코드), `session.go` (로그인 쿠키·의식), `login.go` (등록·로그인 핸들러·공개 경로), `static/login.*`·`manifest.webmanifest`·`icon-*.png`, `../cmd/mux/serve.go` 의 `enroll` | serve.md «패스키 로그인 — Muxtap», «보안» 8 |
| `unpark.go` (보관함 보기·되살리기 — `tmux.RestoreParked` 를 그대로 부른다, 보관·지우기는 없다) | serve.md «보관함 되살리기», park.md «복원» |
| `static/` (카드, 입력줄, 받아쓰기, 세션 페이지, 화면 탭의 ANSI→HTML) | serve.md |
| `transcribe.go` (녹음 받기·받아쓰기 명령·프로세스 묶음 죽이기), `dictation.go` (용어 파일·프롬프트·바꿔 쓰기), `dictlog.go` (받아쓰기 기록 — 받아쓴 글은 여기에만, journald 에는 없다), `static/record.js` (MediaRecorder), `../scripts/whisper/` | serve.md «받아쓰기 — 서버 Whisper» |

받아쓰기에서 되돌리기 쉬운데 되돌리면 안 되는 것 — 근거(측정)는 serve.md 에 있다: `transcribe.py` 의 묶음 처리
(`BatchedInferencePipeline` — 순차 `model.transcribe` 는 30초 창 뒤에 프롬프트를 버린다, 타임스탬프는 끈 채로),
`vad_filter=True`(끄면 무음에서 말을 지어낸다), `condition_on_previous_text=False`, `hotwords` 안 씀(뒤가 잘린다), 프롬프트는
머리말 없는 목록(`dictationPrompt` — 머리말은 짧은 말에 새고, 문장은 용어를 망친다)이고 `--prompt=` 한 덩어리로 넘긴다
(`-` 로 시작해도). 명령은 프로세스 묶음째 죽인다. 녹음 본문의 Content-Type 은 `audio/webm`·`audio/mp4` 뿐이다
(`acceptsBody` — 단순 요청 형식 금지).

화면 문구는 `static/app.js` 의 `RESULTS` 가 정한다 — 서버는 갈래(`result`)와 이유(`detail`)만 낸다. 글은 늘
`textContent` 로 넣고, `innerHTML` 은 고정된 아이콘 문자열에만 쓴다(CSP 와 함께 XSS 를 막는 두 겹).
