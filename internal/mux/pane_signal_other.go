//go:build !darwin

package mux

func (p *Pane) notifyResizeSignal() {}
