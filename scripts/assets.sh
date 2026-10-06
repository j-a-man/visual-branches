#!/usr/bin/env bash
# Regenerates the README images in docs/assets from the demo repository.
# Needs Go, git, and (for the web screenshot) Chrome, Chromium, or Edge.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/docs/assets"
work="$(mktemp -d)"
# The browser may still hold its profile lock for a moment; cleanup is best effort.
trap 'kill "${web_pid:-0}" 2>/dev/null || true; rm -rf "$work" 2>/dev/null || true' EXIT

mkdir -p "$out"
cd "$root"
go build -o "$work/vb" ./cmd/vb
go run ./tools/demo "$work/demo" > /dev/null
repo="$work/demo/webapp"
export VB_GITHUB_FIXTURE="$work/demo/prs.json"
export CLICOLOR_FORCE=1 COLORTERM=truecolor

(cd "$repo" && "$work/vb") | go run ./tools/termshot -title "vb" > "$out/tree.svg"
(cd "$repo" && "$work/vb" show feat/auth-ui) | go run ./tools/termshot -title "vb show feat/auth-ui" > "$out/show.svg"
(cd "$repo" && CLICOLOR_FORCE=0 "$work/vb" --agent) | go run ./tools/termshot -title "vb --agent" > "$out/agent.svg"
go run ./tools/tuishot -C "$repo" -w 150 -h 30 -select feat/auth-ui | go run ./tools/termshot -title "vb tui" > "$out/tui.svg"
(cd "$repo" && "$work/vb" export svg --title webapp) > "$out/map.svg"

browser=""
for candidate in google-chrome chromium chromium-browser msedge \
  "/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe" \
  "/c/Program Files/Google/Chrome/Application/chrome.exe" \
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; do
  if command -v "$candidate" > /dev/null 2>&1 || [ -x "$candidate" ]; then
    browser="$candidate"
    break
  fi
done
if [ -n "$browser" ]; then
  (cd "$repo" && "$work/vb" web --no-open --port 7979) &
  web_pid=$!
  for _ in $(seq 1 60); do
    curl -sf http://127.0.0.1:7979/api/map > /dev/null 2>&1 && break
    sleep 0.5
  done
  rm -f "$out/web.png"
  shot="$out/web.png"
  profile="$work/browser-profile"
  if command -v cygpath > /dev/null 2>&1; then
    shot="$(cygpath -w "$shot")"
    profile="$(cygpath -w "$profile")"
  fi
  # Some browser launchers return before the headless instance finishes, so
  # wait for the screenshot to appear before stopping the server.
  "$browser" --headless=new --disable-gpu --hide-scrollbars --force-dark-mode \
    --user-data-dir="$profile" --no-first-run \
    --window-size=1440,880 --virtual-time-budget=4000 --screenshot="$shot" http://127.0.0.1:7979/ > /dev/null 2>&1 || true
  for _ in $(seq 1 60); do
    [ -s "$out/web.png" ] && break
    sleep 0.5
  done
  sleep 1
else
  echo "no Chrome, Chromium, or Edge found; skipped docs/assets/web.png" >&2
fi
echo "assets written to $out"
