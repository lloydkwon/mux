# 렌더링 — 팔레트·프레임·고정 폭

> 관련 코드: ui/styles.go, ui/layout.go, ui/list.go
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## The palette is the terminal's

`ui/styles.go` names roles — accent, muted, danger — and fills them with **ANSI palette indices**, never hex. The user's scheme supplies the values, so a light scheme gets colours its author picked for a light background and a dark one gets colours picked for a dark background. Ordinary row text sets no colour at all, taking the terminal's own foreground.

It was hex once, all of it chosen against a dark terminal. Measured on GitHub Light (`#F6F8FA`, this fork's author's actual scheme), the colour every list row was drawn in reached **2.4:1** and the accent **1.7:1**, against a 4.5:1 floor. The screen was not ugly so much as barely there. `tmux/aitools.go`'s tool colours are indices for the same reason.

`lipgloss.AdaptiveColor` was the obvious alternative and is deliberately not used: it resolves by asking the terminal for its background over OSC 11, and `mux watch` runs inside a tmux pane where the answer may never arrive — which would draw the panel in the opposite palette from the TUI beside it. An index has nothing to ask.

**A selected row is reverse video, and `renderRow` drops segment colours on it.** That second half is not optional: under reverse the terminal swaps foreground and background, so a colour set on a span is painted as its *background* and the row grows green and red blocks. The row inverts as one piece and the state glyphs (`⏳❗✅`) still carry what the colour would have said. This is the tempting thing to undo — `TestSelectedRowDropsSegmentColors` is there to catch it.

## One frame, not two boxes

`viewMain` draws a single `drawFrame` around both columns with a `┬ │ ┴` divider, and `renderListView` / `renderPreview` return **unframed** content of exactly the inner size. Two separate boxes put two border characters side by side down the middle of the screen, which reads as a seam. Consequences worth knowing: the two inner widths plus three frame characters are the whole terminal (`m.listInnerWidth()` + right + 3 = `m.width`), and the mouse map has to exclude both the outer frame and the divider — `x == 0` and `x == listInnerWidth+1` are not rows.

## Manual fixed-size rendering

`ui/layout.go` (`padOrTruncate`, `fixedBox`, `drawFrame`, `renderRow`) does the layout by hand instead of using lipgloss containers, because `capture-pane -e` output carries raw ANSI that must be clipped to an exact cell width. Two consequences:

- Every rendered panel must return exactly `panelHeight` lines and exact widths, or the two columns desynchronize.
- **Width compensation is impossible here.** The frame re-pads every line to the column width, so any cell a row subtracts to account for a wide glyph is added straight back. The only workable rule is to emit glyphs whose drawn width equals `ansi.StringWidth` — verify a new glyph before using it (`TestGlyphWidthsAreStable` in `ui/list_test.go` pins the current set). Emoji measure and draw 2; most geometric shapes measure and draw 1.
- **Never wrap a row containing nested styled spans in an outer style.** A nested `lipgloss.Render` emits its own `ESC[0m`, which resets the *background* too, so a colored segment mid-row strips the selection highlight from everything after it. `renderRow` (`ui/layout.go`) builds rows from `rowSeg` values where each span re-states the full style, background included.
