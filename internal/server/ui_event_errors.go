package server

import "fmt"

func errUnknownUIEvent(name string) error {
	return fmt.Errorf("unknown ui event: %s", name)
}
