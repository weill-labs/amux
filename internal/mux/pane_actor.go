package mux

type paneCommand struct {
	run  func()
	done chan struct{}
}

func (p *Pane) startActor() {
	if p.actorCommands != nil {
		return
	}
	p.actorCommands = make(chan paneCommand)
	p.actorDone = make(chan struct{})
	go p.actorLoop()
}

func (p *Pane) actorLoop() {
	defer close(p.actorDone)
	for cmd := range p.actorCommands {
		cmd.run()
		close(cmd.done)
	}
}

func (p *Pane) stopActor() {
	if p.actorCommands == nil {
		return
	}
	close(p.actorCommands)
	if p.actorDone != nil {
		<-p.actorDone
	}
}

func (p *Pane) withActor(run func()) {
	ch := p.actorCommands
	if ch == nil {
		run()
		return
	}
	done := make(chan struct{})
	defer func() {
		if recover() != nil {
			if p.actorDone != nil {
				<-p.actorDone
			}
			run()
		}
	}()
	ch <- paneCommand{run: run, done: done}
	<-done
}

func paneActorValue[T any](p *Pane, run func() T) (value T) {
	ch := p.actorCommands
	if ch == nil {
		return run()
	}
	done := make(chan struct{})
	value = *new(T)
	defer func() {
		if recover() != nil {
			if p.actorDone != nil {
				<-p.actorDone
			}
			value = run()
		}
	}()
	ch <- paneCommand{
		run: func() {
			value = run()
		},
		done: done,
	}
	<-done
	return value
}
