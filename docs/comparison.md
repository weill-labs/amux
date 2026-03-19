# amux vs tmux vs Zellij

## Feature matrix

| Feature | amux | tmux | Zellij |
|---------|------|------|--------|
| Structured JSON capture | Yes — `capture --format json` | No — raw text + ANSI escapes | No |
| Blocking wait primitives | Yes — `wait-idle`, `wait-busy`, `wait-for` | No — requires polling | No |
| Push-based event stream | Yes — `amux events` (NDJSON) | No — `wait-for` but no structured events | No |
| Idle/busy detection | Yes — per-pane, in capture + events | No | No |
| Session persistence | Yes | Yes | Yes |
| Multiple windows | Yes | Yes | Yes (tabs) |
| Splits | Vertical, horizontal, root-level | Vertical, horizontal | Vertical, horizontal |
| Remote pane hosting (SSH) | Yes — `split --host` | No (use ssh manually) | No |
| Mouse support | Click, drag resize, scroll | Click, drag resize, scroll | Click, drag resize, scroll |
| Plugin/extension system | Hooks (`set-hook`) | Hooks (extensive) | WASM plugins |
| Web UI | No | No | Yes |
| Status bar customization | Minimal (session bar) | Extensive (scripted status bar) | Configurable |
| Session groups | No | Yes | No |
| Copy mode | Yes (vi keys) | Yes (vi/emacs keys) | Yes (vi keys) |
| Configurable keybindings | Yes — presets + per-key overrides | Yes — full rebinding | Yes — KDL config |
| Hot-reload binary | Yes — automatic on rebuild | No | No (restart needed) |

## When to use each

**tmux** is the right choice when you need maximum compatibility and breadth. It runs everywhere, has decades of ecosystem support, and handles edge cases that newer tools haven't encountered. If your workflow is human-only terminal multiplexing, tmux is hard to beat.

**Zellij** is the right choice when you want a richer workspace UX out of the box. Floating panes, a web UI, WASM plugins, and beginner-friendly defaults make it approachable. It targets the interactive developer experience.

**amux** is the right choice when agents need to interact with terminal sessions programmatically. Structured capture, blocking waits, and push-based events eliminate the polling loops and regex parsing that make tmux-based agent tooling brittle. If your workflow is human+agent pairing on a shared screen, amux provides the primitives that tmux and Zellij don't.

## What amux does not aim to replace

amux does not replicate tmux's full feature surface. Session groups, extensive status bar scripting, and the broader tmux plugin ecosystem are outside scope. amux focuses on the subset of multiplexer features that matter for human+agent pairing, plus the agent API layer that doesn't exist in other tools.
