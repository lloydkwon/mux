package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func fakeParked(name string) tmux.ParkedSession {
	sid := "sid-" + name
	return tmux.ParkedSession{ID: sid, Name: name,
		Conversations: []tmux.ParkedConversation{{SessionID: sid, CWD: "/w/" + name}}}
}

// parkTestModel 은 액션 행 둘 아래 세션 셋(claude 둘, 셸 하나)을, 이름순으로 둔다.
func parkTestModel(parked int) Model {
	created := time.Now()
	m := Model{
		tree:  newTreeState(),
		prefs: defaultPreferences(),
		width: 120, height: 30,
		sessions: []tmux.Session{
			{Name: "alpha", Created: created, ActiveCommand: "claude", AITool: "claude", AIState: tmux.AIStateReady},
			{Name: "beta", Created: created, ActiveCommand: "claude", AITool: "claude", AIState: tmux.AIStateWorking},
			{Name: "shell", Created: created, ActiveCommand: "zsh"},
		},
	}
	m.prefs.Sort = sortName
	for i := range parked {
		m.parked = append(m.parked, fakeParked(fmt.Sprintf("old-%d", i)))
	}
	m.applyFilter()
	return m
}

// parkSeams 는 tmux 쪽 시임을 바꿔 끼우고 무엇이 불렸는지 적는다.
type parkSeams struct {
	parked []string // parkSession 이 받은 세션 이름
	forgot []string
	loads  int
	list   []tmux.ParkedSession // loadParked 가 돌려줄 것
	err    error                // parkSession 이 돌려줄 오류
}

func stubParkSeams(t *testing.T) *parkSeams {
	t.Helper()
	s := &parkSeams{}
	oldPark, oldLoad, oldForget := parkSession, loadParked, forgetParked
	parkSession = func(sess tmux.Session) ([]tmux.ParkedSession, error) {
		s.parked = append(s.parked, sess.Name)
		if s.err != nil {
			return s.list, s.err
		}
		s.list = append([]tmux.ParkedSession{fakeParked(sess.Name)}, s.list...)
		return s.list, nil
	}
	loadParked = func() ([]tmux.ParkedSession, error) {
		s.loads++
		return s.list, nil
	}
	forgetParked = func(id string) error {
		s.forgot = append(s.forgot, id)
		var next []tmux.ParkedSession
		for _, p := range s.list {
			if p.ID != id {
				next = append(next, p)
			}
		}
		s.list = next
		return nil
	}
	t.Cleanup(func() { parkSession, loadParked, forgetParked = oldPark, oldLoad, oldForget })
	return s
}

// update 는 msg 를 Update 에 넣고, 돌아온 Cmd 가 있으면 그 메시지까지 한 번 더 넣는다.
func updateThrough(t *testing.T, m Model, msg tea.Msg) (Model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	got := next.(Model)
	if cmd == nil {
		return got, nil
	}
	out := cmd()
	if _, quit := out.(tea.QuitMsg); quit {
		return got, out
	}
	next, _ = got.Update(out)
	return next.(Model), out
}

func kinds(items []listItem) []itemKind {
	var out []itemKind
	for _, it := range items {
		out = append(out, it.kind)
	}
	return out
}

func TestParkedRowAppearsOnlyWhenSomethingIsParked(t *testing.T) {
	for _, it := range parkTestModel(0).items {
		if it.kind == itemParked {
			t.Fatal("a Parked row with nothing parked")
		}
	}
	m := parkTestModel(2)
	if got := kinds(m.items[:4]); got[2] != itemParked || got[3] != itemSession {
		t.Fatalf("rows = %v, want the Parked row right under the two action rows", got)
	}
	if row := ansi.Strip(formatItemRow(m.items[2], false, false, 16, 80, &m.tree)); !strings.Contains(row, "Parked sessions") || !strings.Contains(row, "2 to restore") {
		t.Errorf("parked row = %q", row)
	}
	m.filterText = "alp"
	m.applyFilter()
	for _, it := range m.items {
		if it.kind == itemParked {
			t.Error("the Parked row survived a typed filter, unlike the other action rows")
		}
	}
}

// 첫 목록은 첫 세션에 앉는다 — Parked 행이 끼어 있어도. 그래야 첫 enter 가 팝업이 아니라 attach 다.
func TestFirstLoadLandsOnTheFirstSessionUnderTheParkedRow(t *testing.T) {
	m := parkTestModel(1)
	sessions := m.sessions
	m.sessions, m.landed = nil, false
	m.applyFilter()
	next, _ := m.Update(sessionsLoadedMsg{sessions: sessions})
	got := next.(Model)
	if it := got.currentItem(); it == nil || it.kind != itemSession || it.session.Name != "alpha" {
		t.Fatalf("cursor on %+v, want the first session", it)
	}
}

func TestParkKeyTakesOnlyAClaudeSession(t *testing.T) {
	m := parkTestModel(0)
	m.focusItemSession("shell")
	next, _ := m.updateList(runeKey('p'))
	got := next.(Model)
	if got.mode != modeList || !strings.Contains(got.notice, "only Claude") {
		t.Errorf("p on a shell: mode=%v notice=%q, want a notice and no prompt", got.mode, got.notice)
	}

	m.focusItemSession("alpha")
	next, _ = m.updateList(runeKey('p'))
	got = next.(Model)
	if got.mode != modeConfirmPark || !strings.Contains(ansi.Strip(got.extraBar()), `Park "alpha"?`) {
		t.Errorf("p on a Claude session: mode=%v bar=%q", got.mode, ansi.Strip(got.extraBar()))
	}
}

func TestParkKeyIsOffForAnotherSource(t *testing.T) {
	m := parkTestModel(0)
	m.src = &claudeSource{}
	m.focusItemSession("alpha")
	for _, r := range []rune{'p', 'P'} {
		next, cmd := m.updateList(runeKey(r))
		if got := next.(Model); got.mode != modeList || got.notice != "" || cmd != nil {
			t.Errorf("%c with a source set: mode=%v notice=%q cmd=%v", r, got.mode, got.notice, cmd != nil)
		}
	}
}

func TestBusySessionWarnsThatTheTurnIsCut(t *testing.T) {
	m := parkTestModel(0)
	m.focusItemSession("beta")
	next, _ := m.updateList(runeKey('p'))
	if bar := ansi.Strip(next.(Model).extraBar()); !strings.Contains(bar, "busy") {
		t.Errorf("bar = %q, want a warning for a session mid-turn", bar)
	}
}

// ui 에는 목 러너가 없다. 보관은 돌려준 Cmd 안에서만 tmux 를 만진다.
func TestConfirmParkRunsOnlyInTheCmd(t *testing.T) {
	seams := stubParkSeams(t)
	m := parkTestModel(0)
	m.focusItemSession("alpha")
	m.prefs.Orders["alpha"] = 3
	next, _ := m.updateList(runeKey('p'))

	next, cmd := next.(Model).Update(runeKey('y'))
	if cmd == nil {
		t.Fatal("y returned no Cmd")
	}
	started := cmd()
	next, cmd = next.(Model).Update(started)
	// y 는 확인 줄을 곧바로 닫는다 — 보관이 끝나기까지 몇 초 동안 누른 키가 «취소» 로 먹히지 않게.
	if got := next.(Model); got.mode != modeList || !strings.Contains(got.notice, "parking") {
		t.Fatalf("after y: mode=%v notice=%q, want the list with a parking notice", got.mode, got.notice)
	}
	if len(seams.parked) != 0 {
		t.Fatal("Update parked the session itself instead of returning a Cmd")
	}
	msg := cmd()
	if len(seams.parked) != 1 || seams.parked[0] != "alpha" {
		t.Fatalf("parked = %v, want alpha", seams.parked)
	}
	final, _ := next.(Model).Update(msg)
	got := final.(Model)
	if !strings.Contains(got.notice, `parked "alpha"`) || got.parking != "" {
		t.Errorf("notice=%q parking=%q, want the success said and parking cleared", got.notice, got.parking)
	}
	if got.mode != modeList || len(got.parked) != 1 {
		t.Errorf("after parking: mode=%v parked=%d", got.mode, len(got.parked))
	}
	if got.prefs.Orders["alpha"] != 3 {
		t.Error("parking dropped the order slot — the session is meant to come back with it")
	}
	if got.focusSession != "beta" {
		t.Errorf("focusSession = %q, want the next session down", got.focusSession)
	}
}

func TestParkPromptCancelsOnAnyOtherKey(t *testing.T) {
	seams := stubParkSeams(t)
	m := parkTestModel(0)
	m.focusItemSession("alpha")
	next, _ := m.updateList(runeKey('p'))
	got, _ := updateThrough(t, next.(Model), runeKey('n'))
	if got.mode != modeList || len(seams.parked) != 0 {
		t.Errorf("n: mode=%v parked=%v, want cancelled", got.mode, seams.parked)
	}
}

// VS Code 에서 연 목록은 시딩 필터가 늘 서 있다. 그 아래에 두면 «보관 못 함» 이 보이지 않는다.
func TestParkFailureShowsEvenUnderASeededFilter(t *testing.T) {
	m := parkTestModel(0)
	m.projectDir, m.filterText = "/w", "/w"
	next, _ := m.Update(sessionParkedMsg{name: "alpha", err: tmux.ErrNothingToPark})
	got := next.(Model)
	if bar := ansi.Strip(got.extraBar()); !strings.Contains(bar, "nothing was closed") {
		t.Errorf("bar = %q, want the park failure", bar)
	}
	next, _ = got.updateList(runeKey('j'))
	if n := next.(Model).notice; n != "" {
		t.Errorf("notice = %q after a key, want it cleared", n)
	}
}

func TestParkedRowOpensThePopup(t *testing.T) {
	seams := stubParkSeams(t)
	seams.list = []tmux.ParkedSession{fakeParked("old-0")}
	m := parkTestModel(1)
	m.cursor = 2
	got, msg := updateThrough(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := msg.(parkedLoadedMsg); !ok || got.mode != modeParked {
		t.Fatalf("enter on Parked: msg=%T mode=%v, want the popup", msg, got.mode)
	}
	if seams.loads != 1 {
		t.Errorf("loads = %d, want the list re-read on open", seams.loads)
	}
}

// 더블클릭은 activateCurrent 를 거친다. Parked 행이 그 switch 에 없으면 빈 attach 로 빠져 mux 가 닫힌다.
func TestDoubleClickOnTheParkedRowOpensThePopup(t *testing.T) {
	seams := stubParkSeams(t)
	seams.list = []tmux.ParkedSession{fakeParked("old-0")}
	m := parkTestModel(1)
	y := m.rowY(2)
	first, _ := m.updateList(leftClick(4, y))
	second, cmd := first.(Model).updateList(leftClick(4, y))
	if cmd == nil {
		t.Fatal("double click did nothing")
	}
	if _, quit := cmd().(tea.QuitMsg); quit || second.(Model).AttachName() != "" {
		t.Fatal("double click on the Parked row quit mux")
	}
}

func TestPWithNothingParkedSaysSo(t *testing.T) {
	stubParkSeams(t)
	got, _ := updateThrough(t, parkTestModel(0), runeKey('P'))
	if got.mode != modeList || !strings.Contains(got.notice, "nothing parked") {
		t.Errorf("P with nothing parked: mode=%v notice=%q", got.mode, got.notice)
	}
}

func popupModel(t *testing.T, names ...string) (Model, *parkSeams) {
	t.Helper()
	seams := stubParkSeams(t)
	for _, n := range names {
		seams.list = append(seams.list, fakeParked(n))
	}
	got, _ := updateThrough(t, parkTestModel(0), runeKey('P'))
	if got.mode != modeParked {
		t.Fatalf("P did not open the popup: mode=%v notice=%q", got.mode, got.notice)
	}
	return got, seams
}

func TestPopupEnterRestoresAndQuits(t *testing.T) {
	m, _ := popupModel(t, "one", "two")
	m, _ = updateThrough(t, m, runeKey('j'))
	got, msg := updateThrough(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := msg.(parkedRestoreMsg); !ok {
		t.Fatalf("enter sent %T, want parkedRestoreMsg", msg)
	}
	if p := got.UnparkRequest(); p == nil || p.Name != "two" {
		t.Fatalf("unpark = %+v, want the second entry", p)
	}
	// 복원은 TUI 밖에서 한다 — 대상을 적고 끝낸다.
	if _, cmd := m.Update(msg); cmd == nil {
		t.Fatal("restore did not quit")
	} else if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Fatal("restore did not quit")
	}
}

func TestPopupForgetAsksFirst(t *testing.T) {
	m, seams := popupModel(t, "one", "two")
	m, _ = updateThrough(t, m, runeKey('d'))
	if !strings.Contains(ansi.Strip(m.View()), `Forget "one"?`) {
		t.Fatalf("d did not ask:\n%s", ansi.Strip(m.View()))
	}
	m, _ = updateThrough(t, m, runeKey('n'))
	if len(seams.forgot) != 0 || m.mode != modeParked {
		t.Fatalf("n forgot %v / mode=%v, want nothing forgotten and the popup kept", seams.forgot, m.mode)
	}

	m, _ = updateThrough(t, m, runeKey('d'))
	m, _ = updateThrough(t, m, runeKey('y'))
	if len(seams.forgot) != 1 || seams.forgot[0] != "sid-one" || len(m.parked) != 1 || m.mode != modeParked {
		t.Fatalf("d y: forgot=%v parked=%d mode=%v", seams.forgot, len(m.parked), m.mode)
	}
	m, _ = updateThrough(t, m, runeKey('d'))
	m, _ = updateThrough(t, m, runeKey('y'))
	if m.mode != modeList || len(m.parked) != 0 {
		t.Errorf("forgetting the last one: mode=%v parked=%d, want the popup closed", m.mode, len(m.parked))
	}
}

func TestPopupEscCloses(t *testing.T) {
	m, _ := popupModel(t, "one")
	got, _ := updateThrough(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got.mode != modeList || got.UnparkRequest() != nil {
		t.Errorf("esc: mode=%v unpark=%v", got.mode, got.UnparkRequest())
	}
}

// 상자는 화면 안에 머문다 — 항목이 많아도, 화면이 좁아도.
func TestPopupFitsTheScreen(t *testing.T) {
	var names []string
	for i := range 30 {
		names = append(names, fmt.Sprintf("a-rather-long-parked-session-name-%02d", i))
	}
	m, _ := popupModel(t, names...)
	for _, size := range [][2]int{{60, 12}, {120, 30}, {40, 10}} {
		m.width, m.height = size[0], size[1]
		lines := strings.Split(m.View(), "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > m.width {
				t.Errorf("%dx%d: line %d is %d cells", size[0], size[1], i, w)
			}
		}
	}
	m.width, m.height = 120, 30
	for range 29 {
		m, _ = updateThrough(t, m, runeKey('j'))
	}
	if !strings.Contains(ansi.Strip(m.View()), "name-29") {
		t.Error("the popup did not scroll to the cursor")
	}
}

func TestParkErrorsReadAsNotices(t *testing.T) {
	if got := parkNotice(tmux.ErrNothingToPark); !strings.Contains(got, "nothing was closed") {
		t.Errorf("nothing-to-park notice = %q", got)
	}
	if got := parkNotice(errors.New("boom")); !strings.Contains(got, "boom") {
		t.Errorf("notice = %q, want the error", got)
	}
}

// 보관이 끝나기 전에 p 를 다시 누르면, kill 뒤의 두 번째 보관이 «nothing was closed» 로 거꾸로 보고한다.
func TestParkKeyWaitsForTheOneInFlight(t *testing.T) {
	m := parkTestModel(0)
	m.parking = "alpha"
	m.focusItemSession("beta")
	next, _ := m.updateList(runeKey('p'))
	if got := next.(Model); got.mode != modeList || !strings.Contains(got.notice, "still parking") {
		t.Errorf("p while parking: mode=%v notice=%q", got.mode, got.notice)
	}
}

// 늦게 온 취소가 그 사이 연 다른 입력(필터 등)을 닫지 않는다.
func TestLateParkCancelLeavesAnotherModeAlone(t *testing.T) {
	m := parkTestModel(0)
	m.mode = modeFilter
	next, _ := m.Update(sessionParkedMsg{cancelled: true})
	if got := next.(Model); got.mode != modeFilter {
		t.Errorf("mode = %v, want the filter still open", got.mode)
	}
}

func TestParkedLoadErrorIsANotice(t *testing.T) {
	next, _ := parkTestModel(0).Update(parkedLoadedMsg{err: errors.New("parse parked sessions: bad")})
	if got := next.(Model); got.mode != modeList || !strings.Contains(got.notice, "bad") {
		t.Errorf("mode=%v notice=%q, want the error said in the list", got.mode, got.notice)
	}
}

// Parked 행이 생기면 아래 행이 한 칸씩 밀린다. 커서는 인덱스가 아니라 같은 세션에 남는다.
func TestParkedRowAppearingKeepsTheCursorOnItsSession(t *testing.T) {
	m := parkTestModel(0)
	m.focusItemSession("shell")
	next, _ := m.Update(sessionParkedMsg{name: "alpha", parked: []tmux.ParkedSession{fakeParked("alpha")}})
	got := next.(Model)
	if it := got.currentItem(); it == nil || it.session == nil || it.session.Name != "shell" {
		t.Errorf("cursor on %+v, want it still on shell", it)
	}
	if got.items[2].kind != itemParked {
		t.Errorf("rows = %v, want the Parked row to have appeared", kinds(got.items))
	}
}
