package source

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/scttfrdmn/spore-demo/internal/state"
)

// Sim replays the talk's spore.host demo as a deterministic timeline, so the
// dashboard can be built and rehearsed with no AWS account:
//
//	truffle (nothing launched) → demo workstation launches → idles → stops
//	itself → `spawn connect` restarts it → 8-member job array runs and each
//	member terminates itself → 4-node MPI cohort launches all-or-nothing,
//	runs, terminates itself → demo is terminated
//
// Times are sim-seconds at speed 1; the idle wait is shortened from the
// configured 15m to keep the loop short.
type Sim struct {
	mu      sync.Mutex
	start   time.Time
	pausedT float64
	speed   float64
	spores  []simSpore
}

type phase struct {
	at    float64
	state string
}

type simSpore struct {
	base   state.Spore
	phases []phase
}

const simLoop = 380.0

func NewSim(speed float64) *Sim {
	if speed <= 0 {
		speed = 1
	}
	s := &Sim{start: time.Now(), pausedT: -1, speed: speed}

	demo := state.Spore{Name: "demo", Type: "m8a.4xlarge", Rate: 0.9738, EBSRate: 0.0022,
		IdleTimeout: "15m", ID: "i-0demo"}
	s.spores = append(s.spores, simSpore{demo, []phase{
		{15, state.EC2Pending}, {33, state.EC2Running},
		{105, state.EC2Stopping}, {120, state.EC2Stopped}, // idle → stop
		{150, state.EC2Pending}, {165, state.EC2Running}, // spawn connect restarts it
		{330, state.EC2ShuttingDown}, {345, state.EC2Terminated},
	}})
	for i := 0; i < 8; i++ {
		run := 197 + float64(i)*0.4
		done := run + 45 + float64(i)*3
		s.spores = append(s.spores, simSpore{state.Spore{Name: fmt.Sprintf("arr-%d", i),
			ID: fmt.Sprintf("i-0arr%d", i), Type: "c8a.xlarge", Spot: true, Rate: 0.0812, EBSRate: 0.0022,
			OnComplete: "terminate", ArrayName: "arr", ArrayIndex: i, ArraySize: 8}, []phase{
			{190, state.EC2Pending}, {run, state.EC2Running},
			{done, state.EC2ShuttingDown}, {done + 8, state.EC2Terminated},
		}})
	}
	for i := 0; i < 4; i++ {
		done := 300 + float64(i)
		s.spores = append(s.spores, simSpore{state.Spore{Name: fmt.Sprintf("sim-%d", i),
			ID: fmt.Sprintf("i-0sim%d", i), Type: "c8a.4xlarge", Rate: 0.8622, EBSRate: 0.0022,
			OnComplete: "terminate", ArrayName: "sim", ArrayIndex: i, ArraySize: 4, MPI: true}, []phase{
			{260, state.EC2Pending}, {275, state.EC2Running}, // all-or-nothing: together
			{done, state.EC2ShuttingDown}, {done + 10, state.EC2Terminated},
		}})
	}
	return s
}

func (s *Sim) now() float64 {
	if s.pausedT >= 0 {
		return s.pausedT
	}
	return math.Mod(time.Since(s.start).Seconds()*s.speed, simLoop)
}

// TogglePause freezes or resumes the timeline.
func (s *Sim) TogglePause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pausedT >= 0 {
		s.start = time.Now().Add(-time.Duration(s.pausedT / s.speed * float64(time.Second)))
		s.pausedT = -1
	} else {
		s.pausedT = s.now()
	}
}

// Restart jumps back to the beginning.
func (s *Sim) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.start = time.Now()
	if s.pausedT >= 0 {
		s.pausedT = 0
	}
}

// Skip moves the timeline forward by d sim-seconds.
func (s *Sim) Skip(d float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pausedT >= 0 {
		s.pausedT = math.Mod(s.pausedT+d, simLoop)
		return
	}
	s.start = s.start.Add(-time.Duration(d / s.speed * float64(time.Second)))
}

func (s *Sim) Snapshot() state.Snapshot {
	s.mu.Lock()
	t := s.now()
	s.mu.Unlock()
	wall := time.Now()
	at := func(st float64) time.Time {
		return wall.Add(-time.Duration((t - st) / s.speed * float64(time.Second)))
	}
	snap := state.Snapshot{Taken: wall, Title: "spore.host · demo-account · us-west-2"}
	for _, ss := range s.spores {
		if t < ss.phases[0].at {
			continue
		}
		sp := ss.base
		sp.Launch = at(ss.phases[0].at)
		sp.TTLDeadline = sp.Launch.Add(time.Hour)
		sp.Heartbeat = wall
		runningSince := -1.0
		for _, p := range ss.phases {
			if p.at > t {
				break
			}
			if runningSince >= 0 {
				sp.ComputeSec += (p.at - runningSince) / s.speed
				runningSince = -1
			}
			if p.state == state.EC2Running {
				runningSince = p.at
			}
			if sp.State != p.state {
				snap.Events = append(snap.Events, state.Event{At: at(p.at), Name: sp.Name, Text: simDescribe(sp, p.state)})
			}
			sp.State = p.state
		}
		if runningSince >= 0 {
			sp.ComputeSec += (t - runningSince) / s.speed
		}
		if sp.State == state.EC2Terminated && sp.OnComplete == "" {
			sp.Reason = "User initiated"
		}
		snap.Spores = append(snap.Spores, sp)
	}
	SortSpores(snap.Spores)
	sortEvents(snap.Events)
	return snap
}

func simDescribe(sp state.Spore, st string) string {
	switch {
	case st == state.EC2Pending && sp.State == "":
		return "launched"
	case st == state.EC2Stopping && sp.IdleTimeout != "":
		return "idle " + sp.IdleTimeout + " → stopping"
	case st == state.EC2Pending && sp.State == state.EC2Stopped:
		return "starting"
	}
	return st
}

func sortEvents(ev []state.Event) {
	sort.SliceStable(ev, func(a, b int) bool { return ev[a].At.Before(ev[b].At) })
}
