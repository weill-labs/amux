package remote

import (
	"math"
	"time"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
	backoffFactor  = 2.0
)

// startReconnectLoop begins an exponential backoff reconnection loop.
// It runs in a goroutine and attempts to reconnect until successful or
// the host is explicitly disconnected.
func (hc *HostConn) startReconnectLoop() {
	hc.mu.Lock()
	if hc.state != Disconnected && hc.state != Reconnecting {
		hc.mu.Unlock()
		return
	}
	hc.setState(Reconnecting)
	sessionName := hc.sessionName
	remoteUID := hc.remoteUID
	isTakeover := hc.takeoverMode
	sshAddr := normalizeAddr(hc.config.Address)
	hc.mu.Unlock()

	delay := initialBackoff
	for attempt := 0; ; attempt++ {
		time.Sleep(delay)

		hc.mu.Lock()
		// Stop if we were explicitly disconnected or already reconnected
		if hc.state == Connected || hc.state == Disconnected {
			hc.mu.Unlock()
			return
		}
		hc.mu.Unlock()

		var err error
		if isTakeover {
			err = hc.connectTakeover(sessionName, remoteUID, sshAddr)
		} else {
			err = hc.connect(sessionName)
		}
		if err == nil {
			hc.mu.Lock()
			hc.setState(Connected)
			hc.mu.Unlock()

			// Restart the read loop for pane output
			go hc.readLoop()
			return
		}

		// Exponential backoff
		delay = time.Duration(float64(initialBackoff) * math.Pow(backoffFactor, float64(attempt+1)))
		if delay > maxBackoff {
			delay = maxBackoff
		}
	}
}
