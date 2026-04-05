package server

import (
	"os"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
)

const (
	defaultCheckpointDebounce = 500 * time.Millisecond
	defaultCheckpointPeriodic = 30 * time.Second
)

type crashCheckpointCoordinator interface {
	Trigger()
	Stop()
	Write()
	WriteNow() (string, error)
}

type CheckpointCoordinatorConfig struct {
	Clock                   Clock
	Debounce                time.Duration
	Periodic                time.Duration
	SessionName             func() string
	SessionStart            func() time.Time
	BuildCrashCheckpoint    func() *checkpoint.CrashCheckpoint
	IsShuttingDown          func() bool
	OnCheckpointWritten     func(string)
	LogCheckpointWrite      func(string, time.Duration, error)
	LogDirectoryUnavailable func(error)
	WriteCrash              func(*checkpoint.CrashCheckpoint, string, time.Time) error
	MkdirAll                func(string, os.FileMode) error
}

// CheckpointCoordinator owns crash checkpoint scheduling and file writes for a session.
type CheckpointCoordinator struct {
	clock                   Clock
	debounce                time.Duration
	periodic                time.Duration
	sessionName             func() string
	sessionStart            func() time.Time
	buildCrashCheckpoint    func() *checkpoint.CrashCheckpoint
	isShuttingDown          func() bool
	onCheckpointWritten     func(string)
	logCheckpointWrite      func(string, time.Duration, error)
	logDirectoryUnavailable func(error)
	writeCrash              func(*checkpoint.CrashCheckpoint, string, time.Time) error
	mkdirAll                func(string, os.FileMode) error
	trigger                 chan struct{}
	stop                    chan struct{}
	done                    chan struct{}
}

func NewCheckpointCoordinator(cfg CheckpointCoordinatorConfig) *CheckpointCoordinator {
	coord := &CheckpointCoordinator{
		clock:                   cfg.Clock,
		debounce:                cfg.Debounce,
		periodic:                cfg.Periodic,
		sessionName:             cfg.SessionName,
		sessionStart:            cfg.SessionStart,
		buildCrashCheckpoint:    cfg.BuildCrashCheckpoint,
		isShuttingDown:          cfg.IsShuttingDown,
		onCheckpointWritten:     cfg.OnCheckpointWritten,
		logCheckpointWrite:      cfg.LogCheckpointWrite,
		logDirectoryUnavailable: cfg.LogDirectoryUnavailable,
		writeCrash:              cfg.WriteCrash,
		mkdirAll:                cfg.MkdirAll,
		trigger:                 make(chan struct{}, 1),
		stop:                    make(chan struct{}),
		done:                    make(chan struct{}),
	}
	if coord.clock == nil {
		coord.clock = RealClock{}
	}
	if coord.debounce == 0 {
		coord.debounce = defaultCheckpointDebounce
	}
	if coord.periodic == 0 {
		coord.periodic = defaultCheckpointPeriodic
	}
	if coord.sessionName == nil {
		coord.sessionName = func() string { return "" }
	}
	if coord.sessionStart == nil {
		coord.sessionStart = func() time.Time { return time.Time{} }
	}
	if coord.buildCrashCheckpoint == nil {
		coord.buildCrashCheckpoint = func() *checkpoint.CrashCheckpoint { return nil }
	}
	if coord.isShuttingDown == nil {
		coord.isShuttingDown = func() bool { return false }
	}
	if coord.onCheckpointWritten == nil {
		coord.onCheckpointWritten = func(string) {}
	}
	if coord.logCheckpointWrite == nil {
		coord.logCheckpointWrite = func(string, time.Duration, error) {}
	}
	if coord.logDirectoryUnavailable == nil {
		coord.logDirectoryUnavailable = func(error) {}
	}
	if coord.writeCrash == nil {
		coord.writeCrash = checkpoint.WriteCrash
	}
	if coord.mkdirAll == nil {
		coord.mkdirAll = os.MkdirAll
	}

	coord.ensureCheckpointDir()
	go coord.loop()
	return coord
}

func (c *CheckpointCoordinator) ensureCheckpointDir() {
	if err := c.mkdirAll(checkpoint.CrashCheckpointDir(), 0700); err != nil {
		c.logDirectoryUnavailable(err)
	}
}

func (c *CheckpointCoordinator) Trigger() {
	if c == nil {
		return
	}
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

func (c *CheckpointCoordinator) Stop() {
	if c == nil || c.stop == nil || c.done == nil {
		return
	}
	select {
	case <-c.done:
		return
	default:
	}
	close(c.stop)
	<-c.done
}

func (c *CheckpointCoordinator) loop() {
	defer close(c.done)

	var debounceTimer Timer
	var debounceC <-chan time.Time
	periodicTimer := c.clock.NewTimer(c.periodic)
	defer stopTimer(periodicTimer)

	for {
		select {
		case <-c.stop:
			if debounceTimer != nil {
				stopTimer(debounceTimer)
			}
			return
		case <-c.trigger:
			drainCheckpointTriggers(c.trigger)
			if debounceTimer == nil {
				debounceTimer = c.clock.NewTimer(c.debounce)
			} else {
				resetTimer(debounceTimer, c.debounce)
			}
			debounceC = debounceTimer.C()
		case <-debounceC:
			c.Write()
			debounceC = nil
		case <-periodicTimer.C():
			c.Write()
			periodicTimer.Reset(c.periodic)
		}
	}
}

func drainCheckpointTriggers(trigger <-chan struct{}) {
	for {
		select {
		case <-trigger:
		default:
			return
		}
	}
}

func (c *CheckpointCoordinator) Write() {
	if c == nil || c.isShuttingDown() {
		return
	}
	_, _ = c.WriteNow()
}

func (c *CheckpointCoordinator) WriteNow() (string, error) {
	if c == nil {
		return "", nil
	}

	started := c.clock.Now()
	cp := c.buildCrashCheckpoint()
	if cp == nil {
		return "", nil
	}

	sessionName := c.sessionName()
	startTime := c.sessionStart()
	path := checkpoint.CrashCheckpointPathTimestamped(sessionName, startTime)
	if err := c.writeCrash(cp, sessionName, startTime); err != nil {
		c.logCheckpointWrite(path, c.clock.Now().Sub(started), err)
		return "", err
	}
	c.onCheckpointWritten(path)
	c.logCheckpointWrite(path, c.clock.Now().Sub(started), nil)
	return path, nil
}

func newSessionCheckpointCoordinator(sess *Session) crashCheckpointCoordinator {
	if sess == nil {
		return nil
	}
	return NewCheckpointCoordinator(CheckpointCoordinatorConfig{
		Clock:                sess.clock(),
		SessionName:          func() string { return sess.Name },
		SessionStart:         func() time.Time { return sess.startedAt },
		BuildCrashCheckpoint: sess.buildCrashCheckpoint,
		IsShuttingDown:       func() bool { return sess.shutdown.Load() },
		OnCheckpointWritten: func(path string) {
			sess.enqueueEvent(crashCheckpointWrittenEvent{path: path})
		},
		LogCheckpointWrite: func(path string, duration time.Duration, err error) {
			sess.logCheckpointWrite("crash", path, duration, err)
		},
		LogDirectoryUnavailable: func(err error) {
			sess.logger.Warn("crash checkpoint dir unavailable",
				"event", "checkpoint_write",
				"checkpoint_kind", "crash",
				"error", err,
			)
		},
	})
}

func (s *Session) triggerCrashCheckpoint() {
	if s == nil || s.checkpointCoordinator == nil {
		return
	}
	s.checkpointCoordinator.Trigger()
}

func (s *Session) stopCrashCheckpointLoop() {
	if s == nil || s.checkpointCoordinator == nil {
		return
	}
	s.checkpointCoordinator.Stop()
}

func (s *Session) writeCrashCheckpointNow() (string, error) {
	if s == nil || s.checkpointCoordinator == nil {
		return "", nil
	}
	return s.checkpointCoordinator.WriteNow()
}

func (s *Session) writeCrashCheckpoint() {
	if s == nil || s.checkpointCoordinator == nil {
		return
	}
	s.checkpointCoordinator.Write()
}
