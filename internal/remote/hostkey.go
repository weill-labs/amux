package remote

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback returns an ssh.HostKeyCallback that implements TOFU
// (Trust On First Use) against the OpenSSH known_hosts file.
//
// On each invocation the callback re-reads knownHostsPath to pick up
// entries written by concurrent connections. If knownHostsPath is "",
// defaultKnownHostsPath() is used.
func hostKeyCallback(knownHostsPath string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		path := knownHostsPath
		if path == "" {
			var err error
			path, err = defaultKnownHostsPath()
			if err != nil {
				return fmt.Errorf("amux: cannot determine known_hosts path: %w", err)
			}
		}

		if _, err := os.Stat(path); err == nil {
			cb, err := knownhosts.New(path)
			if err != nil {
				return fmt.Errorf("amux: failed to parse %s: %w", path, err)
			}

			err = cb(hostname, remote, key)
			if err == nil {
				return nil // known host, key matches
			}

			var revokedErr *knownhosts.RevokedError
			if errors.As(err, &revokedErr) {
				return err // hard reject
			}

			var keyErr *knownhosts.KeyError
			if errors.As(err, &keyErr) {
				if len(keyErr.Want) > 0 {
					return hostKeyChangedError(hostname, keyErr)
				}
				// len(Want) == 0: unknown host, fall through to TOFU
			} else {
				return err // unexpected error, propagate
			}
		}

		// TOFU: trust this key and record it
		if err := appendKnownHost(path, hostname, key); err != nil {
			return fmt.Errorf("amux: failed to save host key: %w", err)
		}
		fmt.Fprintf(os.Stderr, "amux: trusting new host key for %s (%s)\n", hostname, key.Type())
		return nil
	}
}

// hostKeyChangedError formats a user-facing error when a host key has changed.
func hostKeyChangedError(hostname string, keyErr *knownhosts.KeyError) error {
	want := keyErr.Want[0]
	return fmt.Errorf(`amux: SSH host key verification failed for %s
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!    @
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
The host key for %s has changed.
Known key: %s in %s:%d
To fix: remove the old key with
  ssh-keygen -R %s`, hostname, hostname, want.Key.Type(), want.Filename, want.Line, hostname)
}

// appendKnownHost writes a new known_hosts entry. It creates the parent
// directory and file if they don't exist.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, knownhosts.Line([]string{hostname}, key))
	return err
}

// defaultKnownHostsPath returns ~/.ssh/known_hosts.
func defaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}
