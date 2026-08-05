package varnish

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// syslogState drains the combined stdout/stderr stream of the varnishd
// child. It exists to detect process exit and to enrich [VarnishBuilder.Build]
// failures with diagnostics; it intentionally exposes no accumulation or
// subscription API of its own — that convenience belongs in vtest, built on
// top of [VarnishBuilder.Output].
type syslogState struct {
	mu        sync.Mutex
	lines     []string    // retained only until Build finishes, for diagnostics
	done      atomic.Bool // Build finished successfully: stop retaining lines
	closeOnce sync.Once
	pw        *io.PipeWriter
	wg        sync.WaitGroup
	exited    chan struct{} // closed when the process exits
}

func newSyslogState(pw *io.PipeWriter) *syslogState {
	return &syslogState{
		pw:     pw,
		exited: make(chan struct{}),
	}
}

// finalize stops retaining diagnostic lines once Build has succeeded, so a
// long-running instance doesn't accumulate its output in memory forever.
func (ss *syslogState) finalize() {
	ss.mu.Lock()
	ss.lines = nil
	ss.mu.Unlock()
	ss.done.Store(true)
}

func (ss *syslogState) closePipe() {
	ss.closeOnce.Do(func() { ss.pw.Close() })
}

// start launches two goroutines: one that scans lines from pr, retaining
// them for diagnostics until finalize is called, and one that waits for the
// process to exit and then closes the pipe.
func (ss *syslogState) start(pr *io.PipeReader, wait func() error) {
	ss.wg.Add(1)
	go func() {
		defer ss.wg.Done()
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			if !ss.done.Load() {
				ss.mu.Lock()
				if !ss.done.Load() {
					ss.lines = append(ss.lines, line)
				}
				ss.mu.Unlock()
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
