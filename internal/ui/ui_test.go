package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/scttfrdmn/spore-demo/internal/source"
	"github.com/scttfrdmn/spore-demo/internal/state"
)

func TestCompactMergesArrayEvents(t *testing.T) {
	t0 := time.Now()
	ev := []state.Event{
		{At: t0, Name: "demo", Text: "launched"},
		{At: t0, Name: "arr-0", Text: "launched"},
		{At: t0, Name: "arr-1", Text: "launched"},
		{At: t0.Add(time.Second), Name: "arr-2", Text: "launched"},
		{At: t0.Add(50 * time.Second), Name: "arr-0", Text: "shutting-down"},
		{At: t0.Add(53 * time.Second), Name: "arr-1", Text: "shutting-down"},
		{At: t0.Add(58 * time.Second), Name: "arr-0", Text: "terminated"},
		{At: t0.Add(59 * time.Second), Name: "arr-2", Text: "shutting-down"},
		{At: t0.Add(61 * time.Second), Name: "arr-1", Text: "terminated"},
	}
	var got []string
	for _, e := range compact(ev) {
		got = append(got, e.Name+" "+e.Text)
	}
	want := []string{"demo launched", "arr[0-2] launched", "arr[0-2] terminating", "arr[0-1] terminated"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("compact =\n  %q\nwant\n  %q", got, want)
	}
}

// Every line of the dashboard must be exactly 140 wide at every point in the
// simulated demo, matching the presenter's terminal.
func TestViewWidthAcrossTimeline(t *testing.T) {
	sim := source.NewSim(1)
	sim.TogglePause()
	m := New(sim)
	m.width, m.height = 140, 14
	for step := 0; step < 30; step++ {
		m.snap = sim.Snapshot()
		lines := strings.Split(m.View(), "\n")
		if len(lines) != 14 {
			t.Fatalf("step %d: %d lines, want 14", step, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != 140 {
				t.Fatalf("step %d line %d width %d: %q", step, i, w, l)
			}
		}
		sim.Skip(13)
	}
}

func TestCostAndEffectiveRate(t *testing.T) {
	now := time.Now()
	s := state.Spore{State: state.EC2Stopped, Rate: 1.0, Launch: now.Add(-2 * time.Hour), ComputeSec: 3600}
	if c := s.Cost(now); c < 0.999 || c > 1.001 {
		t.Errorf("cost = %v, want 1.00 (1 h compute at $1/hr)", c)
	}
	if r := s.EffectiveRate(now); r < 0.499 || r > 0.501 {
		t.Errorf("effective = %v, want 0.50 (stopped half the time)", r)
	}
	s.State, s.Heartbeat = state.EC2Running, now.Add(-30*time.Minute)
	if c := s.Cost(now); c < 1.499 || c > 1.501 {
		t.Errorf("running cost = %v, want 1.50 (extrapolated from last heartbeat)", c)
	}
}
