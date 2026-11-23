package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

// 보관(p)과 보관함(P). 지금은 필요 없는 Claude·opencode 세션을 닫았다가, 나중에 같은 대화로 되살린다.
// 저장과 tmux 쪽 일은 tmux/park.go, 설계 이유는 docs/design/park.md.

// tmux 쪽 시임이다. ui 에는 목 러너가 없어서, 테스트는 이것을 바꿔 끼워 «Update 안에서는 부르지
// 않고 돌려준 Cmd 에서만 부른다» 를 확인한다.
var (
	parkSession  = tmux.ParkSession
	loadParked   = tmux.LoadParked
	forgetParked = tmux.ForgetParked
)

const (
	actionParkedLabel = "Parked sessions"
	// parkedGlyph 는 Parked 행의 아이콘이다. 도구 아이콘(◇ 는 agy)과 겹치지 않는 1칸 글리프 —
	// TestGlyphWidthsAreStable 이 폭을 고정한다.
	parkedGlyph = "≡"

	// parkedPopupMaxWidth 는 보관함 상자의 안쪽 폭 상한이다. 넓은 화면에서 한 줄이 끝없이 늘지 않게.
	parkedPopupMaxWidth = 100
	// 상자의 테두리(위아래·좌우 1)와 viewWithOverlay 의 안쪽 여백(위아래 1, 좌우 2)이 먹는 칸.
	parkedPopupChromeWidth  = 6
	parkedPopupChromeHeight = 4
	// 제목·빈 줄·빈 줄·안내 줄 — 상자 안에서 행이 아닌 줄.
	parkedPopupFixedLines = 4
)

// parkStartedMsg 는 확인 줄에서 y 를 눌렀다는 알림이다. 보관 자체는 이것을 받은 뒤의 Cmd 에서 한다 —
// 끝나기까지 최대 3초(PID 대기)가 걸리는데, 그동안 확인 줄이 떠 있으면 사이에 누른 키가 «취소» 로 먹힌다.
type parkStartedMsg struct{ session tmux.Session }

// sessionParkedMsg 는 보관 확인이 끝났다는 알림이다. cancelled 면 아무것도 하지 않았다.
type sessionParkedMsg struct {
	name      string
	parked    []tmux.ParkedSession
	err       error
	cancelled bool
}

// parkedLoadedMsg 는 보관함을 다시 읽은 결과다. 팝업을 열 때 읽는다 — 다른 TUI 가 그사이 보관했을 수 있다.
type parkedLoadedMsg struct {
	parked []tmux.ParkedSession
	err    error
}

// parkedForgottenMsg 는 팝업에서 항목 하나를 지운 뒤 다시 읽은 보관함이다.
type parkedForgottenMsg struct {
	parked []tmux.ParkedSession
	err    error
}

type parkedRestoreMsg struct{ p tmux.ParkedSession }

type parkedClosedMsg struct{}

// loadParkedCmd 는 보관함을 다시 읽는다.
func loadParkedCmd() tea.Msg {
	list, err := loadParked()
	return parkedLoadedMsg{parked: list, err: err}
}

// parkNotice 는 보관 실패를 한 줄로 말한다. 세션이 닫히지 않았다는 것까지 — 그게 사용자가 알아야 할 것이다.
func parkNotice(err error) string {
	if errors.Is(err, tmux.ErrNothingToPark) {
		return "no conversation to resume in this session — nothing was closed"
	}
	return "park failed: " + err.Error()
}

// parkableSessionRow 는 p 가 받는 행인지 — Claude 나 opencode 가 도는 세션 행이다. 다른 도구는 대화 id 를
// 알아낼 길이 없다.
func parkableSessionRow(it *listItem) bool {
	if it == nil || it.kind != itemSession || it.session == nil {
		return false
	}
	tool, ok := tmux.SessionAITool(*it.session)
	return ok && tmux.ParkableTool(tool.Name)
}

// parkToolLabel 은 확인 줄에 쓰는 도구 이름이다.
func parkToolLabel(s tmux.Session) string {
	if tool, ok := tmux.SessionAITool(s); ok && tool.Name == "opencode" {
		return "opencode"
	}
	return "Claude"
}

// neighbourSession 은 name 바로 아래 세션, 없으면 바로 위 세션이다. 보관한 세션이 목록에서 빠진 뒤
// 커서가 앉을 곳이다.
func neighbourSession(sessions []tmux.Session, name string) string {
	for i, s := range sessions {
		if s.Name != name {
			continue
		}
		if i+1 < len(sessions) {
			return sessions[i+1].Name
		}
		if i > 0 {
			return sessions[i-1].Name
		}
	}
	return ""
}

// confirmParkModel 은 보관 확인 줄이다. kill 확인과 같은 자리(extraBar)에 뜬다.
type confirmParkModel struct {
	session tmux.Session
}

func newConfirmParkModel(s tmux.Session) confirmParkModel {
	return confirmParkModel{session: s}
}

func (m confirmParkModel) Update(msg tea.Msg) (confirmParkModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "Y":
		s := m.session
		return m, func() tea.Msg { return parkStartedMsg{session: s} }
	default:
		// 다른 키는 전부 취소 — kill 확인과 같다.
		return m, func() tea.Msg { return sessionParkedMsg{cancelled: true} }
	}
}

func (m confirmParkModel) View() string {
	label := parkToolLabel(m.session)
	text := fmt.Sprintf("Park %q? closes the session — its %s conversation comes back from P (y/N)", m.session.Name, label)
	switch m.session.AIState {
	case tmux.AIStateWorking, tmux.AIStateApproval, tmux.AIStateShell:
		// 닫으면 진행 중인 턴(또는 승인 대기, 배경 명령)이 끊긴다. 대화는 돌아와도 그 턴은 돌아오지 않는다.
		// opencode 의 TUI 는 서비스의 클라이언트라 턴이 서비스에서 이어질 수도 있다(재지 않았다) — «may».
		cut := "will"
		if label == "opencode" {
			cut = "may"
		}
		text += " · " + label + " is busy, this turn " + cut + " be cut"
	}
	return helpKeyStyle.Render(text)
}

// parkedModel 은 보관함 팝업이다. 고르면 되살리고(enter), 필요 없으면 지운다(d).
type parkedModel struct {
	items  []tmux.ParkedSession
	cursor int
	// forgetting 은 «지울까요?» 를 묻는 중이다. 다음 키가 y 면 지우고, 아니면 그만둔다.
	forgetting bool
}

func newParkedModel(items []tmux.ParkedSession) parkedModel {
	return parkedModel{items: items}
}

// withItems 는 목록을 바꾼 새 모델이다. 커서는 같은 자리에서 끝을 넘지 않게 둔다.
func (m parkedModel) withItems(items []tmux.ParkedSession) parkedModel {
	m.items = items
	m.cursor = clampInt(m.cursor, 0, max(0, len(items)-1))
	m.forgetting = false
	return m
}

func (m parkedModel) Update(msg tea.Msg) (parkedModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if len(m.items) == 0 {
		return m, func() tea.Msg { return parkedClosedMsg{} }
	}
	if m.forgetting {
		m.forgetting = false
		if s := key.String(); s != "y" && s != "Y" {
			return m, nil
		}
		id := m.items[m.cursor].ID
		return m, func() tea.Msg {
			err := forgetParked(id)
			list, loadErr := loadParked()
			return parkedForgottenMsg{parked: list, err: errors.Join(err, loadErr)}
		}
	}
	switch key.String() {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(m.items)-1, m.cursor+1)
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.items) - 1
	case "enter":
		p := m.items[m.cursor]
		return m, func() tea.Msg { return parkedRestoreMsg{p: p} }
	case "d", "x":
		m.forgetting = true
	case "esc", "q", "P":
		return m, func() tea.Msg { return parkedClosedMsg{} }
	}
	return m, nil
}

// View 는 상자 안쪽을 그린다. 크기는 화면에 묶는다 — viewWithOverlay 는 크기 상한이 없어서, 줄이
// 길거나 많으면 상자가 화면을 넘는다(help.go 가 fixedBox 를 쓰는 이유). 그래서 줄마다 폭을 맞추고
// 행 수를 화면 높이로 자른다.
func (m parkedModel) View(screenWidth, screenHeight int) string {
	width := max(1, min(parkedPopupMaxWidth, screenWidth-parkedPopupChromeWidth))
	rows := max(1, screenHeight-parkedPopupChromeHeight-parkedPopupFixedLines)
	rows = min(rows, len(m.items))

	lines := []string{
		padOrTruncate(titleStyle.Render(fmt.Sprintf("Parked sessions · %d", len(m.items))), width),
		strings.Repeat(" ", width),
	}
	offset := listOffset(m.cursor, rows)
	nameWidth := parkedNameWidth(m.items)
	for i := offset; i < len(m.items) && i < offset+rows; i++ {
		lines = append(lines, parkedRow(m.items[i], i == m.cursor, nameWidth, width))
	}
	lines = append(lines, strings.Repeat(" ", width))

	footer := helpStyle.Render("enter restore • d forget • esc close")
	if m.forgetting && m.cursor < len(m.items) {
		footer = errorStyle.Render(fmt.Sprintf("Forget %q? the transcript stays on disk (y/N)", m.items[m.cursor].Name))
	}
	lines = append(lines, padOrTruncate(footer, width))
	return strings.Join(lines, "\n")
}

// parkedNameWidth 는 이름 칸의 폭이다 — 가장 긴 이름에 맞추되 세션 목록과 같은 상·하한 안에서.
// 고정 폭이면 짧은 이름 뒤에 구멍이 생기고, 눈이 그 구멍을 건너뛰어야 한다(sessionNameWidth 와 같은 이유).
func parkedNameWidth(items []tmux.ParkedSession) int {
	longest := 0
	for _, p := range items {
		longest = max(longest, ansi.StringWidth(p.Name))
	}
	return clampInt(longest, sessionNameMin, sessionNameMax)
}

// parkedRow 는 보관 항목 한 줄이다: 이름, 보관한 지 얼마, 디렉터리, 대화 수(둘 이상일 때), 메모.
func parkedRow(p tmux.ParkedSession, selected bool, nameWidth, width int) string {
	marker := "  "
	if selected {
		marker = "▶ "
	}
	dir := ""
	if len(p.Conversations) > 0 {
		dir = p.Conversations[0].CWD
	}
	segs := []rowSeg{
		{text: marker, color: colorAccent},
		{text: fitCells(p.Name, nameWidth), color: colorAccent},
		// 나이는 compactAgo — 일 단위까지 가고, 0 이면 빈칸이라 골든이 시각에 묶이지 않는다.
		{text: fmt.Sprintf("  %4s", compactAgo(p.ParkedAt)), color: colorMuted, atom: true},
		{text: "  " + shortenPath(dir)},
	}
	if n := len(p.Conversations); n > 1 {
		segs = append(segs, rowSeg{text: fmt.Sprintf("  %d conv", n), color: colorMuted, atom: true})
	}
	if p.Note != "" {
		segs = append(segs, rowSeg{text: "  " + noteGlyph + " " + p.Note, color: colorMuted})
	}
	return renderRow(segs, width, selected)
}

// renderParkedPreview 는 커서가 Parked 행에 있을 때의 오른쪽 칸이다 — 무엇이 보관돼 있고, enter 가
// 무엇을 하는지.
func renderParkedPreview(parked []tmux.ParkedSession, innerWidth, innerHeight int) string {
	lines := make([]string, innerHeight)
	for i := range lines {
		lines[i] = strings.Repeat(" ", innerWidth)
	}
	text := []string{
		lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(padOrTruncate("[ Parked sessions ]", innerWidth)),
		lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", innerWidth)),
		"",
		padOrTruncate("Claude/opencode sessions closed with p, to resume later.", innerWidth),
		helpStyle.Render(padOrTruncate("Enter opens the list to restore or forget one.", innerWidth)),
		"",
	}
	nameWidth := parkedNameWidth(parked)
	for _, p := range parked {
		text = append(text, parkedRow(p, false, nameWidth, innerWidth))
	}
	for i := 0; i < len(text) && i < innerHeight; i++ {
		lines[i] = padOrTruncate(text[i], innerWidth)
	}
	return strings.Join(lines, "\n")
}

// setParked 는 보관함을 바꾸고 목록을 다시 세운다. Parked 행이 생기거나 사라지면 아래 행이 한 칸씩
// 밀리므로, 커서는 인덱스가 아니라 같은 세션에 다시 앉힌다.
func (m *Model) setParked(list []tmux.ParkedSession) {
	keep := m.currentSessionName()
	m.parked = list
	m.rebuildItems()
	m.focusItemSession(keep)
}

// openParked 는 보관함을 다시 읽어 팝업을 연다. 비었으면 그렇다고 말한다.
func (m Model) openParked() (tea.Model, tea.Cmd) {
	if m.writesBlocked() {
		return m, nil
	}
	return m, loadParkedCmd
}

func (m Model) updateConfirmPark(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.confirmParkMod, cmd = m.confirmParkMod.Update(msg)
	return m, cmd
}

func (m Model) updateParked(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.parkedMod, cmd = m.parkedMod.Update(msg)
	return m, cmd
}

func (m Model) viewParked() string {
	return m.viewWithOverlay(m.parkedMod.View(m.width, m.height))
}

// handleParkMsg 는 보관·보관함 메시지를 처리한다. 해당 메시지가 아니면 handled 가 거짓이다.
func (m Model) handleParkMsg(msg tea.Msg) (_ tea.Model, _ tea.Cmd, handled bool) {
	switch msg := msg.(type) {
	case parkStartedMsg:
		next, cmd := m.startPark(msg.session)
		return next, cmd, true
	case sessionParkedMsg:
		next, cmd := m.finishPark(msg)
		return next, cmd, true
	case parkedLoadedMsg:
		return m.openParkedList(msg), nil, true
	case parkedForgottenMsg:
		return m.afterForget(msg), nil, true
	case parkedRestoreMsg:
		// 세션 생성과 attach 는 TUI 밖(cmd/mux)에서 한다. 여기서는 대상을 적고 끝낸다.
		p := msg.p
		m.unparkTarget = &p
		return m, tea.Quit, true
	case parkedClosedMsg:
		m.mode = modeList
		return m, nil, true
	}
	return m, nil, false
}

// startPark 는 확인 줄을 닫고 보관을 Cmd 로 보낸다. 끝날 때까지 parking 이 그 세션을 적어 둔다 —
// 그 사이 p 를 다시 누르면 kill 뒤의 두 번째 보관이 «nothing was closed» 로 거꾸로 보고한다.
func (m Model) startPark(s tmux.Session) (tea.Model, tea.Cmd) {
	m.mode = modeList
	m.parking = s.Name
	m.notice = fmt.Sprintf("parking %q…", s.Name)
	return m, func() tea.Msg {
		list, err := parkSession(s)
		return sessionParkedMsg{name: s.Name, parked: list, err: err}
	}
}

func (m Model) finishPark(msg sessionParkedMsg) (tea.Model, tea.Cmd) {
	if msg.cancelled {
		// 늦게 와도 다른 모드(그새 연 생성·필터)를 닫지 않게, 확인 줄일 때만 목록으로 돌린다.
		if m.mode == modeConfirmPark {
			m.mode = modeList
		}
		return m, nil
	}
	m.parking = ""
	if msg.parked != nil || msg.err == nil {
		m.setParked(msg.parked)
	}
	if msg.err != nil {
		m.notice = parkNotice(msg.err)
		return m, nil
	}
	m.notice = fmt.Sprintf("parked %q — P brings it back", msg.name)
	// Orders 는 지우지 않는다(kill 과 다르다). 같은 이름으로 돌아오면 순서 칸도 같이 돌아온다.
	m.focusSession = neighbourSession(m.filtered, msg.name)
	return m, m.loadSessionsCmd()
}

func (m Model) openParkedList(msg parkedLoadedMsg) Model {
	if msg.err != nil {
		m.notice = "parked sessions: " + msg.err.Error()
		return m
	}
	m.setParked(msg.parked)
	if len(msg.parked) == 0 {
		m.notice = "nothing parked — p on a Claude or opencode session parks it"
		return m
	}
	m.mode = modeParked
	m.parkedMod = newParkedModel(msg.parked)
	return m
}

func (m Model) afterForget(msg parkedForgottenMsg) Model {
	if msg.err != nil {
		// 팝업 안에는 extraBar 가 없다. 닫고 목록에서 말한다.
		m.mode = modeList
		m.notice = "forget failed: " + msg.err.Error()
		return m
	}
	m.setParked(msg.parked)
	if len(msg.parked) == 0 {
		m.mode = modeList
		return m
	}
	m.parkedMod = m.parkedMod.withItems(msg.parked)
	return m
}

// UnparkRequest 는 TUI 가 끝난 뒤 되살릴 보관 항목이다. 없으면 nil.
func (m Model) UnparkRequest() *tmux.ParkedSession { return m.unparkTarget }
