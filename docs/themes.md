# Themes And Terminal Fonts

amux theme settings control human-facing status glyphs and status-line shape.
They do not change pane state, structured capture JSON, or event payloads.
Agents should keep using semantic fields such as pane state, host, tracked PRs,
and tracked issues instead of parsing rendered glyphs.

Config file: `~/.config/amux/config.toml` unless `AMUX_CONFIG` points
somewhere else.

## Icon Mode

`[theme] icons` selects the renderer icon preset:

```toml
[theme]
icons = "unicode" # ascii | unicode | nerd
```

Valid values:

| Value | Use when | Notes |
| --- | --- | --- |
| `unicode` | Your terminal handles common Unicode symbols. | Default. Uses compact symbols such as `●`, `◇`, `⚡`, and `[copy]`. |
| `ascii` | You need the safest fallback for plain terminals, CI logs, serial consoles, or remote environments with unknown font support. | Uses printable single-cell ASCII markers such as `*`, `.`, `+`, `#`, `I`, and `T`. |
| `nerd` | Your terminal is configured to use a Nerd Font-compatible patched font. | Uses Private Use Area glyphs for pane state, hosts, PRs, issues, tasks, copy mode, and connection state. |

Examples:

```toml
[theme]
icons = "ascii"
```

```toml
[theme]
icons = "unicode"
```

```toml
[theme]
icons = "nerd"
```

amux does not install, select, or manage terminal fonts. `icons = "nerd"` only
tells amux to emit Nerd Font glyphs. The terminal emulator still decides which
font is used to draw those glyphs.

Nerd Font icons and Powerline separators use Unicode Private Use Area code
points. Without a compatible patched font, those glyphs may render as boxes,
question marks, blank cells, or mismatched-width characters. That is a terminal
font setup problem, not a pane, PTY, or capture bug.

## Status Style

`[theme] status_style` selects the status-line preset:

```toml
[theme]
status_style = "compact" # compact | plain | powerline
```

Valid values:

| Value | Use when | Notes |
| --- | --- | --- |
| `compact` | You want the default status-line layout. | Default. Uses normal separators and works with `ascii`, `unicode`, or `nerd` icons. |
| `plain` | You want to avoid Powerline separators. | A non-Powerline fallback style for terminals where separator glyphs are unreliable. |
| `powerline` | Your terminal font renders Powerline separator glyphs correctly. | Uses Powerline separators in pane status lines and the global bar. |

Icon mode and status style are independent:

```toml
[theme]
icons = "nerd"
status_style = "powerline"
```

If pane status separators render as boxes or look misaligned, keep the icon mode
you want and switch the status style back to a non-Powerline value:

```toml
[theme]
icons = "nerd"
status_style = "compact"
```

## Font Diagnostic

Run the local diagnostic to see what your current terminal font renders:

```bash
amux doctor fonts
```

The command prints samples for the `ascii`, `unicode`, and `nerd` icon presets,
plus the Powerline separators used by `status_style = "powerline"`. It does not
connect to an amux server, install fonts, change config, or mutate terminal
settings.

If any sample appears as a box, question mark, missing glyph, blank cell, or
badly aligned glyph, choose a fallback config until the terminal font is fixed:

```toml
[theme]
icons = "ascii"
status_style = "compact"
```

## Text Captures

Text-mode captures are useful for checking the shape of rendered status lines.
The exact colors are omitted here.

Default Unicode icons with compact status:

```text
● [pane-1] #42, LAB-1651 @gpu build



 amux │ SESSION          1 panes │ ? help │ 00:00
```

Nerd icons with compact status:

```text
 [pane-1] 42, LAB-1651 gpu  build



 amux │ SESSION          1 panes │ ? help │ 00:00
```

Nerd icons with Powerline status:

```text
 pane-142, LAB-1651gpu build



 amux  SESSION          1 panes  ? help  00:00
```

If the Nerd or Powerline examples look wrong in your editor or terminal, use the
diagnostic command in the terminal where you run amux. Markdown renderers,
browsers, and code review tools often use different fonts from your terminal.

## Fallback Guidance

Use `icons = "ascii"` and `status_style = "compact"` for CI, logs, remote shells
viewed through unknown terminal stacks, SSH sessions from minimal clients, and
any setup where glyph width matters more than visual density.

Use `icons = "unicode"` with `status_style = "compact"` as the default local
interactive setup. It keeps the existing compact status rendering without
depending on patched font glyphs.

Use `icons = "nerd"` only after confirming that the terminal profile used for
amux is configured with a Nerd Font-compatible patched font. Add
`status_style = "powerline"` only after Powerline separators also render
correctly.

Color and style-string customization is tracked separately in
[LAB-114](https://linear.app/weill-labs/issue/LAB-114/configurable-color-themes-and-style-strings).
