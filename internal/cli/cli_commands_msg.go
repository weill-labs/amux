package cli

import (
	"fmt"
	"io"
	"os"
)

func msgCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"msg": func(inv invocation, args []string) int {
			if wantsMsgHelp(args) {
				fmt.Fprintln(inv.runtime.Stdout, msgHelpUsage(args))
				return 0
			}
			if len(args) == 0 {
				fmt.Fprintln(inv.runtime.Stderr, msgUsage)
				return 1
			}
			serverArgs, err := prepareMsgCLIArgs(inv.runtime.Stdin, args)
			if err != nil {
				fmt.Fprintf(inv.runtime.Stderr, "amux msg: %v\n", err)
				return 1
			}
			return inv.runSessionCommand("msg", serverArgs)
		},
	}
}

func wantsMsgHelp(args []string) bool {
	return len(args) == 1 && isHelpFlag(args[0]) ||
		len(args) == 2 && isHelpFlag(args[1])
}

func msgHelpUsage(args []string) string {
	if len(args) == 2 && isHelpFlag(args[1]) {
		switch args[0] {
		case "send":
			return msgSendUsage
		case "reply":
			return msgReplyUsage
		case "inbox", "list":
			return msgInboxUsage
		case "drain-status":
			return msgDrainStatusUsage
		case "read":
			return msgReadUsage
		case "ack":
			return msgAckUsage
		case "thread":
			return msgThreadUsage
		}
	}
	return msgUsage
}

func prepareMsgCLIArgs(stdin io.Reader, args []string) ([]string, error) {
	if len(args) == 0 || (args[0] != "send" && args[0] != "reply") {
		return args, nil
	}

	out := make([]string, 0, len(args)+2)
	out = append(out, args[0])
	var bodySet bool
	var bodyFileSet bool
	var bodyFileData []byte

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--body":
			value, next, err := msgCLIRequiredValue(args, i, "--body")
			if err != nil {
				return nil, err
			}
			if bodySet || bodyFileSet {
				return nil, fmt.Errorf("--body, --body-file, and stdin are mutually exclusive")
			}
			bodySet = true
			out = append(out, "--body", value)
			i = next
		case "--body-file":
			value, next, err := msgCLIRequiredValue(args, i, "--body-file")
			if err != nil {
				return nil, err
			}
			if bodySet || bodyFileSet {
				return nil, fmt.Errorf("--body, --body-file, and stdin are mutually exclusive")
			}
			data, err := os.ReadFile(value)
			if err != nil {
				return nil, fmt.Errorf("reading --body-file: %w", err)
			}
			bodyFileSet = true
			bodyFileData = data
			i = next
		default:
			out = append(out, args[i])
		}
	}

	if shouldReadMsgStdin(stdin) {
		if bodySet || bodyFileSet {
			return nil, fmt.Errorf("--body, --body-file, and stdin are mutually exclusive")
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		out = append(out, "--body", string(data))
		return out, nil
	}
	if bodyFileSet {
		out = append(out, "--body", string(bodyFileData))
	}
	return out, nil
}

func msgCLIRequiredValue(args []string, i int, name string) (string, int, error) {
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("missing value for %s", name)
	}
	return args[i+1], i + 1, nil
}

func shouldReadMsgStdin(stdin io.Reader) bool {
	if stdin == nil {
		return false
	}
	f, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}
