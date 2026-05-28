package vtest

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
)

// syslogState captures the stdout+stderr stream of a running Varnish process.
type syslogState struct {
	mu        sync.Mutex
	lines     []string
	collect   bool
	subs      []chan string
	stopped   bool
	closeOnce sync.Once
	pw        *io.PipeWriter
	wg        sync.WaitGroup
	exited    chan struct{} // closed when the process exits
}

func newSyslogState(collect bool, pw *io.PipeWriter) *syslogState {
	return &syslogState{
		collect: collect,
		pw:      pw,
		exited:  make(chan struct{}),
	}
}

// transfer adjusts the collection mode and registers pre-subscribed channels.
// Called once on successful Start() to hand off builder-time configuration.
func (ss *syslogState) transfer(collect bool, preSubs []chan string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.collect = collect
	if !collect {
		ss.lines = nil
	}
	ss.subs = append(ss.subs, preSubs...)
}

func (ss *syslogState) closePipe() {
	ss.closeOnce.Do(func() { ss.pw.Close() })
}

func (ss *syslogState) closeAllSubs() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.stopped = true
	for _, ch := range ss.subs {
		close(ch)
	}
	ss.subs = nil
}

// start launches two goroutines: one that scans lines from pr and broadcasts
// them, and one that waits for the process to exit and then closes the pipe.
func (ss *syslogState) start(pr *io.PipeReader, wait func() error) {
	ss.wg.Add(1)
	go func() {
		defer ss.wg.Done()
		defer ss.closeAllSubs()
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			ss.mu.Lock()
			if ss.collect {
				ss.lines = append(ss.lines, line)
			}
			subs := make([]chan string, len(ss.subs))
			copy(subs, ss.subs)
			ss.mu.Unlock()
			for _, ch := range subs {
				select {
				case ch <- line:
				default:
				}
			}
		}
	}()

	ss.wg.Add(1)
	go func() {
		defer ss.wg.Done()
		_ = wait()
		close(ss.exited)
		ss.closePipe()
	}()
}

func (ss *syslogState) subscribe() <-chan string {
	ch := make(chan string, 64)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.stopped {
		close(ch)
		return ch
	}
	ss.subs = append(ss.subs, ch)
	return ch
}

func (ss *syslogState) snapshot() []string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	cp := make([]string, len(ss.lines))
	copy(cp, ss.lines)
	return cp
}

func (ss *syslogState) stop() {
	ss.closePipe()
	ss.wg.Wait()
}

// SysLogs returns a snapshot of all stdout/stderr lines emitted by the Varnish
// process since it was started. Safe to call concurrently or after Stop.
func (v *Varnish) SysLogs() []string {
	if v.syslogs == nil {
		return nil
	}
	return v.syslogs.snapshot()
}

// SysLogChannel returns a channel that receives every stdout/stderr line
// emitted by the Varnish process from the point of subscription onwards.
// The channel is closed when the instance is stopped.
func (v *Varnish) SysLogChannel() (<-chan string, error) {
	if v.syslogs == nil {
		return nil, fmt.Errorf("vtest: varnish not started")
	}
	return v.syslogs.subscribe(), nil
}
