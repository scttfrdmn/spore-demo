# spore-demo

A live terminal dashboard for presenting [spore.host](https://spore.host) — the companion to
[burstlab-demo](https://github.com/scttfrdmn/burstlab-demo), with the same look, for the same talk.
Built for a 140×40 terminal:

```
 spore.host · demo-account · us-west-2                                                                                  LIVE   11:01:07
╭─ SPORES · single instances ─────── spent $0.10 · burning $0.98/hr ─╮╭─ ARRAYS & MPI ──────────────────────── 2 arrays · 0 members alive ─╮
│ NAME     TYPE        MKT  EC2          TTL    IDLE      COST EFF/HR││ NAME  SHAPE           MKT  MEMBERS   KIND  ON DONE   TTL      COST │
│ demo     m8a.4xlarge od   ● running    54:59  15m      $0.06  $0.72││ arr   8× c8a.xlarge   spot ○○○○○○○○  array terminate —       $0.01 │
│                                                                    ││ sim   4× c8a.4xlarge  od   ○○○○      MPI   terminate —       $0.03 │
│  COST = compute so far + EBS · EFF/HR = cost ÷ hours since launch  ││ ·································································· │
│                                                                    ││ LIFECYCLE                                                          │
│                                                                    ││  11:00:10  arr[0-7]  job complete → terminating                    │
│                                                                    ││  11:00:11  sim[0-3]  launched                                      │
│                                                                    ││  11:00:18  arr[0-7]  terminated                                    │
│                                                                    ││  11:00:26  sim[0-3]  running                                       │
│                                                                    ││  11:00:51  sim[0-3]  job complete → terminating                    │
│                                                                    ││  11:01:01  sim[0-3]  terminated                                    │
╰────────────────────────────────── spawn launch · status · connect ─╯╰───────────────────── ● running  ◐ starting  ◑ terminating  ○ gone ─╯
laptop $ spawn status demo
```

The top 14 rows are the dashboard; below is a local shell where `spawn` and `truffle` run.

- **SPORES** — single instances: EC2 state, market, a live TTL countdown, the idle rule, cost so
  far and **effective rate** (cost ÷ hours since launch), so the audience sees the rate fall while
  an instance sits stopped. The border shows total spend and what's burning right now.
- **ARRAYS & MPI** — one row per job array or MPI cohort, one dot per member, so members
  terminating themselves are visible at a glance; below it, a lifecycle feed.
- Rows flash for 3 s when their state changes; terminated instances stay visible in gray.

All data comes from EC2 and the `spawn:*` tags spawn and spored maintain (`ttl-deadline`,
`idle-timeout`, `compute-seconds`, `last-heartbeat`, `price-per-hour`, `job-array-*`, …); Spot
instances are costed at the live Spot price. The dashboard never connects to an instance — an
SSH session would count as activity and keep it from going idle. Only instances launched after the
dashboard starts are shown (`--all` shows every spawn-managed instance in the region).

## Install and run

```bash
brew install tmux go spore-host/tap/spawn spore-host/tap/truffle
go build -o bin/spore-demo .

bin/spore-demo --sim            # rehearse with no AWS (scripted, loops); click the top pane:
                                #   space pause · → skip · r restart · q quit
AWS_PROFILE=aws bin/spore-demo  # live, in $AWS_REGION (default us-west-2)
```

| Flag | Default | |
|---|---|---|
| `--region` | `$AWS_REGION` or `us-west-2` | |
| `--profile` | `$AWS_PROFILE` | |
| `--all` | | show every spawn-managed instance, not just new ones |
| `--names` | | only these name prefixes, e.g. `demo,arr,sim` (keeps other work in the account off screen) |
| `--login-shell` | | your own shell in the bottom pane instead of the clean `laptop $` prompt |
| `--fresh` | | replace an existing `spore` tmux session |
| `--sim`, `--speed` | | simulator |

## The demo

From the shell pane (the talk's "spore.host: from laptop to MPI" slide):

| Step | Command | What the audience sees |
|---|---|---|
| 1. Find | `truffle find "amd turin 16 cores 64gb"` | ~3 s in your configured region; the exact fit, `m8a.4xlarge`, first, then the next-closest. |
| | `truffle spot m8a.4xlarge --sort-by-price` | Cheapest Spot first across regions; 60–75% savings. |
| 2. Launch | `spawn launch demo --instance-type m8a.4xlarge --ttl 1h --idle-timeout 15m` | Ready in ~25 s: a row goes ◐ pending → ● running, TTL counting down. (`--dry-run` shows the max cost first.) |
| | `spawn status demo` | Cost, effective rate, TTL, and — after spored's first reading (~1 min) — idle progress ("stops in 3m 8s"). |
| 3. Idle | *(hands off — even `spawn connect` counts as activity)* | spored stops it: ◻ stopped. Burn rate drops to EBS only; EFF/HR keeps falling. |
| | `spawn status demo` | Stopped since, why, TTL still counting, cost across all boots, effective rate, what still bills. |
| 4. Connect | `spawn connect demo` | "Instance is stopped — starting it…", shell in ~35 s; compute carries over across boots. |
| 5. Array | `spawn launch arr --job-array-name arr --count 8 --instance-type c8a.xlarge --spot --ttl 1h --on-complete terminate -y --command 'echo "member $JOB_ARRAY_INDEX of $JOB_ARRAY_SIZE on $(hostname)"; sleep 45'` | Eight dots go green, then each member terminates itself. |
| | `spawn array status arr` · `spawn array logs arr --index 3` | Per-member state; one member's output. |
| 6. MPI | `spawn launch sim --job-array-name sim --count 4 --mpi --instance-type c8a.4xlarge --ttl 1h --mpi-command hostname --mpi-processes-per-node 1 --on-complete terminate -y` | All-or-nothing cohort; runs; cleans itself up. |
| 7. Clean up | `spawn list` · `spawn terminate demo --yes` | Only the workstation was left to remove. (`spawn list` shows every spawn instance in the account — skip it if other work is running.) |

The workstation is On-Demand on purpose: a one-time Spot instance can't be stopped, and Spot can be
reclaimed mid-demo.

Tested live with spawn 0.134.0 and truffle 0.60.0.

## Recording the demo

```bash
brew install asciinema agg ffmpeg
AWS_PROFILE=aws scripts/record-demo.sh      # drives the whole demo unattended (~18 min); IDLE=5m by default
scripts/render-mp4.sh                       # sped-up .cast + MP4 (waits compressed to seconds)
asciinema play -m recordings/spore-demo-fast.cast   # -m holds the final frame
```

Add `--sim` to `record-demo.sh` to try the pipeline without AWS (commands are typed, not run).

## Layout

```
main.go                 CLI: up | dash
internal/source/        live (EC2 + spawn tags) and simulated snapshot sources
internal/ui/            Bubble Tea dashboard (burstlab-demo styling)
internal/launch/        tmux layout
scripts/                record-demo.sh, speedup-cast.py, render-mp4.sh
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
