---
name: demo-recording
description: Use when recording, updating, or troubleshooting the hero demo GIF in `demo/`. Covers the full pipeline (asciinema -> Playwright -> GIF), font/rendering issues, and editing the simulated Claude Code sessions.
---

# Demo Recording

## Quick Start

```bash
bash demo/record.sh    # records + converts to GIF in one command
```

**First run** installs Playwright automatically (`cd demo && npm install && npx playwright install chromium`).

**Dependencies**: `brew install asciinema ffmpeg node jq`. The `amux` binary must be on `$PATH`.

## Architecture

```
asciinema rec  ->  .cast file  ->  cast2gif.mjs (Playwright)  ->  hero.gif
```

`cast2gif.mjs` loads the `.cast` file in asciinema-player inside headless Chromium, screenshots at `1/fps` intervals, and assembles frames into a GIF with ffmpeg.

### Why Playwright instead of agg

`agg` (asciinema's official GIF generator) cannot render block element characters (the Claude Code logo), uncommon symbols, or font fallback. The browser's text rendering engine handles all of these correctly.

### Why asciinema v2 format

The converter supports both v2 and v3, but v2 absolute timestamps are easier to inspect manually. Record with `--output-format asciicast-v2`.

### Why real-time playback (not seek)

`asciinema-player.seek()` does not render terminal state -- it produces blank frames. The converter plays at 1x speed and screenshots at intervals, so conversion takes as long as the recording (~20s).

## How driver.sh Works

The driver runs inside asciinema's recorded PTY with a foreground/background split:

1. **Background agent** waits for the amux server socket, then drives the demo via `amux` CLI commands (spawn, split, send-keys, wait idle, capture)
2. **Foreground amux client** renders the TUI into the PTY that asciinema records
3. Agent kills the client (via saved PID) to end the recording

Each Claude pane uses a fake `claude.sh` that shows the startup banner and blocks on `read`. The agent then sends prompts via `amux send-keys` so text appears naturally at the prompt. When `read` completes, a per-pane response script (`resp-server.sh`, `resp-tests.sh`, `resp-review.sh`) simulates Claude Code tool use output with `printf` + `sleep`.

## Tuning the GIF

All tuning flags are in `record.sh` and passed to `cast2gif.mjs`:

```bash
--font "Menlo"         # Font family (browser must have it installed)
--font-size 20         # CSS pixels
--scale 2              # Device pixel ratio (2 = retina)
--window-size 160x40   # cols x rows for the recorded terminal
--fps 8                # Frames per second (lower = smaller file, choppier)
```

**Font matters for block elements.** JetBrains Mono and Fira Code render block characters too narrow. Menlo has better proportions. No browser font perfectly matches terminal emulators, which draw block elements programmatically.

**File size target**: < 1MB for README. Current output is ~700KB at 2x scale.

## Editing the Demo Content

- **Response scripts**: Edit `write_sim_scripts()` in `driver.sh` -- each pane has a response script (`resp-server.sh`, `resp-tests.sh`, `resp-review.sh`).
- **Prompts**: Edit the Phase 2 `send-keys` calls in the `agent()` function.
- **Pane layout**: The `agent()` function uses `spawn`, `focus && split v`, and `focus && split root`. Note that `split root` can rearrange the layout tree, moving panes out of vertical splits and breaking `minimize`.
- **Utility pane**: The rightmost pane content comes from the inline `send-keys` command in `agent()` (line ~178). The `devlog.sh` reference on line ~165 is a no-op (the file is not created). To refactor, add a `devlog.sh` to `write_sim_scripts()` and remove the inline send-keys block.

## Rules

- GIF must be under 1MB for README rendering performance.
- Clean up `.cast` intermediate files after conversion.
- Verify the GIF renders the Claude Code logo and tool indicators correctly before committing.
- Use absolute paths in `record.sh` — relative paths break when the working directory differs.

## Output Checklist

- `demo/hero.gif` generated and under 1MB.
- No `.cast` intermediate files left behind.
- GIF animates correctly (check key frames: startup banners, claude responses, JSON capture).
- `README.md` references `demo/hero.gif` and renders inline on GitHub.

## Troubleshooting

**Blank GIF frames** -- asciinema-player did not load (CDN unreachable or font timeout). Increase the font-loading wait in `cast2gif.mjs`.

**Unicode renders as boxes or `[?]`** -- Using `agg` instead of Playwright, or the browser lacks the font. Use `cast2gif.mjs` and install Noto Sans Symbols 2 if needed.

**Logo looks "smooshed"** -- Font renders block elements with narrow proportions. Switch to Menlo or increase font-size.

**"amux server socket not found"** -- amux binary not on PATH. Run `go build -o ~/.local/bin/amux .` and ensure `~/.local/bin` is in PATH.

**Recording only captures 1-2 events** -- Wrong path to `driver.sh`. `record.sh` uses `${SCRIPT_DIR}/driver.sh` (absolute path).

**"cannot minimize: pane is not in a vertical split"** -- A `split root` rearranged the layout. Reorder splits so vertical splits happen after root splits, or skip minimize.
