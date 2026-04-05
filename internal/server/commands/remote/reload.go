package remote

import (
	"fmt"

	"github.com/weill-labs/amux/internal/reload"
)

const ReloadServerExecPathFlag = "--exec-path"

func RequestedReloadExecPath(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] != ReloadServerExecPathFlag {
			continue
		}
		if i+1 >= len(args) {
			return "", fmt.Errorf("missing value for %s", ReloadServerExecPathFlag)
		}
		return reload.NormalizeExecutablePath(args[i+1])
	}
	return "", nil
}
