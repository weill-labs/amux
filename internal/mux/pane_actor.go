package mux

type paneCommand struct {
	run  func()
	done chan struct{}
	stop bool
}

func (p *Pane) startActor() {
	if p.actorCommands != nil {
		return
	}
	p.actorClosing.Store(false)
	p.actorCommands = make(chan paneCommand)
	p.actorDone = make(chan struct{})
	go p.actorLoop()
}

func (p *Pane) actorLoop() {
	defer close(p.actorDone)
	for cmd := range p.actorCommands {
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
	ch := p.actorCommands
	done := p.actorDone
	if ch == nil {
		return
	}
	if !p.actorClosing.CompareAndSwap(false, true) {
		if done != nil {
			<-done
		}
		p.actorCommands = nil
		return
	}
	stopDone := make(chan struct{})
	ch <- paneCommand{done: stopDone, stop: true}
	<-stopDone
	if done != nil {
		<-done
	}
	p.actorCommands = nil
}

func (p *Pane) withActor(run func()) {
	ch := p.actorCommands
	doneCh := p.actorDone
	if ch == nil {
		run()
		return
	}
	if p.actorClosing.Load() {
		if doneCh != nil {
			<-doneCh
		}
		run()
		return
	}
	done := make(chan struct{})
	defer func() {
		if recover() != nil {
			if doneCh != nil {
				<-doneCh
			}
			run()
		}
	}()
	select {
	case ch <- paneCommand{run: run, done: done}:
		<-done
	case <-doneCh:
		run()
	}
}

func paneActorValue[T any](p *Pane, run func() T) (value T) {
	ch := p.actorCommands
	doneCh := p.actorDone
	if ch == nil {
		return run()
	}
	if p.actorClosing.Load() {
		if doneCh != nil {
			<-doneCh
		}
		return run()
	}
	done := make(chan struct{})
	value = *new(T)
	defer func() {
		if recover() != nil {
			if doneCh != nil {
				<-doneCh
			}
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
	case ch <- cmd:
		<-done
	case <-doneCh:
		value = run()
	}
	return value
}
