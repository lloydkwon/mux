package tmux

import "testing"

// A width above the ceiling must never reach split-window. The panel would shrink
// itself on its first frame, which is a flash of the wrong size on every window
// the panel opens in — and the whole point of the ceiling living in this package
// is that both ends apply the same number.
func TestMaxPanelWidthIsHalfTheWindow(t *testing.T) {
	for _, tt := range []struct{ win, want int }{
		{269, 134},
		{172, 86},
		{149, 74},
		{0, 0},
	} {
		if got := MaxPanelWidth(tt.win); got != tt.want {
			t.Errorf("MaxPanelWidth(%d) = %d, want %d", tt.win, got, tt.want)
		}
	}
}
