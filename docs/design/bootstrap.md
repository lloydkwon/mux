# tmux 밖에서 친 `mux` 도 팝업으로

> 관련 코드: cmd/mux (runRoot, runTUI), tmux/popup.go (AttachAndPopup)
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## Typing `mux` outside tmux gets the popup too

`prefix + m` draws mux in a `display-popup`; typed in a terminal that is not in tmux it drew itself in that terminal instead — the same program in a different shape. `runRoot` (`cmd/mux`) closes that gap by attaching first and popping up on the client that creates (`AttachAndPopup`, `tmux/popup.go`).

Attaching has to come first, and that is not a preference. Measured: with a server up and a client attached elsewhere, `display-popup` succeeds with `$TMUX` unset — and draws on *that* client, in a different terminal from the one you typed in. A popup needs a client, so outside tmux there is nothing to draw on until this terminal becomes one. `attach-session` with no `-t` takes tmux's own most-recently-used session; which session to attach to is the question mux exists to answer, but asking it needs a client, so tmux's default gets us on screen and the popup that follows is where the answer is given.

**`MUX_NO_BOOTSTRAP` is the recursion guard and it is load-bearing.** The popup runs mux, so without it the child bootstraps in turn and the popups never stop. `$TMUX` *is* set inside `display-popup -E` — measured, it reads back as the socket and a pane id — so that check would work today, but the cost of being wrong is an unbounded loop and an env var mux sets itself depends on nothing. It rides in the popup's command as a `VAR=1 exec …` prefix, which works because tmux runs a shell-command through a shell.

**Quitting that popup detaches, and that is the other half of the feature.** The terminal was attached only so there would be a client to draw the popup on, so `q` — no session chosen — has to give it back; otherwise closing the popup drops the user into whichever session `attach-session` picked, which is neither where they typed `mux` nor a session they chose, and getting out means knowing tmux's detach key. `runTUI` (`cmd/mux`) reads `BootstrapPopupEnv` and routes that case through `ui.DetachClient`, the same call `New shell` already makes from inside a popup.

`MUX_BOOTSTRAP_POPUP` is a **second** variable rather than `MUX_NO_BOOTSTRAP` doing double duty, because they answer different questions. The guard says "do not bootstrap", which a user may reasonably export in their own shell to opt out; the mark says "you are the popup a bootstrap opened", and only mux ever sets it. Read as one, an opted-out `mux` in a plain terminal would try to detach a client it never attached.

It stands down with no sessions: there is nothing to attach to, and creating one to attach to means naming it for the user, while the list mux draws instead already offers `New tmux session`, which asks. Every other failure — old tmux, no server, no executable — falls through to drawing in place, which is what mux did before.
