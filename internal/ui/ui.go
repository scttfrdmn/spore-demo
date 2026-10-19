// Package ui renders the two-region dashboard (single spores | arrays & MPI),
// styled to match burstlab-demo. The interactive shell is a tmux pane below.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/scttfrdmn/spore-demo/internal/source"
	"github.com/scttfrdmn/spore-demo/internal/state"
)

// Rows highlight briefly when their state changes, so the audience's eye is
// drawn to what just happened.
const flashFor = 3 * time.Second

const minRows = 10

var (
	cGreen   = lipgloss.Color("10")
	cYellow  = lipgloss.Color("11")
	cMagenta = lipgloss.Color("13")
	cCyan    = lipgloss.Color("14")
	cRed     = lipgloss.Color("9")
	cDim     = lipgloss.Color("8")
	cWhite   = lipgloss.Color("15")

	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sPlain  = lipgloss.NewStyle()
	sBold   = lipgloss.NewStyle().Bold(true).Foreground(cWhite)
	sHeader = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	sBorder = lipgloss.NewStyle().Foreground(cDim)
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(cCyan)
	sBar    = lipgloss.NewStyle().Reverse(true)
	sErr    = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	sFlash  = lipgloss.NewStyle().Reverse(true).Bold(true)
	sMoney  = lipgloss.NewStyle().Foreground(cYellow)
)

type tickMsg time.Time

type Model struct {
	src    state.Source
	sim    *source.Sim
	snap   state.Snapshot
	width  int
	height int
	last   map[string]string
	flash  map[string]time.Time
}

func New(src state.Source) Model {
	m := Model{src: src, last: map[string]string{}, flash: map[string]time.Time{}, width: 140}
	if s, ok := src.(*source.Sim); ok {
		m.sim = s
	}
	return m
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Init() tea.Cmd { return tick() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case " ":
			if m.sim != nil {
				m.sim.TogglePause()
			}
		case "r":
			if m.sim != nil {
				m.sim.Restart()
			}
		case "right", "n":
			if m.sim != nil {
				m.sim.Skip(15)
			}
		}
	case tickMsg:
		m.snap = m.src.Snapshot()
		now := time.Time(msg)
		for _, s := range m.snap.Spores {
			key := s.ArrayName
			if key == "" {
				key = s.Name
			}
			if prev, ok := m.last[s.ID]; ok && prev != s.State {
				m.flash[key] = now
			}
			m.last[s.ID] = s.State
		}
		return m, tick()
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Layout

func (m Model) View() string {
	if m.snap.Taken.IsZero() {
		return "connecting…"
	}
	w := m.width
	lw := w / 2
	rw := w - lw
	singles, arrays := split(m.snap.Spores)
	rows := max(minRows, len(singles)+3, len(arrays)+4)

	left := m.leftPane(lw, rows, singles)
	right := m.rightPane(rw, rows, arrays)
	out := []string{m.titleBar(w)}
	for i := range left {
		out = append(out, left[i]+right[i])
	}
	if m.height > 0 && len(out) > m.height {
		out = out[:m.height]
	}
	return strings.Join(out, "\n")
}

type group struct {
	name    string
	members []state.Spore
}

func split(all []state.Spore) (singles []state.Spore, arrays []group) {
	idx := map[string]int{}
	for _, s := range all {
		if s.ArrayName == "" {
			singles = append(singles, s)
			continue
		}
		i, ok := idx[s.ArrayName]
		if !ok {
			i = len(arrays)
			idx[s.ArrayName] = i
			arrays = append(arrays, group{name: s.ArrayName})
		}
		arrays[i].members = append(arrays[i].members, s)
	}
	for _, g := range arrays {
		sort.Slice(g.members, func(a, b int) bool { return g.members[a].ArrayIndex < g.members[b].ArrayIndex })
	}
	return
}

func (m Model) titleBar(w int) string {
	s := m.snap
	left := " " + s.Title
	right := "LIVE"
	if m.sim != nil {
		right = "SIM  space pause · → skip · r restart"
	}
	if s.Err != nil {
		right = sErr.Render("ec2: " + s.Err.Error())
	}
	right += "   " + s.Taken.Format("15:04:05") + " "
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = truncate(right, max(0, w-lipgloss.Width(left)-1))
		gap = max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return sBar.Render(left + strings.Repeat(" ", gap) + right)
}

// NAME TYPE MKT EC2 TTL IDLE COST EFF/HR — 68 cells, the pane's inner width at 140 columns.
var leftCols = []int{8, 13, 5, 13, 7, 7, 7, 7}

func (m Model) leftPane(w, rows int, singles []state.Spore) []string {
	now := m.snap.Taken
	lines := []string{header(leftCols, "NAME", "TYPE", "MKT", "EC2", "TTL", "IDLE", "COST", "EFF/HR")}
	var spend, burn float64
	for _, s := range singles {
		lines = append(lines, m.singleRow(s, now))
	}
	for _, s := range m.snap.Spores {
		spend += s.Cost(now)
		burn += burnRate(s)
	}
	if len(singles) == 0 {
		lines = append(lines, sDim.Render("  no spores launched yet"))
	}
	lines = append(lines, m.feed(w, rows+1-len(lines), false)...)
	top := fmt.Sprintf("spent %s · burning %s/hr", money(spend), money(burn))
	return box(w, rows+1, "SPORES · single instances", top, lines, "COST = compute + EBS · EFF/HR = cost ÷ hours since launch")
}

func (m Model) singleRow(s state.Spore, now time.Time) string {
	nameStyle := sBold
	gone := s.State == state.EC2Terminated
	if gone {
		nameStyle = sDim
	}
	if t, ok := m.flash[s.Name]; ok && now.Sub(t) < flashFor {
		nameStyle = sFlash
	}
	ec2, es := ec2Cell(s.State)
	mkt := "od"
	if s.Spot {
		mkt = "spot"
	}
	ttl, idle, cost, eff := "—", "—", money(s.Cost(now)), money(s.EffectiveRate(now))
	if !gone && !s.TTLDeadline.IsZero() {
		ttl = clock(s.TTLDeadline.Sub(now))
	}
	if s.IdleTimeout != "" {
		idle = s.IdleTimeout
	}
	rowStyle := sPlain
	if gone {
		rowStyle = sDim
	}
	c := leftCols
	return " " + nameStyle.Render(pad(s.Name, c[0]-1)) + " " +
		rowStyle.Render(pad(s.Type, c[1])) + rowStyle.Render(pad(mkt, c[2])) +
		es.Render(pad(ec2, c[3])) + rowStyle.Render(pad(ttl, c[4])) + sDim.Render(pad(idle, c[5])) +
		moneyStyle(gone).Render(padLeft(cost, c[6])) + moneyStyle(gone).Render(padLeft(eff, c[7]))
}

// NAME SHAPE MKT MEMBERS KIND ON-DONE TTL COST
var rightCols = []int{6, 16, 5, 10, 6, 10, 6, 7}

func (m Model) rightPane(w, rows int, arrays []group) []string {
	now := m.snap.Taken
	lines := []string{header(rightCols, "NAME", "SHAPE", "MKT", "MEMBERS", "KIND", "ON DONE", "TTL", "COST")}
	alive := 0
	for _, g := range arrays {
		lines = append(lines, m.arrayRow(g, now))
		for _, s := range g.members {
			if s.State != state.EC2Terminated {
				alive++
			}
		}
	}
	if len(arrays) == 0 {
		lines = append(lines, sDim.Render("  no job arrays yet"))
	}
	lines = append(lines, m.feed(w, rows+1-len(lines), true)...)
	top := fmt.Sprintf("%d arrays · %d members alive", len(arrays), alive)
	return box(w, rows+1, "ARRAYS & MPI", top, lines, "● running  ◐ starting  ◑ terminating  ○ gone")
}

func (m Model) arrayRow(g group, now time.Time) string {
	first := g.members[0]
	nameStyle := sBold
	allGone := true
	var cost float64
	var deadline time.Time
	for _, s := range g.members {
		cost += s.Cost(now)
		if s.State != state.EC2Terminated {
			allGone = false
		}
		if deadline.IsZero() || (!s.TTLDeadline.IsZero() && s.TTLDeadline.Before(deadline)) {
			deadline = s.TTLDeadline
		}
	}
	if allGone {
		nameStyle = sDim
	}
	if t, ok := m.flash[g.name]; ok && now.Sub(t) < flashFor {
		nameStyle = sFlash
	}
	size := max(first.ArraySize, len(g.members))
	shape := fmt.Sprintf("%d× %s", size, first.Type)
	mkt := "od"
	if first.Spot {
		mkt = "spot"
	}
	kind := "array"
	if first.MPI {
		kind = "MPI"
	}
	done := first.OnComplete
	if done == "" {
		done = "—"
	}
	ttl := "—"
	if !allGone && !deadline.IsZero() {
		ttl = clock(deadline.Sub(now))
	}
	rowStyle := sPlain
	if allGone {
		rowStyle = sDim
	}
	c := rightCols
	return " " + nameStyle.Render(pad(g.name, c[0]-1)) + " " + rowStyle.Render(pad(shape, c[1])) +
		rowStyle.Render(pad(mkt, c[2])) + memberDots(g.members, c[3]) + rowStyle.Render(pad(kind, c[4])) +
		sDim.Render(pad(done, c[5])) + rowStyle.Render(pad(ttl, c[6])) + moneyStyle(allGone).Render(padLeft(money(cost), c[7]))
}

// memberDots draws one glyph per member, coloured by state.
func memberDots(ms []state.Spore, width int) string {
	var b strings.Builder
	n := 0
	for _, s := range ms {
		if n >= width-1 {
			break
		}
		g, st := dot(s.State)
		b.WriteString(st.Render(g))
		n++
	}
	return b.String() + strings.Repeat(" ", max(0, width-n))
}

func dot(st string) (string, lipgloss.Style) {
	switch st {
	case state.EC2Running:
		return "●", lipgloss.NewStyle().Foreground(cGreen)
	case state.EC2Pending:
		return "◐", lipgloss.NewStyle().Foreground(cYellow).Bold(true)
	case state.EC2Stopping, state.EC2ShuttingDown:
		return "◑", lipgloss.NewStyle().Foreground(cMagenta).Bold(true)
	case state.EC2Stopped:
		return "◻", lipgloss.NewStyle().Foreground(cCyan)
	}
	return "○", sDim
}

func ec2Cell(s string) (string, lipgloss.Style) {
	st := lipgloss.NewStyle()
	switch s {
	case state.EC2Running:
		return "● running", st.Foreground(cGreen)
	case state.EC2Pending:
		return "◐ pending", st.Foreground(cYellow).Bold(true)
	case state.EC2Stopping:
		return "◑ stopping", st.Foreground(cMagenta).Bold(true)
	case state.EC2ShuttingDown:
		return "◑ terminating", st.Foreground(cMagenta).Bold(true)
	case state.EC2Stopped:
		return "◻ stopped", st.Foreground(cCyan).Bold(true)
	case state.EC2Terminated:
		return "○ terminated", sDim
	}
	return "? " + s, st
}

// feed renders one pane's lifecycle events (single instances on the left,
// array/MPI members on the right) in room lines, newest last.
func (m Model) feed(w, room int, arrays bool) []string {
	if room < 3 {
		return nil
	}
	lines := []string{sDim.Render(" " + strings.Repeat("·", w-4)), " " + sHeader.Render("LIFECYCLE")}
	var ev []state.Event
	for _, e := range m.snap.Events {
		if e.Array == arrays {
			ev = append(ev, e)
		}
	}
	ev = compact(ev)
	if n := room - len(lines); len(ev) > n {
		ev = ev[len(ev)-n:]
	}
	for _, e := range ev {
		lines = append(lines, "  "+sDim.Render(e.At.Format("15:04:05"))+"  "+pad(e.Name, 12)+" "+eventStyle(e.Text).Render(e.Text))
	}
	if len(ev) == 0 {
		lines = append(lines, sDim.Render("  (nothing yet)"))
	}
	return lines
}

// compact merges consecutive events that are the same change across members
// of one array ("arr-0 launched", "arr-1 launched", …) into "arr[0-7] launched",
// so an 8-member launch is one line, not a screenful.
func compact(ev []state.Event) []state.Event {
	var out []state.Event
	var idx [][]int
	for _, e := range ev {
		e.Text = eventText(e.Text)
		base, n, ok := arrayMember(e.Name)
		merged := false
		// look back a few lines: members finish seconds apart, so their
		// "terminating" and "terminated" events interleave
		for k := len(out) - 1; ok && k >= 0 && k >= len(out)-4; k-- {
			if out[k].Text != e.Text || len(idx[k]) == 0 || e.At.Sub(out[k].At) > 30*time.Second {
				continue
			}
			if pb, _, _ := arrayMember(out[k].Name); pb == base || strings.HasPrefix(out[k].Name, base+"[") {
				idx[k] = append(idx[k], n) // keep the first member's time so the feed stays in order
				out[k].Name = base + "[" + ranges(idx[k]) + "]"
				merged = true
				break
			}
		}
		if merged {
			continue
		}
		out = append(out, e)
		if ok {
			idx = append(idx, []int{n})
		} else {
			idx = append(idx, nil)
		}
	}
	return out
}

// eventText maps raw EC2 states to the words used in the EC2 column.
func eventText(t string) string {
	if t == state.EC2ShuttingDown {
		return "terminating"
	}
	return t
}

func arrayMember(name string) (string, int, bool) {
	i := strings.LastIndexByte(name, '-')
	if i <= 0 {
		return "", 0, false
	}
	var n int
	if _, err := fmt.Sscanf(name[i+1:], "%d", &n); err != nil || fmt.Sprint(n) != name[i+1:] {
		return "", 0, false
	}
	return name[:i], n, true
}

func ranges(ns []int) string {
	sort.Ints(ns)
	var parts []string
	for i := 0; i < len(ns); {
		j := i
		for j+1 < len(ns) && ns[j+1] == ns[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, fmt.Sprint(ns[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", ns[i], ns[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

func eventStyle(text string) lipgloss.Style {
	switch {
	case strings.Contains(text, "terminat"):
		return lipgloss.NewStyle().Foreground(cMagenta)
	case strings.Contains(text, "stop") || strings.Contains(text, "idle"):
		return lipgloss.NewStyle().Foreground(cCyan)
	case strings.Contains(text, "launch") || strings.Contains(text, "start") || text == state.EC2Running:
		return lipgloss.NewStyle().Foreground(cGreen)
	}
	return sPlain
}

// burnRate is what an instance is costing right now, per hour.
func burnRate(s state.Spore) float64 {
	switch s.State {
	case state.EC2Running, state.EC2Pending, state.EC2Stopping, state.EC2ShuttingDown:
		return s.Rate + s.EBSRate
	case state.EC2Stopped:
		return s.EBSRate // compute stops; the volume keeps billing
	}
	return 0
}

func moneyStyle(gone bool) lipgloss.Style {
	if gone {
		return sDim
	}
	return sMoney
}

func money(v float64) string {
	if v < 10 {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%.0f", v)
}

func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%dh%02d", s/3600, (s%3600)/60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func header(cols []int, names ...string) string {
	var b strings.Builder
	b.WriteString(" ")
	for i, n := range names {
		if i >= len(cols)-2 && (n == "COST" || n == "EFF/HR") {
			b.WriteString(sHeader.Render(padLeft(n, cols[i])))
			continue
		}
		b.WriteString(sHeader.Render(pad(n, cols[i])))
	}
	return b.String()
}

// box draws a rounded frame with a title and a right-aligned note in the top
// border and a note in the bottom border. Every returned line is exactly w wide.
func box(w, h int, title, note string, lines []string, foot string) []string {
	inner := w - 2
	t := "─ " + sTitle.Render(title) + " "
	n := " " + note + " ─"
	fill := inner - lipgloss.Width(t) - lipgloss.Width(n)
	if fill < 1 {
		n, fill = "─", max(1, inner-lipgloss.Width(t)-1)
	}
	out := []string{sBorder.Render("╭") + sBorder.Render("─ ") + sTitle.Render(title) + " " +
		sBorder.Render(strings.Repeat("─", fill)) + sDim.Render(n[:len(n)-len("─")]) + sBorder.Render("─╮")}
	for i := 0; i < h; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		lw := lipgloss.Width(l)
		if lw > inner {
			l = truncate(l, inner)
			lw = lipgloss.Width(l)
		}
		out = append(out, sBorder.Render("│")+l+strings.Repeat(" ", inner-lw)+sBorder.Render("│"))
	}
	f := " " + foot + " "
	out = append(out, sBorder.Render("╰"+strings.Repeat("─", max(0, inner-lipgloss.Width(f)-1)))+
		sDim.Render(f)+sBorder.Render("─╯"))
	return out
}

func pad(s string, w int) string {
	s = runewidth.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-runewidth.StringWidth(s)))
}

func padLeft(s string, w int) string {
	s = runewidth.Truncate(s, w, "…")
	return strings.Repeat(" ", max(0, w-runewidth.StringWidth(s))) + s
}

// truncate shortens a possibly-styled string to w cells.
func truncate(s string, w int) string {
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
