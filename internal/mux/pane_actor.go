package mux

type paneCommand struct {
	run  func()
	done chan struct{}
	stop bool
}

// paneActorChannels bundles the actor's command and done channels so they can
// be swapped together behind a single atomic pointer. Readers load the pointer
// once and use a consistent pair, never racing start/stop.
type paneActorChannels struct {
	commands chan paneCommand
	done     chan struct{}
}

func (p *Pane) startActor() {
	if p.actorChans.Load() != nil {
		return
	}
	p.actorClosing.Store(false)
	c := &paneActorChannels{
		commands: make(chan paneCommand),
		done:     make(chan struct{}),
	}
	p.actorChans.Store(c)
	go p.actorLoop(c)
}

func (p *Pane) actorLoop(c *paneActorChannels) {
	defer close(c.done)
	for cmd := range c.commands {
		if cmd.run != nil {
			cmd.run()
		}
		close(cmd.done)
		if cmd.stop {
			return
		}
	}
}

func (p *Pane) stopActor() {
	c := p.actorChans.Load()
	if c == nil {
		return
	}
	if !p.actorClosing.CompareAndSwap(false, true) {
		<-c.done
		p.actorChans.Store(nil)
		return
	}
	stopDone := make(chan struct{})
	c.commands <- paneCommand{done: stopDone, stop: true}
	<-stopDone
	<-c.done
	p.actorChans.Store(nil)
}

func (p *Pane) withActor(run func()) {
	c := p.actorChans.Load()
	if c == nil {
		run()
		return
	}
	if p.actorClosing.Load() {
		<-c.done
		run()
		return
	}
	done := make(chan struct{})
	defer func() {
		if recover() != nil {
			<-c.done
			run()
		}
	}()
	select {
	case c.commands <- paneCommand{run: run, done: done}:
		<-done
	case <-c.done:
		run()
	}
}

func paneActorValue[T any](p *Pane, run func() T) (value T) {
	c := p.actorChans.Load()
	if c == nil {
		return run()
	}
	if p.actorClosing.Load() {
		<-c.done
		return run()
	}
	done := make(chan struct{})
	value = *new(T)
	defer func() {
		if recover() != nil {
			<-c.done
			value = run()
		}
	}()
	cmd := paneCommand{
		run: func() {
			value = run()
		},
		done: done,
	}
	select {
	case c.commands <- cmd:
		<-done
	case <-c.done:
		value = run()
	}
	return value
}
