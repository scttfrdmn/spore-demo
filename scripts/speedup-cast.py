#!/usr/bin/env python3
"""speedup-cast.py — compress the waiting in a recorded demo.

Reads recordings/spore-demo.cast (asciinema v3) and the .markers file that
record-demo.sh writes, and squeezes each boring stretch — the idle wait, job
runtimes, MPI cohort formation — into a few seconds, leaving every state
change at real speed. The dashboard's clock shows the real time, so the
jumps stay honest.

It also ends the cast on the final dashboard frame: the trailing tmux exit
(clear screen, "[exited]") is dropped, the last frame is held for a few
seconds, and an end marker is added so `asciinema play -m` pauses there
instead of quitting.

Usage: scripts/speedup-cast.py recordings/spore-demo.cast [-o out.cast]
       scripts/speedup-cast.py --finish-only recordings/spore-demo.cast      # trim in place, no speed-up
"""
import argparse
import json
import pathlib

# (from marker, offset s, to marker, offset s, play this stretch in N seconds)
PLAN = [
    ("status (running)", 10, "demo stopped (idle)", -5, 12),   # idle wait (IDLE + up to a minute)
    ("array running", 14, "array gone", -4, 8),                # members run, then terminate themselves
    ("5: MPI", 14, "mpi running", -3, 8),                      # all-or-nothing cohort forming
    ("mpi running", 12, "mpi gone", -4, 8),                    # mpirun, then self-terminate
    ("6: clean up", 14, "all gone", -4, 6),                    # terminate the workstation
]


HOLD_SECONDS = 4
ALT_SCREEN_EXIT = "\x1b[?1049l"


def finish(events):
    """Drop the trailing tmux exit and hold the last frame behind an end marker."""
    for i in range(len(events) - 1, -1, -1):
        if events[i][1] == "o" and ALT_SCREEN_EXIT in events[i][2]:
            events = events[:i]
            break
    events = [e for e in events if e[1] != "x"]  # exit status
    if events and events[-1][1] == "m":          # already finished
        return events
    return events + [[HOLD_SECONDS, "o", ""], [0.0, "m", "end"]]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("cast")
    ap.add_argument("-o", "--out")
    ap.add_argument("--finish-only", action="store_true", help="only trim the ending, in place")
    a = ap.parse_args()
    if a.finish_only:
        src = pathlib.Path(a.cast)
        lines = src.read_text().splitlines()
        events = finish([json.loads(l) for l in lines[1:] if l.strip()])
        src.write_text(lines[0] + "\n" + "\n".join(json.dumps(e, ensure_ascii=False) for e in events) + "\n")
        print(f"{src}: ending trimmed, end marker added")
        return
    src = pathlib.Path(a.cast)
    out = pathlib.Path(a.out or src.with_name(src.stem + "-fast.cast"))
    marks = {}
    for line in src.with_suffix(".markers").read_text().splitlines():
        t, name = line.split(" ", 1)
        marks[name] = float(t)

    windows = []
    for m0, o0, m1, o1, target in PLAN:
        if m0 in marks and m1 in marks:
            start, end = marks[m0] + o0, marks[m1] + o1
            if end - start > target:
                windows.append((start, end, target / (end - start)))

    lines = src.read_text().splitlines()
    header, events = lines[0], [json.loads(l) for l in lines[1:] if l.strip()]
    # v3 stores each event's delay since the previous one; scale delays that
    # fall inside a window.
    t, kept = 0.0, []
    for ev in events:
        t += ev[0]
        factor = next((f for s, e, f in windows if s <= t < e), 1.0)
        kept.append([round(ev[0] * factor, 6)] + ev[1:])
    kept = finish(kept)
    out.write_text(header + "\n" + "\n".join(json.dumps(e, ensure_ascii=False) for e in kept) + "\n")
    before, after = sum(e[0] for e in events), sum(e[0] for e in kept)
    print(f"{out}: {before/60:.1f} min → {after/60:.1f} min ({len(windows)} stretches compressed)")


if __name__ == "__main__":
    main()
