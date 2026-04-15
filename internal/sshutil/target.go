package sshutil

import (
	"fmt"
	"strings"
)

type SSHTarget struct {
	User    string
	Host    string
	Port    string
	Session string
}

func ParseTarget(raw, defaultUser string) (SSHTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SSHTarget{}, fmt.Errorf("ssh target is required")
	}

	target := SSHTarget{
		User:    defaultUser,
		Port:    "22",
		Session: "main",
	}
	if target.User == "" {
		target.User = "ubuntu"
	}

	if at := strings.LastIndex(raw, "@"); at >= 0 {
		if at == 0 {
			return SSHTarget{}, fmt.Errorf("ssh target user is required")
		}
		target.User = raw[:at]
		raw = raw[at+1:]
	}
	if target.User == "" {
		return SSHTarget{}, fmt.Errorf("ssh target user is required")
	}
	if raw == "" {
		return SSHTarget{}, fmt.Errorf("ssh target host is required")
	}

	host, session, err := splitTargetHostSession(raw)
	if err != nil {
		return SSHTarget{}, err
	}
	target.Host = host
	if session != "" {
		target.Session = session
	}
	return target, nil
}

func splitTargetHostSession(raw string) (host, session string, err error) {
	if strings.HasPrefix(raw, "[") {
		end := strings.Index(raw, "]")
		if end < 0 {
			return "", "", fmt.Errorf("invalid ssh target %q", raw)
		}
		host = raw[1:end]
		rest := raw[end+1:]
		switch {
		case rest == "":
			return host, "", nil
		case !strings.HasPrefix(rest, ":"):
			return "", "", fmt.Errorf("invalid ssh target %q", raw)
		default:
			session = rest[1:]
		}
	} else {
		parts := strings.Split(raw, ":")
		switch len(parts) {
		case 1:
			host = parts[0]
		case 2:
			host = parts[0]
			session = parts[1]
		default:
			return "", "", fmt.Errorf("invalid ssh target %q", raw)
		}
	}

	if host == "" {
		return "", "", fmt.Errorf("ssh target host is required")
	}
	if session == "" && strings.HasSuffix(raw, ":") {
		return "", "", fmt.Errorf("ssh target session is required")
	}
	if strings.Contains(session, ":") {
		return "", "", fmt.Errorf("invalid ssh target %q", raw)
	}
	return host, session, nil
}
