// Package source produces dashboard snapshots, from EC2 or from a simulator.
package source

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/scttfrdmn/spore-demo/internal/state"
)

// Instances launched this long before the dashboard started are still shown,
// so a launch typed just before it came up isn't missed. Older spawn-managed
// instances (someone else's work in the same account) stay off screen.
const startGrace = 2 * time.Minute

// Live polls DescribeInstances for spawn-managed instances in one region.
type Live struct {
	client  *ec2.Client
	region  string
	started time.Time
	all     bool
	names   []string // if set, only instances whose Name or array name has one of these prefixes

	mu     sync.Mutex
	spores map[string]state.Spore // by instance ID
	seen   map[string]bool        // IDs seen not-terminated this session
	events []state.Event
	title  string
	err    error

	spotMu    sync.Mutex
	spotCache map[string]spotPrice // "type|az"
}

type spotPrice struct {
	price float64
	at    time.Time
}

// NewLive starts polling. With all=false only instances launched since the
// dashboard started (minus startGrace) are shown; names, if non-empty, further
// limits it to instances whose Name or job-array name starts with one of them.
func NewLive(ctx context.Context, region, profile string, all bool, names []string) (*Live, error) {
	opts := []func(*config.LoadOptions) error{}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	l := &Live{client: ec2.NewFromConfig(cfg), region: cfg.Region, started: time.Now(), all: all, names: names,
		spores: map[string]state.Spore{}, seen: map[string]bool{}, spotCache: map[string]spotPrice{},
		title: "spore.host · " + cfg.Region}
	go func() {
		for {
			l.poll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}()
	return l, nil
}

func (l *Live) poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	in := &ec2.DescribeInstancesInput{Filters: []types.Filter{
		{Name: aws.String("tag:spawn:managed"), Values: []string{"true"}},
	}}
	found := map[string]state.Spore{}
	account := ""
	p := ec2.NewDescribeInstancesPaginator(l.client, in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			l.mu.Lock()
			l.err = err
			l.mu.Unlock()
			return
		}
		for _, r := range page.Reservations {
			for _, i := range r.Instances {
				if a := tag(i, "spawn:account-name"); a != "" {
					account = a // from any instance, so the title is right before our first launch
				}
				s := l.toSpore(ctx, i)
				if !l.all && s.Launch.Before(l.started.Add(-startGrace)) || !l.wanted(s) {
					continue
				}
				found[s.ID] = s
			}
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = nil
	now := time.Now()
	for id, s := range found {
		prev, known := l.spores[id]
		if s.State == state.EC2Terminated && !l.seen[id] {
			continue // terminated before we ever saw it alive: history, not news
		}
		if s.State != state.EC2Terminated {
			l.seen[id] = true
		}
		if !known && s.State != state.EC2Pending && s.State != state.EC2Terminated {
			// first seen already past pending (it launched between polls):
			// still report the launch, so array members' events line up
			l.events = append(l.events, state.Event{At: now, Name: s.Name, Text: "launched", Array: s.ArrayName != ""})
		}
		if !known || prev.State != s.State {
			l.events = append(l.events, state.Event{At: now, Name: s.Name, Text: describe(s, prev, known),
				Array: s.ArrayName != ""})
		}
		l.spores[id] = s
	}
	if len(l.events) > 50 {
		l.events = l.events[len(l.events)-50:]
	}
	if account != "" {
		l.title = "spore.host · " + account + " · " + l.region
	}
}

func (l *Live) wanted(s state.Spore) bool {
	if len(l.names) == 0 {
		return true
	}
	for _, n := range l.names {
		if strings.HasPrefix(s.Name, n) || (s.ArrayName != "" && strings.HasPrefix(s.ArrayName, n)) {
			return true
		}
	}
	return false
}

// describe words a state change for the lifecycle feed. EC2 can't tell a job
// finishing (spored terminating itself) from a user's terminate — both are
// "User initiated" — so only causes EC2 does reveal are named.
func describe(s, prev state.Spore, known bool) string {
	switch {
	case !known && s.State == state.EC2Pending:
		return "launched"
	case known && prev.State == state.EC2Stopped && s.State == state.EC2Pending:
		return "starting"
	case s.State == state.EC2Terminated && strings.Contains(s.Reason, "Service initiated"):
		return "terminated by AWS (Spot reclaim)"
	case s.State == state.EC2Terminated && !s.TTLDeadline.IsZero() && !time.Now().Before(s.TTLDeadline):
		return "terminated (TTL reached)"
	}
	return s.State
}

func (l *Live) toSpore(ctx context.Context, i types.Instance) state.Spore {
	s := state.Spore{
		ID:    aws.ToString(i.InstanceId),
		Name:  tag(i, "Name"),
		Type:  string(i.InstanceType),
		State: string(i.State.Name),
		Spot:  i.InstanceLifecycle == types.InstanceLifecycleTypeSpot,

		Reason:      aws.ToString(i.StateTransitionReason),
		IdleTimeout: tag(i, "spawn:idle-timeout"),
		OnComplete:  tag(i, "spawn:on-complete"),
		ArrayName:   tag(i, "spawn:job-array-name"),
		MPI:         tag(i, "spawn:mpi-enabled") == "true",
	}
	s.Rate, _ = strconv.ParseFloat(tag(i, "spawn:price-per-hour"), 64)
	s.EBSRate, _ = strconv.ParseFloat(tag(i, "spawn:ebs-hourly-cost"), 64)
	s.ComputeSec, _ = strconv.ParseFloat(tag(i, "spawn:compute-seconds"), 64)
	s.ArrayIndex, _ = strconv.Atoi(tag(i, "spawn:job-array-index"))
	s.ArraySize, _ = strconv.Atoi(tag(i, "spawn:job-array-size"))
	s.Launch = parseTime(tag(i, "spawn:launch-time"))
	if s.Launch.IsZero() {
		s.Launch = aws.ToTime(i.LaunchTime)
	}
	s.TTLDeadline = parseTime(tag(i, "spawn:ttl-deadline"))
	s.Heartbeat = parseTime(tag(i, "spawn:last-heartbeat"))
	if s.Spot && i.Placement != nil {
		if p, ok := l.spotPrice(ctx, s.Type, aws.ToString(i.Placement.AvailabilityZone)); ok {
			s.Rate = p // spawn:price-per-hour is the on-demand rate even for Spot
		}
	}
	return s
}

// spotPrice returns the current Spot price for a type in an AZ, cached 5 min.
func (l *Live) spotPrice(ctx context.Context, typ, az string) (float64, bool) {
	key := typ + "|" + az
	l.spotMu.Lock()
	c, ok := l.spotCache[key]
	l.spotMu.Unlock()
	if ok && time.Since(c.at) < 5*time.Minute {
		return c.price, true
	}
	out, err := l.client.DescribeSpotPriceHistory(ctx, &ec2.DescribeSpotPriceHistoryInput{
		InstanceTypes:       []types.InstanceType{types.InstanceType(typ)},
		AvailabilityZone:    aws.String(az),
		ProductDescriptions: []string{"Linux/UNIX"},
		StartTime:           aws.Time(time.Now()),
		MaxResults:          aws.Int32(1),
	})
	if err != nil || len(out.SpotPriceHistory) == 0 {
		return 0, false
	}
	price, err := strconv.ParseFloat(aws.ToString(out.SpotPriceHistory[0].SpotPrice), 64)
	if err != nil {
		return 0, false
	}
	l.spotMu.Lock()
	l.spotCache[key] = spotPrice{price, time.Now()}
	l.spotMu.Unlock()
	return price, true
}

func (l *Live) Snapshot() state.Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap := state.Snapshot{Taken: time.Now(), Title: l.title, Err: l.err,
		Events: append([]state.Event(nil), l.events...)}
	for _, s := range l.spores {
		snap.Spores = append(snap.Spores, s)
	}
	SortSpores(snap.Spores)
	return snap
}

// SortSpores orders by launch time, then array index.
func SortSpores(ss []state.Spore) {
	sort.SliceStable(ss, func(a, b int) bool {
		if !ss[a].Launch.Equal(ss[b].Launch) {
			return ss[a].Launch.Before(ss[b].Launch)
		}
		if ss[a].ArrayName != ss[b].ArrayName {
			return ss[a].ArrayName < ss[b].ArrayName
		}
		return ss[a].ArrayIndex < ss[b].ArrayIndex
	})
}

func tag(i types.Instance, key string) string {
	for _, t := range i.Tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}

func parseTime(s string) time.Time {
	for _, f := range []string{time.RFC3339, "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
