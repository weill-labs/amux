package test

func rewriteLegacyHarnessArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}

	switch args[0] {
	case "connection-log":
		return []string{"log", "clients"}
	case "pane-log":
		return []string{"log", "panes"}
	case "set-lead":
		return append([]string{"lead"}, args[1:]...)
	case "unset-lead":
		return []string{"lead", "--clear"}
	case "swap-tree":
		out := append([]string{"swap"}, args[1:]...)
		return append(out, "--tree")
	case "move-up":
		if len(args) == 1 {
			return []string{"move"}
		}
		return []string{"move", args[1], "up"}
	case "move-down":
		if len(args) == 1 {
			return []string{"move"}
		}
		return []string{"move", args[1], "down"}
	case "move-to":
		switch len(args) {
		case 1:
			return []string{"move"}
		case 2:
			return []string{"move", args[1], "--to-column"}
		default:
			return []string{"move", args[1], "--to-column", args[2]}
		}
	case "add-pane":
		return append([]string{"spawn", "--spiral"}, args[1:]...)
	case "split":
		return rewriteLegacySplitArgs(args[1:])
	default:
		return args
	}
}

func rewriteLegacySplitArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"spawn", "--at"}
	}

	out := []string{"spawn", "--at", args[0]}
	for _, arg := range args[1:] {
		switch arg {
		case "root":
			out = append(out, "--root")
		case "v":
			out = append(out, "--vertical")
		case "h":
			out = append(out, "--horizontal")
		default:
			out = append(out, arg)
		}
	}
	return out
}
