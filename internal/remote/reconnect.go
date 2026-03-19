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

// reconnectLoop runs in a goroutine spawned by readDisconnectEvent.
// It attempts to reconnect with exponential backoff, posting the result
// to the actor on success. Exits when the actor state changes away from
// Reconnecting (e.g., explicit Disconnect).
func (hc *HostConn) reconnectLoop(sessionName, remoteUID string, isTakeover bool, sshAddr string) {
	delay := initialBackoff
	for attempt := 0; ; attempt++ {
		time.Sleep(delay)

		// Check if still reconnecting (explicit disconnect sets Disconnected).
		if hc.State() != Reconnecting {
			return
		}

		var outcome *connectOutcome
		var err error
		if isTakeover {
			outcome, err = hc.doConnectTakeover(sessionName, remoteUID, sshAddr)
		} else {
			outcome, err = hc.doConnect(sessionName)
		}

		if err == nil {
			done := make(chan struct{})
			if hc.enqueue(reconnectDoneEvent{outcome: outcome, done: done}) {
				<-done
			}
			return
		}

		// Exponential backoff
		delay = time.Duration(float64(initialBackoff) * math.Pow(backoffFactor, float64(attempt+1)))
		if delay > maxBackoff {
			delay = maxBackoff
		}
	}
}
