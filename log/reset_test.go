package log_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varnish/varnish-go/adm"
	varnishlog "github.com/varnish/varnish-go/log"
)

// countReconnects returns a builder setup that counts the errors Run reports
// when it drops its cursor to reconnect.
func countReconnects(n *atomic.Int64) func(*varnishlog.LogReaderBuilder) {
	return func(b *varnishlog.LogReaderBuilder) {
		b.SetErrHandler(func(e varnishlog.LogErr) {
			if e == varnishlog.ErrWorkerRestarted || e == varnishlog.ErrAbandoned {
				n.Add(1)
			}
		})
	}
}

// TestRawReaderKeepsCursorAcrossVCLLoads loads VCLs under traffic with a raw
// backlog reader attached and checks that Run keeps its cursor. Each load adds
// VSM segments for the VCL's backend counters but leaves the log segment alone.
func TestRawReaderKeepsCursorAcrossVCLLoads(t *testing.T) {
	t.Parallel()

	v := startVarnish(t)
	defer v.Stop()

	var reconnects atomic.Int64
	r := newReader(t, &v, countReconnects(&reconnects), func(b *varnishlog.LogReaderBuilder) {
		b.SetGrouping(varnishlog.GroupingRaw).SetBacklog(true)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx, func([]varnishlog.Transaction) error { return nil })
	}()

	// Keep the log busy so the reader is always part way through a batch.
	go func() {
		for ctx.Err() == nil {
			resp, err := http.Get(v.URL + "/traffic")
			if err == nil {
				resp.Body.Close()
			}
		}
	}()

	conn := v.AdmConn()
	for i := range 10 {
		name := fmt.Sprintf("load%d", i)
		vcl := `vcl 4.1;
			backend default { .host = "127.0.0.1"; .port = "9"; }
			sub vcl_recv { return(synth(200)); }`
		if err := conn.VCLInline(ctx, name, vcl, adm.VCLStateAuto); err != nil {
			t.Fatalf("vcl.inline %s: %v", name, err)
		}
		if err := conn.VCLUse(ctx, name); err != nil {
			t.Fatalf("vcl.use %s: %v", name, err)
		}
	}

	// Let the reader catch up past the last load before stopping it.
	time.Sleep(500 * time.Millisecond)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
	if n := reconnects.Load(); n != 0 {
		t.Errorf("reader reconnected %d times while VCLs were loaded, want 0", n)
	}
}

// TestRawReaderReconnectsAfterWorkerRestart restarts the worker under traffic
// and checks that Run reconnects to the new worker's log and reads from it.
func TestRawReaderReconnectsAfterWorkerRestart(t *testing.T) {
	t.Parallel()

	v := startVarnish(t)
	defer v.Stop()

	var reconnects atomic.Int64
	r := newReader(t, &v, countReconnects(&reconnects), func(b *varnishlog.LogReaderBuilder) {
		b.SetGrouping(varnishlog.GroupingRaw).SetBacklog(true)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const url = "/after-restart"
	var found atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx, func(txns []varnishlog.Transaction) error {
			for _, txn := range txns {
				for _, rec := range txn.Records {
					if rec.Tag == varnishlog.TagReqURL && strings.Contains(rec.Data, url) {
						found.Store(true)
						cancel()
					}
				}
			}
			return nil
		})
	}()

	traffic, stopTraffic := context.WithCancel(ctx)
	defer stopTraffic()
	go func() {
		for traffic.Err() == nil {
			resp, err := http.Get(v.URL + "/traffic")
			if err == nil {
				resp.Body.Close()
			}
		}
	}()

	conn := v.AdmConn()
	time.Sleep(200 * time.Millisecond)
	if err := conn.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := conn.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	stopTraffic()

	resp, err := http.Get(v.URL + url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()

	if err := <-done; err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
	if !found.Load() {
		t.Errorf("no ReqURL %s record read after the worker restarted", url)
	}
	if reconnects.Load() == 0 {
		t.Error("reader never reconnected, the test exercised nothing")
	}
}
