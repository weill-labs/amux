package sshutil

import (
	"os"
	"os/user"

	charmlog "github.com/charmbracelet/log"
)

// DefaultSSHUser returns the current OS user for implicit SSH targets.
// If user lookup fails, it logs the error and falls back to $USER or empty.
func DefaultSSHUser() string {
	return defaultSSHUser(user.Current, os.Getenv, func(err error) {
		charmlog.Warn("failed to determine current ssh user", "error", err)
	})
}

func defaultSSHUser(
	currentUser func() (*user.User, error),
	getenv func(string) string,
	logLookupError func(error),
) string {
	usr, err := currentUser()
	if err == nil && usr != nil && usr.Username != "" {
		return usr.Username
	}
	if err != nil && logLookupError != nil {
		logLookupError(err)
	}
	return getenv("USER")
}
