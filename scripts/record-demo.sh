#!/bin/bash
# record-demo.sh — record the spore.host demo with asciinema.
#
# Opens the spore-demo layout in a fresh tmux session at 140×40 under
# `asciinema rec --headless`, then plays presenter: types each command into
# the shell pane (at typing speed) and waits for the dashboard to show its
# outcome before moving on. Ends once everything it launched is gone.
#
# Story: truffle → launch a workstation → status → it idles and stops itself
# → status (effective rate falls) → spawn connect restarts it → a job array
# whose members terminate themselves → an MPI cohort that does the same →
# terminate the workstation.
#
# Output (in recordings/):
#   spore-demo.cast        real time
#   spore-demo.markers     seconds-into-recording of each milestone
#
# Usage: AWS_PROFILE=aws scripts/record-demo.sh [--sim] [extra spore-demo flags]
#   IDLE=5m (default)  idle timeout for the workstation; the slide says 15m,
#                      a shorter one keeps the real-time recording shorter
#   --sim              rehearse the pipeline: commands are typed but not run
set -euo pipefail
cd "$(dirname "$0")/.."
: "${AWS_PROFILE:?set AWS_PROFILE}"
IDLE=${IDLE:-5m}
SESSION=spore-rec
OUT=recordings/spore-demo
mkdir -p recordings
go build -o bin/spore-demo .

SIM=false
case " $* " in *" --sim "*) SIM=true ;; esac

markers=$OUT.markers; : > "$markers"
t0=0
mark() { echo "$(( $(date +%s) - t0 )) $*" | tee -a "$markers"; }

D= S=
screen() { tmux capture-pane -p -t "$D"; }
shellscreen() { tmux capture-pane -p -t "$S"; }
# Wait (up to $2 s, default 30 min) until the dashboard matches regex $1.
wait_for() {
  local deadline=$(( $(date +%s) + ${2:-1800} ))
  until screen | grep -Eq "$1"; do
    [ "$(date +%s)" -lt "$deadline" ] || { echo "timed out waiting for: $1" >&2; exit 1; }
    sleep 1
  done
}
# Wait until the shell pane shows regex $1 (a prompt, a confirmation, output).
wait_shell() {
  local deadline=$(( $(date +%s) + ${2:-300} ))
  until shellscreen | grep -Eq "$1"; do
    [ "$(date +%s)" -lt "$deadline" ] || { echo "timed out waiting in shell for: $1" >&2; exit 1; }
    sleep 0.5
  done
}

# Type a command like a person would. In --sim the line is prefixed with ': '
# so it is shown but not executed.
type_cmd() {
  local s=$1 i
  $SIM && s=": $s"
  for (( i=0; i<${#s}; i++ )); do
    tmux send-keys -t "$S" -l "${s:i:1}"; sleep 0.04
  done
  sleep 0.6; tmux send-keys -t "$S" Enter
}
# Wait for the local shell to be back at a prompt (the line ends in "$ " or "% ").
idle_prompt() { sleep 1; $SIM && { sleep 1; return; }; wait_shell '[$%#] *$' "${1:-600}"; }

driver() {
  until tmux has-session -t "=$SESSION" 2>/dev/null; do sleep 0.2; done
  D=$(tmux list-panes -t "$SESSION" -F '#{pane_id}' | head -1)
  S=$(tmux list-panes -t "$SESSION" -F '#{pane_id}' | tail -1)
  t0=$(date +%s)
  wait_for ' (LIVE|SIM) ' 60; sleep 3
  type_cmd 'clear'; sleep 1

  mark "1: truffle"
  type_cmd 'truffle find "amd turin 16 cores 64gb" | head -12'; idle_prompt; sleep 5
  type_cmd 'truffle spot m8a.4xlarge --sort-by-price | head -14';                 idle_prompt; sleep 5
  type_cmd 'clear'

  mark "2: launch workstation"
  type_cmd "spawn launch demo --instance-type m8a.4xlarge --ttl 1h --idle-timeout $IDLE"
  wait_for 'demo +m8a\.4xlarge .*● running' 300; mark "demo running"; idle_prompt
  sleep 75   # let spored take its first reading, so status shows idle progress, not "unknown"
  type_cmd 'clear'; type_cmd 'spawn status demo | head -16'; idle_prompt; mark "status (running)"; sleep 8

  # Hands off: any SSH session or traffic would count as activity.
  wait_for 'demo +m8a\.4xlarge .*◻ stopped' 3600; mark "demo stopped (idle)"; sleep 5
  type_cmd 'clear'; type_cmd 'spawn status demo | head -20'; idle_prompt; mark "status (stopped)"; sleep 10

  mark "3: connect restarts it"
  type_cmd 'clear'; type_cmd 'spawn connect demo'
  if ! $SIM; then
    wait_shell '@ip-|\]\$ *$' 300; sleep 2
    type_cmd 'sudo spored status | sed -n 1,14p'; sleep 4
    type_cmd 'exit'
  fi
  wait_for 'demo +m8a\.4xlarge .*● running' 300; mark "demo running again"; idle_prompt; sleep 3

  mark "4: job array"
  type_cmd 'clear'
  type_cmd "spawn launch arr --job-array-name arr --count 8 --instance-type c8a.xlarge --spot --ttl 1h \\"
  type_cmd "  --on-complete terminate -y --command 'echo \"member \$JOB_ARRAY_INDEX of \$JOB_ARRAY_SIZE on \$(hostname)\"; sleep 45'"
  wait_for 'arr .*●●●●●●●●' 300; mark "array running"; idle_prompt
  type_cmd 'spawn array status arr'; idle_prompt; sleep 3
  type_cmd 'spawn array logs arr --index 3'; idle_prompt
  wait_for 'arr .*○○○○○○○○' 600; mark "array gone"; sleep 4

  mark "5: MPI"
  type_cmd 'clear'
  type_cmd "spawn launch sim --job-array-name sim --count 4 --mpi --instance-type c8a.4xlarge --ttl 1h \\"
  type_cmd "  --mpi-command hostname --mpi-processes-per-node 1 --on-complete terminate -y"
  wait_for 'sim .*●●●●' 600; mark "mpi running"; idle_prompt
  type_cmd 'spawn array logs sim --index 0'; idle_prompt
  wait_for 'sim .*○○○○' 900; mark "mpi gone"; sleep 4

  mark "6: clean up"
  # (no `spawn list` here: other spawn work in the account would appear on camera;
  #  the dashboard shows that only the workstation is left)
  type_cmd 'clear'; type_cmd 'spawn terminate demo --yes'
  wait_for 'demo +m8a\.4xlarge .*○ terminated' 600; mark "all gone"; sleep 10
  tmux kill-session -t "=$SESSION"     # ends the recording
}

# a failed wait must end the recording too, not leave it running
( trap 'tmux kill-session -t "=$SESSION" 2>/dev/null || true' EXIT; driver ) & drv=$!
asciinema rec --headless --window-size 140x40 --overwrite --title "spore.host demo" \
  -c "env AWS_PROFILE=$AWS_PROFILE $PWD/bin/spore-demo up --session $SESSION --fresh --names demo,arr,sim $*" "$OUT.cast"
wait "$drv" || { echo "driver failed — recording is partial" >&2; exit 1; }
echo "recorded $OUT.cast"; cat "$markers"
