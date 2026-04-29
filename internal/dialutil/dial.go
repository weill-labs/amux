package dialutil

import (
	"context"
	"log"
	"net"
	"os"
	"time"
)

const (
	EnvDialTimeout        = "AMUX_DIAL_TIMEOUT"
	DefaultDialTimeout    = 2 * time.Second
	StaleProbeDialTimeout = 500 * time.Millisecond
)

func TimeoutFromEnv(defaultTimeout time.Duration) time.Duration {
	return TimeoutFromEnvWithLogger(defaultTimeout, log.Printf)
}

func TimeoutFromEnvWithLogger(defaultTimeout time.Duration, warnf func(string, ...any)) time.Duration {
	value := os.Getenv(EnvDialTimeout)
	if value == "" {
		return defaultTimeout
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		warnInvalidTimeout(warnf, value, defaultTimeout, err.Error())
		return defaultTimeout
	}
	if timeout <= 0 {
		warnInvalidTimeout(warnf, value, defaultTimeout, "duration must be positive")
		return defaultTimeout
	}
	return timeout
}

func DialUnix(path string) (net.Conn, error) {
	return DialUnixWithDefault(path, DefaultDialTimeout)
}

func DialUnixStaleProbe(path string) (net.Conn, error) {
	return DialUnixWithDefault(path, StaleProbeDialTimeout)
}

func DialUnixWithDefault(path string, defaultTimeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, TimeoutFromEnv(defaultTimeout))
}

func DialUnixContext(ctx context.Context, path string) (net.Conn, error) {
	return DialUnixContextWithDefault(ctx, path, DefaultDialTimeout)
}

func DialUnixContextWithDefault(ctx context.Context, path string, defaultTimeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: TimeoutFromEnv(defaultTimeout)}
	return dialer.DialContext(ctx, "unix", path)
}

func warnInvalidTimeout(warnf func(string, ...any), value string, defaultTimeout time.Duration, reason string) {
	if warnf == nil {
		return
	}
	warnf("invalid %s=%q (%s); using default %s", EnvDialTimeout, value, reason, defaultTimeout)
}
