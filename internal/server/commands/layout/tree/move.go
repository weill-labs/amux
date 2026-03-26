package tree

import "fmt"

const MoveUsage = "usage: move <pane> --before <target> | move <pane> --after <target>"
const MoveToUsage = "usage: move-to <pane> <target>"

func ParseMoveArgs(args []string) (paneRef, targetRef string, before bool, err error) {
	if len(args) != 3 {
		return "", "", false, fmt.Errorf(MoveUsage)
	}

	paneRef = args[0]
	targetRef = args[2]

	switch args[1] {
	case "--before":
		before = true
	case "--after":
	default:
		return "", "", false, fmt.Errorf(MoveUsage)
	}

	return paneRef, targetRef, before, nil
}

func ParseMoveToArgs(args []string) (paneRef, targetRef string, err error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf(MoveToUsage)
	}
	return args[0], args[1], nil
}
