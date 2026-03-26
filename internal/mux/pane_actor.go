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
	go p.actorLoop()
}

func (p *Pane) actorLoop() {
	for cmd := range p.actorCommands {
		cmd.run()
		close(cmd.done)
	}
}

func (p *Pane) withActor(run func()) {
	if p.actorCommands == nil {
		run()
		return
	}
	done := make(chan struct{})
	p.actorCommands <- paneCommand{run: run, done: done}
	<-done
}

func paneActorValue[T any](p *Pane, run func() T) T {
	if p.actorCommands == nil {
		return run()
	}
	done := make(chan struct{})
	result := make(chan T, 1)
	p.actorCommands <- paneCommand{
		run: func() {
			result <- run()
		},
		done: done,
	}
	<-done
	return <-result
}
