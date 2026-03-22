package tree

import "fmt"

const MoveUsage = "usage: move <pane> --before <target> | move <pane> --after <target>"

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
