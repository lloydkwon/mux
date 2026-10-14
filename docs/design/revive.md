# 소켓만 잃은 tmux 서버 되살리기

> 관련 코드: tmux/revive.go, tmux/proc.go, ui/remote.go, cmd/mux (reviveAndRemember)
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

**A tmux server that lost only its socket is revived, not mourned** (`tmux/revive.go`). `man tmux`: *"If the socket is accidentally removed, the SIGUSR1 signal may be sent to the tmux server process to recreate it (note that this will fail if any parent directories are missing)."* That is the whole fix for the incident above, and it existed the entire time — it just required a human who knew about it. `reviveAndRemember` in `cmd/mux` runs it at the TUI and panel entry points; `runPanelAuto` revives but does not record, because it is the hook path and must not grow a round trip.

**The server is identified from a note, never from a scan.** `RememberServer` asks `display-message -p '#{pid} #{socket_path}'` — one round trip, because the two are only ever used together — and writes them to `~/.config/mux/server.json` with the PID's start time. Scanning `/proc` for something that looks like a tmux server was the obvious alternative and is the dangerous one: the heuristics that separate a server from a client (no tty, PPID 1) fall apart on macOS, and when two servers are alive — which is exactly what this incident produces — reviving the wrong one orphans the server the user is actually in. That is not a bug you can undo.

`procStart` is stored for the same reason and is the load-bearing guard: `processAlive` (`tmux/proc.go`) compares it against `/proc/<pid>/stat` field 22, so a dead server's recycled PID cannot be signalled. Where it could not be recorded — no `/proc`, i.e. macOS — the feature stands down entirely rather than signal on existence alone. `/proc/<pid>/comm` reading exactly `tmux: server` is the second check, because a signal cannot be taken back.

**It never acts while the socket exists.** A socket at the path means someone is serving it — either our server is fine, or a *new* server has taken the place. Signalling in that second case makes the old server seize the path back and orphans the one the user is now using, which is the accident this code exists to prevent, caused by the code meant to prevent it. Recovery is also attempted once per process: firing at a dead PID every two-second tick is noise, not recovery.

`scripts/tmux-assumptions.sh` section 10 measures all of it against a real isolated server — that `#{pid}` names a live process whose `comm` is `tmux: server`, that `#{socket_path}` is the real path, that removing the socket makes calls fail while the server keeps running, and that SIGUSR1 brings back both the socket and the sessions. 29/29 on tmux 3.4.

**`ui/remote.go`'s socket moved out of `/tmp` for the same reason** — `~/.config/mux/tui.sock`. Its old comment justified `os.TempDir()` as "a fixed path that does not depend on environment variables", which was simply false: `os.TempDir()` reads `$TMPDIR`. `os.UserConfigDir()` is the more deterministic of the two and matches how `preferences.json` and `panel.json` resolve. When `/tmp` was emptied the TUI stayed up while its socket vanished, and `mux switch` failed with nothing to say.
