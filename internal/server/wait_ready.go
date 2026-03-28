package server

import (
	"fmt"
	"strings"
	"time"
)

type sendKeysWaitTarget int

const (
	sendKeysNoWait sendKeysWaitTarget = iota
	sendKeysWaitIdle
	sendKeysWaitInputIdle
)

type sendKeysOptions struct {
	waitTarget  sendKeysWaitTarget
	waitTimeout time.Duration
	delayFinal  time.Duration
	hexMode     bool
	keys        []string
}

func parseSendKeysArgs(args []string) (sendKeysOptions, error) {
	opts := sendKeysOptions{waitTimeout: 10 * time.Second}
	timeoutSet := false
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--wait":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --wait")
			}
			i++
			switch args[i] {
			case "idle":
				opts.waitTarget = sendKeysWaitIdle
			case "ui=input-idle":
				opts.waitTarget = sendKeysWaitInputIdle
			default:
				return sendKeysOptions{}, fmt.Errorf("send-keys: unsupported --wait target %q (want idle or ui=input-idle)", args[i])
			}
			i++
		case "--wait-ready":
			return sendKeysOptions{}, fmt.Errorf("send-keys: --wait-ready was removed; use --wait idle")
		case "--timeout":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return sendKeysOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.waitTimeout = timeout
			timeoutSet = true
			i++
		case "--delay-final":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --delay-final")
			}
			i++
			delay, err := time.ParseDuration(args[i])
			if err != nil {
				return sendKeysOptions{}, fmt.Errorf("invalid delay-final: %s", args[i])
			}
			opts.delayFinal = delay
			i++
		case "--hex":
			opts.hexMode = true
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return sendKeysOptions{}, fmt.Errorf("unknown flag: %s", args[i])
			}
			opts.keys = append(opts.keys, args[i:]...)
			i = len(args)
		}
	}

	if timeoutSet && opts.waitTarget == sendKeysNoWait {
		return sendKeysOptions{}, fmt.Errorf("send-keys: --timeout requires --wait idle or --wait ui=input-idle")
	}
	return opts, nil
}
