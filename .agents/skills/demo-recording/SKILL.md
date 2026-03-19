---
name: demo-recording
description: Use when recording, updating, or troubleshooting the hero demo GIF in `demo/`. Covers the full pipeline (asciinema → Playwright → GIF), font/rendering issues, and editing the simulated Claude Code sessions.
---

# Demo Recording

Use this skill when the task involves `demo/hero.gif`, `demo/record.sh`, `demo/driver.sh`, `demo/cast2gif.mjs`, or any work on the README hero GIF.

## Quick Start

```bash
bash demo/record.sh    # one command — records + converts to GIF
```

**First run** installs Playwright automatically (`cd demo && npm install && npx playwright install chromium`).

**Dependencies**: `brew install asciinema ffmpeg node`. The `amux` binary must be on `$PATH`.

## Architecture

The pipeline has three stages:

```
asciinema rec  →  .cast file  →  cast2gif.mjs (Playwright)  →  hero.gif
                                      │
                                 asciinema-player
                                 in headless Chrome
                                 screenshots at 8fps
```

### Why Playwright instead of agg

`agg` (asciinema's official GIF generator) cannot render:
- Block element characters (▐▛███▜▌ — the Claude Code logo)
- Uncommon symbols (⏺ ❯) — renders as `[?]` or wrong glyphs
- No font fallback — missing glyphs silently break

`cast2gif.mjs` uses Playwright + headless Chromium + `asciinema-player` web component. The browser's text rendering engine handles Unicode and font fallback correctly.

### Why asciinema v2 format

Record with `--output-format asciicast-v2`. The v3 format uses delta timestamps which the asciinema-player handles but adds unnecessary complexity in `cast2gif.mjs`. v2 uses absolute timestamps.

### Why real-time playback (not seek)

`asciinema-player.seek()` does not render terminal state — it produces blank frames. The converter plays the recording at 1x speed and screenshots at `1/fps` intervals. This means conversion takes as long as the recording (~20s), but frames are correct.

## Files

| File | Purpose |
|------|---------|
| `demo/record.sh` | One-command recording wrapper. Runs asciinema + cast2gif. |
| `demo/driver.sh` | Runs inside asciinema's PTY. Launches amux TUI (foreground) + background agent that drives it via CLI. |
| `demo/cast2gif.mjs` | Playwright-based converter. Loads .cast in asciinema-player, screenshots each frame, assembles GIF with ffmpeg. |
| `demo/hero.sh` | Standalone CLI demo for live presentations (not used for recording). |
| `demo/hero.gif` | The output GIF, committed to the repo. |
| `demo/package.json` | Playwright dependency for cast2gif.mjs. |

## How driver.sh Works

The driver uses a foreground/background split inside asciinema's recorded PTY:

1. **Background agent** waits for the amux server socket, then drives the demo via `amux` CLI commands (spawn, split, send-keys, wait-idle, capture)
2. **Foreground amux client** renders the TUI into the PTY that asciinema is recording
3. Agent kills the client (via saved PID) to end the recording

### Two-phase Claude Code simulation

Each Claude pane uses a fake `claude.sh` script that:
1. **Phase 1**: Shows the Claude Code startup banner (block character logo) + `>` prompt, then blocks on `read`
2. **Phase 2**: Agent sends the prompt via `amux send-keys` — the text appears at the prompt, `read` completes, response script runs

This mimics the real workflow: launch claude interactively, then an agent types prompts into it.

### Response scripts

Each pane has a separate response script (`resp-server.sh`, `resp-tests.sh`, `resp-review.sh`) that uses `printf` + `sleep` to simulate Claude Code tool use output (● Write, ● Read, ● Run, ● Edit).

## Tuning the GIF

### Font and scale

In `record.sh`:
```bash
--font "Menlo"         # Font family (browser must have it installed)
--font-size 20         # CSS pixels
--scale 2              # Device pixel ratio (2 = retina)
```

**Font matters for block elements.** JetBrains Mono and Fira Code render block characters (▐▛███▜▌) too narrow ("smooshed"). Menlo has better proportions. Terminal emulators like Ghostty draw block elements programmatically — no browser font perfectly matches.

### Terminal size

In `record.sh`:
```bash
--window-size 160x40   # cols x rows for the recorded terminal
```

This determines how many columns each pane gets. With 4 panes (3 columns + bottom row), each column is ~53 chars wide.

### Frame rate and duration

In `record.sh`:
```bash
--fps 8                # Frames per second in the GIF
```

Lower fps = smaller file but choppier animation. 8 fps is a good balance. The recording duration is controlled by sleep timings in `driver.sh`.

### GIF file size

Target < 1MB for README. Current output is ~700KB at 2x scale. Reducing `--scale 1` halves the size but loses retina sharpness.

## Editing the Demo Content

### Change what Claude Code "does"

Edit the response scripts in `driver.sh` inside `write_sim_scripts()`:
- `resp-server.sh` — server creation output
- `resp-tests.sh` — test results
- `resp-review.sh` — security review output

### Change the prompts

Edit the Phase 2 send-keys in the `agent()` function:
```bash
"$AMUX" -s "$session" send-keys pane-1 "create an Express API server" Enter
```

### Change the pane layout

The `agent()` function controls layout:
- `spawn --name NAME --task TASK` — adds a pane (horizontal split from root)
- `focus PANE && split v` — vertical split on the focused pane
- `focus PANE && split root` — root-level horizontal split

**Caution**: `split root` can rearrange the layout tree. Panes that were in a vertical split may end up in a horizontal split after a root split. This breaks `minimize`.

### Change the utility pane

The rightmost pane runs a dev server log simulation. Edit the inline `send-keys` command in the agent function, or create a `devlog.sh` script in `write_sim_scripts()`.

## Troubleshooting

### Blank GIF frames
- The Playwright converter screenshots are all black
- **Cause**: asciinema-player didn't load (CDN unreachable, or font loading timeout)
- **Fix**: Check network access. Increase the font-loading wait in `cast2gif.mjs` (`await page.waitForTimeout(2000)`)

### Unicode renders as boxes or `[?]`
- **Cause**: Using `agg` instead of the Playwright converter, or the browser doesn't have the font
- **Fix**: Use `cast2gif.mjs`. Install Fira Code or Noto Sans Symbols 2 if specific glyphs are missing.

### Logo looks "smooshed"
- **Cause**: Font renders block elements with narrow proportions
- **Fix**: Switch to Menlo or increase font-size. Or accept the difference — terminals draw block elements programmatically while browsers use font glyphs.

### "amux server socket not found"
- **Cause**: amux binary not on PATH, or the server failed to start within 10s
- **Fix**: `go build -o ~/.local/bin/amux .` and ensure `~/.local/bin` is in PATH

### Recording only captures 1-2 events
- **Cause**: Wrong path to `driver.sh` in the `--command` arg
- **Fix**: `record.sh` uses `${SCRIPT_DIR}/driver.sh` (absolute path). Don't run with relative paths from wrong directory.

### "cannot minimize: pane is not in a vertical split"
- **Cause**: A `split root` rearranged the layout, moving the target pane out of a vertical split
- **Fix**: Either reorder split operations so vertical splits happen after root splits, or skip minimize.
