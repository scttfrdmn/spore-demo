// Package state defines the snapshot the dashboard renders. Sources (live EC2
// tags, or the simulator) produce Snapshots; the UI only consumes them.
package state

import "time"

// EC2 instance states as reported by DescribeInstances.
const (
	EC2Pending      = "pending"
	EC2Running      = "running"
	EC2Stopping     = "stopping"
	EC2Stopped      = "stopped"
	EC2ShuttingDown = "shutting-down"
	EC2Terminated   = "terminated"
)

// Spore is one spawn-managed instance. Everything here comes from EC2 and the
// spawn:* tags that spawn and spored maintain — the dashboard never SSHes to
// an instance, because a session would count as activity and keep it from
// ever going idle.
type Spore struct {
	Name, ID, Type string
	State          string
	Reason         string // EC2 state-transition reason, e.g. "User initiated"
	Spot           bool

	Rate        float64   // $/hr used for cost: the live Spot price for Spot, else spawn:price-per-hour
	EBSRate     float64   // $/hr, spawn:ebs-hourly-cost
	Launch      time.Time // spawn:launch-time (first launch, survives stop/start)
	TTLDeadline time.Time // spawn:ttl-deadline
	IdleTimeout string    // spawn:idle-timeout, e.g. "15m"
	OnComplete  string    // spawn:on-complete

	// Running time so far: spored publishes spawn:compute-seconds with each
	// heartbeat; while running we extrapolate from the last heartbeat.
	ComputeSec float64
	Heartbeat  time.Time

	ArrayName  string // spawn:job-array-name; empty for a single instance
	ArrayIndex int
	ArraySize  int
	MPI        bool
}

// Cost returns the estimated spend so far at time now.
func (s Spore) Cost(now time.Time) float64 {
	sec := s.ComputeSec
	switch {
	case s.State != EC2Running:
	case !s.Heartbeat.IsZero() && now.After(s.Heartbeat):
		sec += now.Sub(s.Heartbeat).Seconds()
	case s.Heartbeat.IsZero() && !s.Launch.IsZero() && now.After(s.Launch):
		sec += now.Sub(s.Launch).Seconds() // before spored's first heartbeat
	}
	cost := sec / 3600 * s.Rate
	if !s.Launch.IsZero() && s.State != EC2Terminated {
		cost += now.Sub(s.Launch).Hours() * s.EBSRate // EBS bills while stopped too
	}
	return cost
}

// EffectiveRate is spend so far divided by wall-clock time since launch: what
// the instance has really cost per hour of having it, idle stops included.
func (s Spore) EffectiveRate(now time.Time) float64 {
	h := now.Sub(s.Launch).Hours()
	if s.Launch.IsZero() || h <= 0 {
		return 0
	}
	return s.Cost(now) / h
}

// Event is one line in the lifecycle feed.
type Event struct {
	At   time.Time
	Name string
	Text string
}

// Snapshot is a complete point-in-time view.
type Snapshot struct {
	Taken  time.Time
	Title  string
	Spores []Spore // all, singles and array members
	Events []Event // newest last
	Err    error
}

// Source produces snapshots.
type Source interface {
	Snapshot() Snapshot
}
