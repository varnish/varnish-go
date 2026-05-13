package vtest

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	vsl "github.com/varnish/varnish-go/log"
)

// logState is heap-allocated so that copies of Varnish share the same state.
type logState struct {
	mu      sync.Mutex
	records []vsl.Record
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// newLogState creates a logState with a live context. Always called by Start
// so that RecordChannel and TransactionChannel have a context and waitgroup.
func newLogState() *logState {
	ls := &logState{}
	ls.ctx, ls.cancel = context.WithCancel(context.Background())
	return ls
}

// startCollector attaches a GroupingRaw reader with backlog enabled and starts
// the background goroutine that accumulates records for Records(). Skipped when
// NoRecordLogs is set on the builder.
func (ls *logState) startCollector(name string) {
	r, err := vsl.New().
		SetName(name).
		SetTimeout(5 * time.Second).
		SetGrouping(vsl.GroupingRaw).
		SetBacklog(true).
		Attach()
	if err != nil {
		log.Printf("vtest: records collector failed to attach: %v", err)
		return
	}

	ls.wg.Add(1)
	go func() {
		defer ls.wg.Done()
		defer r.Close()
		r.Run(ls.ctx, func(txns []vsl.Transaction) error { //nolint:errcheck
			ls.mu.Lock()
			for _, txn := range txns {
				ls.records = append(ls.records, txn.Records...)
			}
			ls.mu.Unlock()
			return nil
		})
	}()
}

// stopLogs cancels the shared log context and waits for all goroutines
// (records collector and any active channels) to exit.
func (ls *logState) stop() {
	ls.cancel()
	ls.wg.Wait()
}

// Records returns a snapshot of all VSL records emitted by the instance since
// it was started. Safe to call concurrently or after Stop.
func (v *Varnish) Records() []vsl.Record {
	if v.logs == nil {
		return nil
	}
	v.logs.mu.Lock()
	defer v.logs.mu.Unlock()
	cp := make([]vsl.Record, len(v.logs.records))
	copy(cp, v.logs.records)
	return cp
}

// RecordChannel returns a channel that receives every VSL record streamed live
// from the instance. The channel is closed when the instance is stopped.
// Attach is performed synchronously; an error is returned if it fails.
func (v *Varnish) RecordChannel() (<-chan vsl.Record, error) {
	if v.logs == nil {
		return nil, fmt.Errorf("vtest: varnish not started")
	}

	r, err := vsl.New().
		SetName(v.name).
		SetTimeout(5 * time.Second).
		SetGrouping(vsl.GroupingRaw).
		Attach()
	if err != nil {
		return nil, fmt.Errorf("vtest: RecordChannel attach: %w", err)
	}

	ch := make(chan vsl.Record, 64)
	ls := v.logs
	ls.wg.Add(1)
	go func() {
		defer ls.wg.Done()
		defer r.Close()
		defer close(ch)
		r.Run(ls.ctx, func(txns []vsl.Transaction) error { //nolint:errcheck
			for _, txn := range txns {
				for _, rec := range txn.Records {
					select {
					case ch <- rec:
					case <-ls.ctx.Done():
						return ls.ctx.Err()
					}
				}
			}
			return nil
		})
	}()

	return ch, nil
}

// TransactionChannel returns a channel that receives every VSL transaction
// streamed live from the instance (GroupingVXID). The channel is closed when
// the instance is stopped.
// Attach is performed synchronously; an error is returned if it fails.
func (v *Varnish) TransactionChannel() (<-chan vsl.Transaction, error) {
	if v.logs == nil {
		return nil, fmt.Errorf("vtest: varnish not started")
	}

	r, err := vsl.New().
		SetName(v.name).
		SetTimeout(5 * time.Second).
		SetGrouping(vsl.GroupingVXID).
		Attach()
	if err != nil {
		return nil, fmt.Errorf("vtest: TransactionChannel attach: %w", err)
	}

	ch := make(chan vsl.Transaction, 16)
	ls := v.logs
	ls.wg.Add(1)
	go func() {
		defer ls.wg.Done()
		defer r.Close()
		defer close(ch)
		r.Run(ls.ctx, func(txns []vsl.Transaction) error { //nolint:errcheck
			for _, txn := range txns {
				select {
				case ch <- txn:
				case <-ls.ctx.Done():
					return ls.ctx.Err()
				}
			}
			return nil
		})
	}()

	return ch, nil
}
