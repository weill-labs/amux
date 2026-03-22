//go:build !darwin && !linux

package mux

import "syscall"

func (p *Pane) notifyResizeSignal() {}

func (p *Pane) SignalForegroundProcessGroup(sig syscall.Signal) error {
	return p.SignalProcess(sig)
}

func (p *Pane) SignalProcess(sig syscall.Signal) error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Signal(sig)
	}
	if p.process != nil {
		return p.process.Signal(sig)
	}
	return nil
}
