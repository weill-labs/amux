# amux Operations

This guide covers the Linux operating procedures for keeping long-running amux
servers recoverable under host memory pressure.

## systemd User Service

The user-scoped unit in `packaging/systemd/amux@.service` runs one amux server
per named session. The instance name becomes the amux session name, so
`amux@main.service` starts `amux _server main`.

Install it for the current user:

```bash
mkdir -p ~/.config/systemd/user
cp packaging/systemd/amux@.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now amux@main.service
systemctl --user status amux@main.service
```

The unit sets `OOMScoreAdjust=-500`, `MemoryHigh=2G`, `MemoryMax=4G`,
`Restart=on-failure`, and `RestartSec=2s`. It uses `/usr/bin/env amux` with a
PATH that includes `~/.local/bin`, which matches the default `make install` and
release installer location. If amux is installed somewhere else, add a user
drop-in that adjusts `PATH` or `ExecStart`.

On a standard Linux install, the cgroup memory controls are the enforceable
protection in this user unit. `MemoryHigh=2G` and `MemoryMax=4G` are applied by
systemd through the user slice on cgroup v2 systems. `OOMScoreAdjust=-500` is
included as the preferred process-level protection, but setting a negative
`oom_score_adj` requires `CAP_SYS_RESOURCE`. Most `systemd --user` managers do
not have that capability, so systemd may log `Failed to set OOM score adjust`
and continue starting amux without applying the negative score. If that setting
must be enforced, run amux from a system-scope unit or delegate the capability
through site-specific policy; otherwise rely on the documented user-unit memory
limits and restart behavior.

Useful commands:

```bash
journalctl --user -u amux@main.service -f
systemctl --user restart amux@main.service
systemctl --user disable --now amux@main.service
loginctl enable-linger "$USER"
```

Only enable linger when the server should keep running after the user logs out.

## Diagnose An OOM Outage

When amux disappears under host memory pressure, start with the kernel log:

```bash
sudo grep -E 'Out of memory|oom-reaper|oom-invocation' /var/log/kern.log
```

If the distro does not write `/var/log/kern.log`, use the kernel journal:

```bash
sudo journalctl -k -g 'Out of memory|oom-reaper|oom-invocation'
```

Look for a sequence that names `amux` in the killed process line and then an
`oom-reaper` line for the same pid. Useful surrounding evidence includes the
server log and the newest crash checkpoint:

```bash
ls -lh /tmp/amux-$(id -u)/main.log*
ls -lt ~/.local/state/amux/*_main.json
```

If `XDG_STATE_HOME` is set, crash checkpoints live under
`$XDG_STATE_HOME/amux/` instead of `~/.local/state/amux/`.

The server log should include `checkpoint_kind:"crash"` records for successful
crash checkpoint writes and restore attempts. If the systemd unit is installed,
also check the unit restart and memory status:

```bash
systemctl --user status amux@main.service
journalctl --user -u amux@main.service --since '1 hour ago'
```

## Crash Checkpoint Recovery

amux maintains two recovery paths:

- Hot reload uses `internal/reload/reload.go` to watch the installed binary.
  When the binary changes, the server writes a durable crash checkpoint, writes
  a reload checkpoint with live file descriptors, clears close-on-exec on those
  descriptors, and execs the new binary.
- Crash recovery uses JSON checkpoints under `~/.local/state/amux/`. The
  checkpoint coordinator writes them on a debounce, periodically, and during
  shutdown. The audit log records these writes as `checkpoint_kind:"crash"`.

After an OOM kill, the next `amux` attach or `systemd --user` restart checks for
a stale or missing socket plus a crash checkpoint. Local panes cannot keep their
old process ids or PTY file descriptors after the server process dies, so crash
recovery starts fresh shells, restores pane metadata, retained history, and the
last visible screen. If a pane had a foreground process at crash time, amux adds
an `amux: previous process lost during crash recovery` notice and archives the
pre-crash screen in history.

For a manual recovery check:

```bash
amux wait checkpoint
amux capture --format json
grep -E '"event":"checkpoint_(write|restore)"|"checkpoint_kind":"crash"' /tmp/amux-$(id -u)/main.log*
```

## Stale Pane References

LAB-1593 included a pane like `pane-481` that was skipped during crash recovery
because the saved shell path was `/bin/bash` and that binary was missing on the
host. The current behavior logs the skipped pane and continues restoring the
rest of the session. LAB-1594 tracks the follow-up fix so a missing saved shell
binary can fall back instead of dropping the pane.

Diagnose skipped panes from the server log:

```bash
grep -E 'crash recovery skipped pane|/bin/bash|pane_id' /tmp/amux-$(id -u)/main.log*
amux list --no-cwd
amux capture --format json
```

If the skipped pane no longer appears in `amux list`, clean up any external
references to that pane in orchestration metadata and spawn a replacement pane
with a valid shell. If a stale or exited pane still appears in amux, capture the
session first, confirm no useful process remains, and then use the normal pane
cleanup path:

```bash
amux capture --format json pane-481
amux kill --cleanup pane-481
amux spawn --name pane-481-recovered
```

Do not restart orca or kill worker processes just to clear metadata. Preserve
the log and checkpoint evidence first, then replace only the missing pane.
