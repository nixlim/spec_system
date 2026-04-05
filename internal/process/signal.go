package process

import "syscall"

// SignalSender abstracts OS signal delivery so it can be stubbed in tests.
type SignalSender interface {
	Send(pid int, sig syscall.Signal) error
}

// RealSignalSender sends OS signals via syscall.Kill.
type RealSignalSender struct{}

// Send delivers the given signal to the process identified by pid.
func (RealSignalSender) Send(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}
