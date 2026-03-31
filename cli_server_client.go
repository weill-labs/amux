package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/server"
)

type eventsClientOptions struct {
	reconnect      bool
	initialBackoff time.Duration
	maxBackoff     time.Duration
	maxRetries     int
}

func defaultEventsClientOptions() eventsClientOptions {
	return eventsClientOptions{
		reconnect:      true,
		initialBackoff: 1 * time.Second,
		maxBackoff:     30 * time.Second,
		maxRetries:     10,
	}
}

func parseEventsClientArgs(args []string) ([]string, eventsClientOptions) {
	opts := defaultEventsClientOptions()
	serverArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--no-reconnect" {
			opts.reconnect = false
			continue
		}
		serverArgs = append(serverArgs, arg)
	}

	opts.initialBackoff = overrideDurationFromEnv("AMUX_EVENTS_RECONNECT_INITIAL_BACKOFF", opts.initialBackoff)
	opts.maxBackoff = overrideDurationFromEnv("AMUX_EVENTS_RECONNECT_MAX_BACKOFF", opts.maxBackoff)
	opts.maxRetries = overridePositiveIntFromEnv("AMUX_EVENTS_RECONNECT_MAX_RETRIES", opts.maxRetries)
	if opts.maxBackoff < opts.initialBackoff {
		opts.maxBackoff = opts.initialBackoff
	}
	return serverArgs, opts
}

func overrideDurationFromEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func overridePositiveIntFromEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func runEventsCommand(sessionName string, args []string) {
	serverArgs, opts := parseEventsClientArgs(args)

	conn, err := connectStreamingCommand(sessionName, "events", serverArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux events: %v\n", err)
		os.Exit(1)
	}

	for {
		err := streamCommandOutput(conn, "events")
		if !opts.reconnect {
			return
		}

		emitReconnectEvent()
		conn, err = reconnectStreamingCommand(sessionName, "events", serverArgs, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux events: reconnect failed after %d attempts: %v\n", opts.maxRetries, err)
			os.Exit(1)
		}
	}
}

func reconnectStreamingCommand(sessionName, cmdName string, args []string, opts eventsClientOptions) (net.Conn, error) {
	delay := opts.initialBackoff
	var lastErr error
	for attempt := 1; attempt <= opts.maxRetries; attempt++ {
		time.Sleep(delay)

		conn, err := connectStreamingCommand(sessionName, cmdName, args)
		if err == nil {
			return conn, nil
		}
		lastErr = err

		if delay < opts.maxBackoff {
			delay *= 2
			if delay > opts.maxBackoff {
				delay = opts.maxBackoff
			}
		}
	}
	return nil, lastErr
}

func emitReconnectEvent() {
	data, err := json.Marshal(server.Event{
		Type:      reconnectEventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	fmt.Println(string(data))
}

func dialServer(sessionName string) (net.Conn, error) {
	conn, err := net.Dial("unix", server.SocketPath(sessionName))
	if err != nil {
		return nil, fmt.Errorf("connecting to server: %w", err)
	}
	return conn, nil
}

func connectStreamingCommand(sessionName, cmdName string, args []string) (net.Conn, error) {
	conn, err := dialServer(sessionName)
	if err != nil {
		return nil, err
	}

	if err := server.WriteMsg(conn, newCommandMessage(cmdName, args)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func emitBellMessage(msg *server.Message) bool {
	if msg != nil && msg.Type == server.MsgTypeBell {
		fmt.Fprint(os.Stdout, "\a")
		return true
	}
	return false
}

func streamCommandOutput(conn net.Conn, cmdName string) error {
	defer conn.Close()

	for {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			return err
		}
		if emitBellMessage(msg) {
			continue
		}
		if msg.CmdErr != "" {
			fmt.Fprintf(os.Stderr, "amux %s: %s\n", cmdName, msg.CmdErr)
			os.Exit(1)
		}
		fmt.Print(msg.CmdOutput)
	}
}

func runServerCommand(sessionName, cmdName string, args []string) {
	conn, err := dialServer(sessionName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: %v\n", cmdName, err)
		os.Exit(1)
	}
	defer conn.Close()

	if cmdName == "reload-server" {
		args = prependReloadExecPathArg(reload.ResolveExecutable, args)
	}

	if err := server.WriteMsg(conn, newCommandMessage(cmdName, args)); err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: %v\n", cmdName, err)
		os.Exit(1)
	}

	for {
		reply, err := server.ReadMsg(conn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux %s: %v\n", cmdName, err)
			os.Exit(1)
		}
		if emitBellMessage(reply) {
			continue
		}
		if reply.Type != server.MsgTypeCmdResult {
			continue
		}
		if reply.CmdErr != "" {
			fmt.Fprintf(os.Stderr, "amux %s: %s\n", cmdName, reply.CmdErr)
			os.Exit(1)
		}
		fmt.Print(reply.CmdOutput)
		return
	}
}

func prependReloadExecPathArg(resolve func() (string, error), args []string) []string {
	execPath, err := resolve()
	if err != nil {
		return args
	}
	return append([]string{server.ReloadServerExecPathFlag, execPath}, args...)
}

func newCommandMessage(cmdName string, args []string) *server.Message {
	return &server.Message{
		Type:        server.MsgTypeCommand,
		CmdName:     cmdName,
		CmdArgs:     args,
		ActorPaneID: actorPaneIDFromEnv(),
	}
}

func actorPaneIDFromEnv() uint32 {
	raw := os.Getenv("AMUX_PANE")
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(id)
}
