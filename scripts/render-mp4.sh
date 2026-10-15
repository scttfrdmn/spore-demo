#!/bin/bash
# render-mp4.sh — sped-up .cast and MP4 from a recording made by record-demo.sh.
#
# Usage: scripts/render-mp4.sh [recordings/spore-demo.cast]
# Needs: agg, ffmpeg (brew install agg ffmpeg)
set -euo pipefail
cd "$(dirname "$0")/.."
cast=${1:-recordings/spore-demo.cast}
base=${cast%.cast}
scripts/speedup-cast.py --finish-only "$cast"   # end on the last dashboard frame
scripts/speedup-cast.py "$cast" -o "$base-fast.cast"
# Apple Symbols supplies glyphs Menlo lacks (e.g. the ⏲ idle countdown)
agg --font-family "Menlo,Apple Symbols" --font-size 14 --fps-cap 15 --last-frame-duration 5 \
  "$base-fast.cast" "$base-fast.gif" >/dev/null
ffmpeg -y -loglevel error -i "$base-fast.gif" -movflags +faststart -pix_fmt yuv420p \
  -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2" -c:v libx264 -crf 20 "$base-fast.mp4"
rm "$base-fast.gif"
ls -lh "$base.cast" "$base-fast.cast" "$base-fast.mp4"
