# 선택 모드

> 관련 코드: ui/app.go (toggleSelectMode)
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## 선택 모드는 틱도 멈춘다

`v` (`toggleSelectMode`, `ui/app.go`) 는 `tea.DisableMouse` 를 보내 마우스 리포팅을 끈다.
그것만으로는 절반이다: 목록이 0.5초마다 다시 그려지면 터미널이 선택 영역을 지우므로,
선택을 끝내기도 전에 사라진다. 그래서 `tickMsg` 핸들러가 선택 모드에서 **다음 틱을
예약하지 않고** 그냥 돌아간다 — 화면이 얼어야 긁을 수 있다.

되살리는 곳이 `toggleSelectMode` 한 군데뿐인 것이 그래서 중요하다. 선택 모드가 도는
동안 틱을 들고 있는 것이 아무것도 없으므로, 여기서 다시 걸지 않으면 목록이 영영 멈춘다.

`extraBar` 가 그리는 줄은 장식이 아니다. 설명 없이 멈춘 목록은 mux 가 죽은 것으로
읽히므로, 갱신이 멈췄다는 것과 어떻게 빠져나오는지를 같이 말한다. `chromeTop` 이
extraBar 유무로 마우스 행 보정을 이미 하므로 복귀 후 클릭 좌표는 저절로 맞는다.

무엇이 복사되는지는 어디서 도느냐에 달렸다. tmux 안이면 tmux 의 copy-mode, 팝업
안이면(팝업에는 copy-mode 가 없다) 터미널 자신의 선택 — WSL/Windows Terminal 에서
실제로 원하는 쪽이 후자다. 어느 쪽이든 **화면에 그려진 글자**를 긁는 것이라
`maxSessionNameDisplay` 나 `maxPathDisplay` 로 잘린 값이 복사될 수 있다.

패널(`mux watch`)에는 넣지 않았다. 별도 프로세스라 자기 토글과 자기 틱을 따로 가져야
한다.
