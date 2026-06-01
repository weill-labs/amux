package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

const remoteDiscoverProbeScript = `set -u
session=$1
if [ -z "$session" ]; then
	printf 'ERR\tsession_missing\n'
	exit 0
fi
uid=$(id -u 2>/dev/null) || {
	printf 'ERR\tuid\n'
	exit 0
}
socket="/tmp/amux-${uid}/${session}"
if [ ! -S "$socket" ]; then
	printf 'ERR\tsocket_missing\t%s\n' "$socket"
	exit 0
fi
if ! command -v nc >/dev/null 2>&1; then
	printf 'ERR\tnc_missing\n'
	exit 0
fi
nc_help=$(nc -h 2>&1 || true)
case "$nc_help" in
	*"-U"*) ;;
	*)
		printf 'ERR\tnc_no_unix\n'
		exit 0
		;;
esac
printf 'OK\t%s\t%s\n' "$uid" "$socket"
`

type remoteDiscoverArgs struct {
	name      string
	ssh       string
	session   string
	printOnly bool
}

type remoteDiscoverResult struct {
	name string
	uid  string
	host config.Host
}

type remoteDiscoverRunner interface {
	Run(ctx context.Context, sshTarget, session, script string) (string, error)
}

type sshRemoteDiscoverRunner struct{}

func (sshRemoteDiscoverRunner) Run(ctx context.Context, sshTarget, session, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", sshTarget, "--", "sh", "-s", "--", session)
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return string(out), nil
}

func parseRemoteDiscoverArgs(args []string) (remoteDiscoverArgs, error) {
	if len(args) == 0 {
		return remoteDiscoverArgs{}, errors.New(remoteDiscoverUsage)
	}
	parsed := remoteDiscoverArgs{
		name:    args[0],
		session: DefaultSessionName,
	}
	if parsed.name == "" || strings.HasPrefix(parsed.name, "-") {
		return remoteDiscoverArgs{}, errors.New(remoteDiscoverUsage)
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--ssh":
			if i+1 >= len(args) {
				return remoteDiscoverArgs{}, errors.New(remoteDiscoverUsage)
			}
			parsed.ssh = args[i+1]
			i++
		case "--session":
			if i+1 >= len(args) {
				return remoteDiscoverArgs{}, errors.New(remoteDiscoverUsage)
			}
			parsed.session = args[i+1]
			i++
		case "--print":
			parsed.printOnly = true
		default:
			return remoteDiscoverArgs{}, errors.New(remoteDiscoverUsage)
		}
	}
	if parsed.ssh == "" {
		parsed.ssh = parsed.name
	}
	parsed.ssh = strings.TrimSpace(parsed.ssh)
	parsed.session = strings.TrimSpace(parsed.session)
	if parsed.ssh == "" || strings.HasPrefix(parsed.ssh, "-") || parsed.session == "" {
		return remoteDiscoverArgs{}, errors.New(remoteDiscoverUsage)
	}
	if strings.Contains(parsed.session, "/") {
		return remoteDiscoverArgs{}, errors.New("remote session must not contain '/'")
	}
	return parsed, nil
}

func runRemoteDiscover(ctx *CommandContext) commandpkg.Result {
	parsed, err := parseRemoteDiscoverArgs(ctx.Args[1:])
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	discoverCtx, cancel := context.WithTimeout(ctx.context(), remoteCommandTimeout)
	defer cancel()
	result, err := discoverRemoteHost(discoverCtx, parsed, sshRemoteDiscoverRunner{})
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if !parsed.printOnly {
		if err := saveRemoteHostConfig(ctx, result.name, result.host); err != nil {
			return commandpkg.Result{Err: err}
		}
	}
	return commandpkg.Result{Output: formatRemoteDiscoverResult(result, parsed.printOnly)}
}

func discoverRemoteHost(ctx context.Context, args remoteDiscoverArgs, runner remoteDiscoverRunner) (remoteDiscoverResult, error) {
	if runner == nil {
		return remoteDiscoverResult{}, errors.New("remote discovery runner is required")
	}
	out, err := runner.Run(ctx, args.ssh, args.session, remoteDiscoverProbeScript)
	if err != nil {
		return remoteDiscoverResult{}, fmt.Errorf("checking remote %q over ssh: %w", args.ssh, err)
	}
	return parseRemoteDiscoverResponse(args, out)
}

func parseRemoteDiscoverResponse(args remoteDiscoverArgs, output string) (remoteDiscoverResult, error) {
	line := strings.TrimSpace(output)
	fields := strings.Split(line, "\t")
	if len(fields) == 0 || fields[0] == "" {
		return remoteDiscoverResult{}, fmt.Errorf("unexpected discovery response from %s: %q", args.ssh, line)
	}
	switch fields[0] {
	case "OK":
		if len(fields) != 3 {
			return remoteDiscoverResult{}, fmt.Errorf("unexpected discovery response from %s: %q", args.ssh, line)
		}
		socket := strings.TrimSpace(fields[2])
		if !strings.HasPrefix(socket, "/") {
			return remoteDiscoverResult{}, fmt.Errorf("unexpected discovery socket from %s: %q", args.ssh, socket)
		}
		return remoteDiscoverResult{
			name: args.name,
			uid:  fields[1],
			host: config.Host{
				SSH:        args.ssh,
				Session:    args.session,
				SocketPath: socket,
			},
		}, nil
	case "ERR":
		return remoteDiscoverError(args, fields, line)
	default:
		return remoteDiscoverResult{}, fmt.Errorf("unexpected discovery response from %s: %q", args.ssh, line)
	}
}

func remoteDiscoverError(args remoteDiscoverArgs, fields []string, line string) (remoteDiscoverResult, error) {
	if len(fields) < 2 {
		return remoteDiscoverResult{}, fmt.Errorf("unexpected discovery response from %s: %q", args.ssh, line)
	}
	switch fields[1] {
	case "session_missing":
		return remoteDiscoverResult{}, errors.New("remote session is required")
	case "uid":
		return remoteDiscoverResult{}, fmt.Errorf("remote host %s could not report its uid with id -u", args.ssh)
	case "socket_missing":
		socket := ""
		if len(fields) > 2 {
			socket = fields[2]
		}
		return remoteDiscoverResult{}, fmt.Errorf("remote amux socket %s does not exist for session %q on %s", socket, args.session, args.ssh)
	case "nc_missing":
		return remoteDiscoverResult{}, fmt.Errorf("remote host %s does not have nc in PATH; install netcat with Unix socket (-U) support", args.ssh)
	case "nc_no_unix":
		return remoteDiscoverResult{}, fmt.Errorf("remote host %s has nc but it does not support -U; install a netcat variant with Unix socket support", args.ssh)
	default:
		return remoteDiscoverResult{}, fmt.Errorf("unexpected discovery response from %s: %q", args.ssh, line)
	}
}

func formatRemoteDiscoverResult(result remoteDiscoverResult, printOnly bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Discovered remote %s\n", result.name)
	if result.uid != "" {
		fmt.Fprintf(&b, "UID: %s\n", result.uid)
	}
	fmt.Fprintf(&b, "Socket: %s\n", result.host.SocketPath)
	fmt.Fprintf(&b, "%s\n", remoteDiscoverAddCommand(result))
	if printOnly {
		fmt.Fprintf(&b, "Not saved (--print)\n")
	} else {
		fmt.Fprintf(&b, "Saved remote %s\n", result.name)
	}
	return b.String()
}

func remoteDiscoverAddCommand(result remoteDiscoverResult) string {
	return fmt.Sprintf("amux remote add %s --ssh %s --session %s --socket %s",
		result.name, result.host.SSH, result.host.Session, result.host.SocketPath)
}
