package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/lloydkwon/mux/tmux"
	"github.com/lloydkwon/mux/ui"
)

// version is injected at build time via -ldflags by the Makefile and by
// goreleaser. Builds that skip those — notably `go install <module>@<version>`
// — leave it at defaultVersion and fall back to resolveVersion.
var version = defaultVersion

const defaultVersion = "dev"

// resolveVersion reports the version to display.
//
// An injected value always wins: release builds stamp the exact tag, and
// nothing Go records is more authoritative than that. Otherwise the module
// version Go recorded is used — since Go 1.24 that covers plain local builds
// too, derived from the nearest VCS tag. The bare revision is a last resort
// for a build with no reachable tag. Only a build with none of these stays
// "dev".
//
// The leading "v" is trimmed from every source so that all build paths agree:
// goreleaser passes a bare "0.2.0" while git describe and the module version
// both yield "v0.2.0".
func resolveVersion(injected string, info *debug.BuildInfo, ok bool) string {
	return strings.TrimPrefix(rawVersion(injected, info, ok), "v")
}

func rawVersion(injected string, info *debug.BuildInfo, ok bool) string {
	if injected != "" && injected != defaultVersion {
		return injected
	}
	if !ok || info == nil {
		return defaultVersion
	}

	// "(devel)" is what Go records when the build maps to no module version
	// and no VCS tag is reachable — fall through to the revision.
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return defaultVersion
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}

func main() {
	info, ok := debug.ReadBuildInfo()

	rootCmd := &cobra.Command{
		Use:     "mux",
		Short:   "TUI tmux session manager",
		Version: resolveVersion(version, info, ok),
		RunE:    func(cmd *cobra.Command, args []string) error { return runRoot() },
		// Suppress cobra's default completion and help subcommands
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	rootCmd.SetVersionTemplate("mux {{.Version}}\n")

	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Open mux on the new-session prompt and attach to what it creates",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(ui.NewSessionModel())
		},
	}

	popupCmd := &cobra.Command{
		Use:   "popup",
		Short: "Open mux as a tmux popup overlay",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tmux.OpenPopup()
		},
	}

	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up the whole tmux integration with default keys",
		Long: "Runs setup-keybind and setup-panel in one go, with their default keys.\n" +
			"Use the individual commands instead to choose your own keys.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := tmux.SetupKeybind(tmux.DefaultBindKey); err != nil {
				return err
			}
			fmt.Println()
			return tmux.SetupPanel(tmux.DefaultPanelKey, tmux.DefaultFocusKey)
		},
	}

	setupKeybindCmd := &cobra.Command{
		Use:   "setup-keybind [key]",
		Short: fmt.Sprintf("Add popup keybinding to tmux config (default: %s)", tmux.DefaultBindKey),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := tmux.DefaultBindKey
			if len(args) > 0 {
				key = args[0]
			}
			return tmux.SetupKeybind(key)
		},
	}

	var statusJSON bool
	setupPanelCmd := &cobra.Command{
		Use: "setup-panel [key] [focus-key]",
		Short: fmt.Sprintf("Add the panel keybindings and hooks to tmux config (defaults: %s, %s)",
			tmux.DefaultPanelKey, tmux.DefaultFocusKey),
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, focusKey := tmux.DefaultPanelKey, tmux.DefaultFocusKey
			if len(args) > 0 {
				key = args[0]
			}
			if len(args) > 1 {
				focusKey = args[1]
			}
			return tmux.SetupPanel(key, focusKey)
		},
	}

	var statusWatch bool
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show AI session summary for tmux statusbar",
		RunE: func(cmd *cobra.Command, args []string) error {
			if statusJSON && statusWatch {
				return runStatusJSONWatch()
			}
			if statusJSON {
				return runStatusJSON()
			}
			return runStatus()
		},
	}
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "output session list as JSON")
	statusCmd.Flags().BoolVar(&statusWatch, "watch", false,
		"with --json: keep running, print a JSON line whenever the sessions change")

	switchCmd := &cobra.Command{
		Use:   "switch <session>",
		Short: "Switch the Windows Terminal tmux client to a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := tmux.SwitchWTClient(args[0])
			if err != nil {
				// WT 클라이언트가 없어도 mux TUI가 목록 화면에 떠 있으면
				// TUI에게 attach를 요청해 같은 결과를 낸다 (위젯 클릭 경로)
				if ui.RequestRemoteAttach(args[0]) == nil {
					return nil
				}
			}
			return err
		},
	}

	watchCmd := &cobra.Command{
		Use:   "watch",
		Short: "Live AI session panel for a dedicated tmux pane",
		RunE: func(cmd *cobra.Command, args []string) error {
			reviveAndRemember()
			return ui.RunWatch()
		},
	}

	var panelTarget string
	var panelAuto bool
	var panelFocus bool
	panelCmd := &cobra.Command{
		Use:   "panel",
		Short: "Toggle the AI session panel pane in a tmux window",
		RunE: func(cmd *cobra.Command, args []string) error {
			if panelFocus {
				return tmux.FocusPanel(panelTarget)
			}
			return runPanelAuto(panelTarget, panelAuto)
		},
		// Same reason navCmd carries it: the error from this command is read by
		// tmux, and cobra's whole usage page on a status line helps nobody.
		SilenceUsage: true,
	}
	panelCmd.Flags().StringVarP(&panelTarget, "target", "t", "",
		"pane whose window to toggle (default: current)")
	panelCmd.Flags().BoolVar(&panelAuto, "auto", false,
		"for hooks: skip windows whose session is only viewed from VS Code")
	panelCmd.Flags().BoolVar(&panelFocus, "focus", false,
		"move focus into the panel, or back out of it; opens and closes nothing")
	panelCmd.MarkFlagsMutuallyExclusive("auto", "focus")

	var borderTarget string
	var borderWidth int
	borderCmd := &cobra.Command{
		Use:   "border",
		Short: "Print the session summary tmux draws above a pane",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBorder(borderTarget, borderWidth)
		},
		SilenceUsage: true,
	}
	borderCmd.Flags().StringVarP(&borderTarget, "target", "t", "",
		"pane to describe (default: current)")
	borderCmd.Flags().IntVarP(&borderWidth, "width", "w", 0,
		"cells available on the border line")

	var navTarget string
	navCmd := &cobra.Command{
		Use:   "nav <up|down|top|bottom|enter>",
		Short: "Move the panel's selection without leaving the pane you are in",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tmux.NavPanel(navTarget, args[0])
		},
		// The error is the direction being wrong, which is a typo in the user's
		// tmux.conf — printing cobra's whole usage page onto the status line
		// helps nobody.
		SilenceUsage: true,
	}
	navCmd.Flags().StringVarP(&navTarget, "target", "t", "",
		"pane whose window holds the panel (default: current)")

	rootCmd.AddCommand(newCmd, popupCmd, setupCmd, setupKeybindCmd, setupPanelCmd, statusCmd,
		switchCmd, watchCmd, panelCmd, navCmd, borderCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runRoot opens mux, from a popup when it can get one.
//
// `prefix + m` draws mux in a display-popup: no panel beside it, no border title
// above it, just the list. Typed in a terminal that is not in tmux, mux drew
// itself in that terminal instead — the same program, a different shape. This
// makes the two agree by attaching first and popping up on the client that
// creates.
//
// A bootstrap that cannot happen is not a reason to refuse to run: every failure
// falls through to drawing here, which is exactly what mux did before.
// reviveAndRemember 는 tmux 서버가 소켓만 잃었다면 되살리고, 닿아 있는 서버를
// 다음을 위해 적어 둔다.
//
// 둘 다 조용히 실패한다. 소켓이 멀쩡한 평소에는 파일 stat 한 번으로 끝나고,
// 되살릴 것이 없을 때 아무 말도 하지 않아야 한다 — 사용자가 방금 친 명령의 답으로
// "복구할 게 없었다"를 출력하는 것은 소음이다.
func reviveAndRemember() {
	tmux.ReviveServer()
	tmux.RememberServer()
}

func runRoot() error {
	reviveAndRemember()
	if shouldBootstrap() {
		// Returns only on failure — attach-session replaces this process.
		_ = tmux.AttachAndPopup()
	}
	// The first-run offer belongs to the bare `mux` launch only: `mux new`
	// opens mid-gesture, and the popup/panel surfaces exist because setup
	// already ran.
	return runTUI(ui.NewModel().OfferSetupIfNeeded())
}

// shouldBootstrap reports whether this mux should hand itself to a popup.
//
// Not when already inside tmux: there `prefix + m` is the popup and a bare `mux`
// in a pane is a deliberate choice. Not when the guard is set — that is the
// popup's own child, and bootstrapping there would open popups forever.
//
// And not when there are no sessions. There would be nothing to attach to, and
// creating one to attach to means naming it on the user's behalf; the list mux
// draws in that case already offers `New tmux session`, which asks.
func shouldBootstrap() bool {
	if os.Getenv(tmux.BootstrapGuardEnv) != "" || os.Getenv("TMUX") != "" {
		return false
	}
	sessions, err := tmux.ListSessions()
	return err == nil && len(sessions) > 0
}

func runStatus() error {
	sessions, err := tmux.ListSessions()
	if err != nil {
		return err
	}

	var parts []string
	for _, s := range sessions {
		tool, ok := tmux.SessionAITool(s)
		if !ok {
			continue
		}
		// Same badge rule as the list: a live state replaces the tool icon, so
		// the status bar alone shows which session is waiting on you.
		icon := tool.Icon
		if g := s.AIState.Icon(); g != "" {
			icon = g
		}
		parts = append(parts, icon)
	}

	if len(parts) == 0 {
		return nil // no AI sessions, output nothing
	}

	fmt.Print(fmt.Sprintf(" %s ", joinWith(parts, " ")))
	return nil
}

// defaultBorderWidth is what a border line is fitted to when tmux did not say.
// Wide enough to be worth printing, narrow enough that a real pane clips little.
const defaultBorderWidth = 80

// runPanelAuto opens the panel, and on the hook path never reports a failure.
//
// `--auto` is what the seven panelHooks run, `client-resized` among them. Those
// fire constantly, and a failing run-shell paints the status line of *every*
// attached client every time — so one thing going wrong once goes wrong loudly
// and forever.
//
// What goes wrong is almost always that the world moved on. tmux captures
// `#{pane_id}` when the hook fires and runs the command a moment later, so the
// pane named on the command line may already be gone by the time this process
// starts. Measured: `display-message -p -t %53` on a dead pane exits 0 and
// prints a space, which panelWindow correctly refuses — and that refusal became
// `'mux panel --auto -t %53' returned 1` on the user's status line, twice, for
// two panes that had closed a moment earlier.
//
// Nothing on this path is worth telling anyone about. The ensure already stands
// down silently for its three designed cases — VS Code, a narrow window, a panel
// closed by hand — so silence when the target has vanished is consistent rather
// than new. Same shape as runBorder below, and swallowed here rather than in
// tmux/ for the same reason: the package keeps reporting honestly, and "do not
// paint this on the status line" is the caller's policy.
//
// Pressing prefix + a still reports. That is an answer to a key the user just
// pressed, and it has somewhere to go.
func runPanelAuto(target string, auto bool) error {
	// 훅 경로라 왕복을 늘리지 않는다 — 되살리기만 하고 기록은 하지 않는다.
	// 기록은 TUI 와 패널 시작이 맡고, 패널은 창 이벤트마다 새로 뜬다.
	tmux.ReviveServer()
	err := tmux.TogglePanel(target, auto)
	if auto {
		return nil
	}
	return err
}

// runBorder prints the one line tmux draws above a pane.
//
// Failure prints an empty line and exits 0, deliberately. tmux inserts the last
// line of this command's output into the border and re-runs it every few
// seconds, so an error message here would not be reported anywhere a user can
// act on it — it would simply be painted across the top of the pane they are
// working in, and stay there.
func runBorder(target string, width int) error {
	if width <= 0 {
		width = defaultBorderWidth
	}
	session, err := tmux.SessionForTarget(target)
	if err != nil {
		fmt.Println()
		return nil
	}
	fmt.Println(ui.BorderLine(session, width))
	return nil
}

// sessionJSON is the public schema for `mux status --json`, consumed by
// external widgets. Field names are pinned by TestSessionsJSON — change them
// only with the consumers.
type sessionJSON struct {
	Name     string `json:"name"`
	Attached bool   `json:"attached"`
	Dir      string `json:"dir"`
	Branch   string `json:"branch,omitempty"`
	Worktree bool   `json:"worktree,omitempty"` // Directory가 연결된 git worktree인지
	Tool     string `json:"tool,omitempty"`
	// State는 AIState.String()을 그대로 쓴다. 주의: AIStateReady가 "waiting"으로
	// 직렬화된다 (Claude 원시 상태 "waiting"은 approval로 매핑됨). 소비자는
	// "waiting"을 완료/입력 대기(✅)로 해석해야 한다.
	State      string `json:"state,omitempty"`
	WaitingFor string `json:"waitingFor,omitempty"`
	Since      int64  `json:"since,omitempty"` // 상태 시작 시각, unix millis
	PID        int    `json:"pid,omitempty"`   // 상태를 발행한 프로세스 (소비자의 프로세스 검사용)
	// 이 세션에 통합 터미널로 붙어 있는 VS Code 창의 워크스페이스 폴더.
	// 세션 dir과 다를 수 있다 (예: 상위 폴더를 연 창). 없으면 생략.
	VSCodeDir string `json:"vscodeDir,omitempty"`
}

func sessionsJSON(sessions []tmux.Session, vscodeDirs map[string][]string) ([]byte, error) {
	out := make([]sessionJSON, 0, len(sessions)) // 빈 목록도 null이 아닌 []로
	for _, s := range sessions {
		j := sessionJSON{
			Name:     s.Name,
			Attached: s.Attached,
			Dir:      s.Directory,
			Branch:   s.GitBranch,
			Worktree: s.IsWorktree,
			State:    s.AIState.String(),
		}
		if t, ok := tmux.SessionAITool(s); ok {
			j.Tool = t.Name
		}
		if s.AIState == tmux.AIStateApproval {
			j.WaitingFor = s.AIWaitingFor
		}
		if !s.AISince.IsZero() {
			j.Since = s.AISince.UnixMilli()
		}
		j.PID = s.AIPID
		// VS Code 창 매핑은 세션의 작업 디렉터리가 그 창의 워크스페이스 폴더
		// 안(또는 동일)인 창만 인정한다. 다른 프로젝트 창의 터미널에서 이 세션에
		// attach만 한 경우(구경)는 후보에서 제외 — 세션마다 후보가 여럿일 수
		// 있으므로(자기 창 + 구경 창) 조건에 맞는 첫 창을 고른다.
		for _, vdir := range vscodeDirs[s.Name] {
			if j.Dir == vdir || strings.HasPrefix(j.Dir, vdir+"/") {
				j.VSCodeDir = vdir
				break
			}
		}
		out = append(out, j)
	}
	return json.Marshal(out)
}

func runStatusJSON() error {
	sessions, err := tmux.ListSessions()
	if err != nil {
		return err
	}
	b, err := sessionsJSON(sessions, tmux.VSCodeClientDirs())
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// runStatusJSONWatch streams the session list as JSON lines: one line at start,
// then one whenever the marshaled output changes (checked every second — the
// tmux package's internal caches make each check cheap). Exits when stdout
// closes, i.e. when the consuming widget goes away.
func runStatusJSONWatch() error {
	var last string
	for {
		sessions, err := tmux.ListSessions()
		if err == nil {
			b, mErr := sessionsJSON(sessions, tmux.VSCodeClientDirs())
			if mErr == nil && string(b) != last {
				last = string(b)
				if _, wErr := fmt.Fprintln(os.Stdout, last); wErr != nil {
					return nil // 소비자(위젯)가 사라짐 — 조용히 종료
				}
			}
		}
		time.Sleep(time.Second)
	}
}

func joinWith(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}

// runTUI drives the Bubble Tea program and carries out whatever the model asked
// for on its way out.
//
// The starting model is a parameter so `mux` and `mux new` differ only in where
// they open — the attach and detach handling below is shared, and a second copy
// of it is how one of them would quietly stop attaching.
// repairOwnedConfig heals a tmux config that still names an older copy of mux.
//
// Failures are swallowed on purpose. This is housekeeping the user did not ask
// for on this run, it has no bearing on the TUI about to open, and the only
// place to report it would be over the screen they were reaching for.
func repairOwnedConfig() {
	_, _ = tmux.RepairOwnedConfig()
}

func runTUI(model ui.Model) error {
	repairOwnedConfig()

	// Mouse reporting is on so list rows can be clicked. The cost is that the
	// terminal hands mux every mouse event instead of handling it itself, so
	// wheel-scroll and drag-to-select stop working here; holding Shift still
	// gets the terminal's own selection.
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// 원격 attach 소켓: 위젯 클릭(mux switch)이 WT 클라이언트를 못 찾을 때
	// 이 TUI를 해당 세션으로 attach시킨다. 소켓을 못 열어도 TUI는 정상 동작.
	if closeRemote, err := ui.ServeRemote(p.Send); err == nil {
		defer closeRemote()
	}

	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	m, ok := result.(ui.Model)
	if !ok {
		return nil
	}

	switch {
	case m.DetachRequested():
		if err := ui.DetachClient(); err != nil {
			return fmt.Errorf("failed to detach tmux client: %w", err)
		}
	case m.AttachName() != "":
		if err := ui.AttachToSession(m.AttachName(), m.AttachWindowIndex(), m.AttachPaneIndex()); err != nil {
			return fmt.Errorf("failed to attach: %w", err)
		}
	case bootstrappedPopup():
		// `q` in the popup a bootstrap opened. The terminal was attached only so
		// there would be a client to draw the popup on, so quitting without
		// choosing a session has to undo that much: otherwise closing the popup
		// leaves the user inside whichever session `attach-session` picked,
		// which is not where they typed `mux` and not a session they chose.
		// Before the bootstrap existed, quitting there returned to the shell.
		if err := ui.DetachClient(); err != nil {
			return fmt.Errorf("failed to detach tmux client: %w", err)
		}
	}
	return nil
}

// bootstrappedPopup reports whether this mux is the one AttachAndPopup opened.
func bootstrappedPopup() bool {
	return os.Getenv(tmux.BootstrapPopupEnv) != ""
}
