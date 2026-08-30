package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lloydkwon/mux/tmux"
)

// The first-run offer: `go install` delivers the binary but not the tmux side
// of mux — the popup bind, the panel bind and the hooks — and the two setup
// commands are exactly the kind of thing a new user has no way to know about.
// So the program itself asks, once, the first time it opens with a config that
// carries no mux region.
//
// Detection is automatic, writing is not: the offer edits ~/.tmux.conf only on
// an explicit `y`. And it follows modeHelp's shape rather than the sub-model
// pattern — two keys, no state of its own.

// OfferSetupIfNeeded puts the model on the first-run offer when the tmux config
// carries no mux-owned region and the offer has never been answered.
//
// Called from cmd on the plain `mux` path only. `mux new` opens on the create
// prompt — someone mid-gesture is not someone to interrupt — and the offer
// waits for the next bare launch instead.
func (m Model) OfferSetupIfNeeded() Model {
	if m.prefs.SetupOffered || tmux.IntegrationInstalled() {
		return m
	}
	m.mode = modeSetupOffer
	return m
}

// setupInstalledMsg reports the accepted offer's install. The work happens in a
// tea.Cmd, never inside Update: `ui` has no mock runner, and tests drive Update
// directly — an inline install would write into the developer's own tmux config.
type setupInstalledMsg struct{ err error }

func runSetupInstall() tea.Msg {
	return setupInstalledMsg{err: tmux.InstallIntegration()}
}

func (m Model) updateSetupOffer(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil // ticks keep refreshing behind the page, never answer it
	}

	switch key.String() {
	case "y", "Y", "enter":
		return m.answerSetupOffer(runSetupInstall)
	case "n", "N", "esc", "q":
		return m.answerSetupOffer(nil)
	case "ctrl+c":
		// Bailing out is not an answer: the offer comes back next launch.
		return m, tea.Quit
	}
	return m, nil
}

// answerSetupOffer records that the question was asked and answered — both
// ways, because asking again on every launch after a `n` is nagging.
func (m Model) answerSetupOffer(install tea.Cmd) (tea.Model, tea.Cmd) {
	m.mode = modeList
	m.prefs.SetupOffered = true
	if err := savePreferences(m.prefs); err != nil {
		m.err = err
	}
	return m, install
}

func renderSetupOfferBody() string {
	lines := []string{
		titleStyle.Render(" tmux 연동 설정"),
		"",
		"mux의 tmux 연동이 아직 설정되지 않았습니다.",
		"기본 키로 설정하면 다음을 쓸 수 있습니다:",
		"",
		// One item per line: these key names are longer than helpKeyRow's
		// two-column budget, and a truncated key is worse than a longer page.
		"  prefix + m        세션 팝업",
		"  prefix + a        사이드 패널 (창마다 자동 유지)",
		"  prefix + Tab      패널 진입 / 복귀",
		"  M-↑ M-↓ M-Enter   패널 이동 / 선택",
		"",
		helpStyle.Render("~/.tmux.conf에 mux 전용 블록을 쓰고, 실행 중인 서버에 바로"),
		helpStyle.Render("적용합니다. 키는 setup-keybind / setup-panel 명령으로 바꿀 수 있습니다."),
		"",
		titleStyle.Render(" y") + " 설정하기   " + titleStyle.Render("n") + " 다음에 (나중에 `mux setup`)",
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewSetupOffer() string {
	return fixedBox(renderSetupOfferBody(), m.width, m.height)
}
