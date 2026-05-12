package vtest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	varnishlog "github.com/varnish/varnish-go/log"
	"github.com/varnish/varnish-go/vtest"
)

// afterSettleLog fires fn after a short delay, giving Run time to acquire a
// cursor and sit at the log tail before new records are written.
func afterSettleLog(fn func()) {
	go func() {
		time.Sleep(200 * time.Millisecond)
		fn()
	}()
}

// TestSetLiveFalseStopsAfterBacklog verifies that SetLive(false) causes Run to
// return nil after draining the existing backlog, without requiring ctx cancel.
func TestSetLiveFalseStopsAfterBacklog(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	// Generate a request into the backlog before attaching.
	if _, err := http.Get(v.URL + "/backlog-test"); err != nil {
		t.Fatalf("GET: %v", err)
	}
	// Give Varnish time to write the End record to the VSL buffer;
	// http.Get returns when the response body is received, which is
	// slightly before the End record is written.
	time.Sleep(50 * time.Millisecond)

	r, err := varnishlog.New().
		SetName(v.Name()).
		SetTimeout(5 * time.Second).
		SetLive(false).
		Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	reqURLTag, _ := varnishlog.TagByName("ReqURL")
	var sawRequest bool

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx, func(txns []varnishlog.Transaction) error {
			for _, txn := range txns {
				for _, rec := range txn.Records {
					if rec.Tag == reqURLTag && rec.Data == "/backlog-test" {
						sawRequest = true
					}
				}
			}
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop after backlog within 10s")
	}

	if !sawRequest {
		t.Error("expected to see the backlog request")
	}
}

// TestRecords verifies that Records() returns VSL records accumulated since
// the instance started, including records written before the call.
func TestRecords(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	if _, err := http.Get(v.URL + "/records-test"); err != nil {
		t.Fatalf("GET: %v", err)
	}
	// Give the background collector time to process the request.
	time.Sleep(300 * time.Millisecond)

	recs := v.Records()
	if len(recs) == 0 {
		t.Fatal("expected non-empty Records()")
	}
	reqURLTag, _ := varnishlog.TagByName("ReqURL")
	var found bool
	for _, rec := range recs {
		if rec.Tag == reqURLTag && rec.Data == "/records-test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find ReqURL /records-test in Records()")
	}
}

// TestRecordChannel verifies that RecordChannel streams live records and is
// closed when Stop is called.
func TestRecordChannel(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	ch, err := v.RecordChannel()
	if err != nil {
		t.Fatalf("RecordChannel: %v", err)
	}

	reqURLTag, _ := varnishlog.TagByName("ReqURL")

	afterSettleLog(func() { http.Get(v.URL + "/channel-test") }) //nolint:errcheck

	deadline := time.After(10 * time.Second)
	for {
		select {
		case rec, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before finding expected record")
			}
			if rec.Tag == reqURLTag && rec.Data == "/channel-test" {
				return // success
			}
		case <-deadline:
			t.Fatal("timed out waiting for /channel-test in RecordChannel")
		}
	}
}

// TestRecordChannelClosedOnStop verifies that the channel returned by
// RecordChannel is closed when Stop is called.
func TestRecordChannelClosedOnStop(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := v.RecordChannel()
	if err != nil {
		t.Fatalf("RecordChannel: %v", err)
	}

	v.Stop()

	select {
	case _, ok := <-ch:
		if ok {
			// drain until closed
			for range ch {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed within 5s after Stop")
	}
}

// TestTransactionChannel verifies that TransactionChannel streams live
// transactions and that each transaction has the expected fields.
func TestTransactionChannel(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	ch, err := v.TransactionChannel()
	if err != nil {
		t.Fatalf("TransactionChannel: %v", err)
	}

	reqURLTag, _ := varnishlog.TagByName("ReqURL")

	afterSettleLog(func() { http.Get(v.URL + "/txn-channel-test") }) //nolint:errcheck

	deadline := time.After(10 * time.Second)
	for {
		select {
		case txn, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before finding expected transaction")
			}
			for _, rec := range txn.Records {
				if rec.Tag == reqURLTag && rec.Data == "/txn-channel-test" {
					if txn.VXID == 0 {
						t.Error("expected non-zero VXID")
					}
					return // success
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for /txn-channel-test in TransactionChannel")
		}
	}
}

// TestTransactionChannelClosedOnStop verifies that the channel returned by
// TransactionChannel is closed when Stop is called.
func TestTransactionChannelClosedOnStop(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := v.TransactionChannel()
	if err != nil {
		t.Fatalf("TransactionChannel: %v", err)
	}

	v.Stop()

	select {
	case _, ok := <-ch:
		if ok {
			for range ch {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed within 5s after Stop")
	}
}

// TestNoRecordLogsRecordsEmpty verifies that Records() returns nil when NoLog is set.
func TestNoRecordLogsRecordsEmpty(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().NoRecordLogs().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	if _, err := http.Get(v.URL + "/nolog-test"); err != nil {
		t.Fatalf("GET: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	if recs := v.Records(); len(recs) != 0 {
		t.Errorf("NoLog: expected empty Records(), got %d records", len(recs))
	}
}

// TestNoRecordLogsChannelsWork verifies that RecordChannel and TransactionChannel
// still function when NoLog is set (only the records collector is disabled).
func TestNoRecordLogsChannelsWork(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().NoRecordLogs().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	rch, err := v.RecordChannel()
	if err != nil {
		t.Fatalf("RecordChannel: %v", err)
	}
	tch, err := v.TransactionChannel()
	if err != nil {
		t.Fatalf("TransactionChannel: %v", err)
	}

	reqURLTag, _ := varnishlog.TagByName("ReqURL")
	afterSettleLog(func() { http.Get(v.URL + "/nolog-ch-test") }) //nolint:errcheck

	var sawRecord, sawTxn bool
	deadline := time.After(10 * time.Second)
	for !sawRecord || !sawTxn {
		select {
		case rec, ok := <-rch:
			if ok && rec.Tag == reqURLTag && rec.Data == "/nolog-ch-test" {
				sawRecord = true
			}
		case txn, ok := <-tch:
			if ok {
				for _, rec := range txn.Records {
					if rec.Tag == reqURLTag && rec.Data == "/nolog-ch-test" {
						sawTxn = true
					}
				}
			}
		case <-deadline:
			t.Fatalf("timed out: sawRecord=%v sawTxn=%v", sawRecord, sawTxn)
		}
	}
}

// TestGroupingRequestIncludesBackend verifies that GroupingRequest delivers
// both the client request and its triggered backend fetch in the same group.
// A real HTTP backend is used so that a bereq is actually created.
func TestGroupingRequestIncludesBackend(t *testing.T) {
	t.Parallel()
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer be.Close()

	v, err := vtest.New().
		Backend("be", be.URL).
		VclString(`
			sub vcl_recv { return(pass); }
		`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	r, err := varnishlog.New().
		SetName(v.Name()).
		SetTimeout(5 * time.Second).
		SetGrouping(varnishlog.GroupingRequest).
		Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reqURLTag, _ := varnishlog.TagByName("ReqURL")

	afterSettleLog(func() { http.Get(v.URL + "/bereq-test") }) //nolint:errcheck

	var sawClientReq, sawBeReq bool
	err = r.Run(ctx, func(txns []varnishlog.Transaction) error {
		for _, txn := range txns {
			for _, rec := range txn.Records {
				if rec.Tag == reqURLTag && rec.Data == "/bereq-test" {
					for _, t2 := range txns {
						switch t2.Type {
						case varnishlog.TypeRequest:
							sawClientReq = true
						case varnishlog.TypeBackend:
							sawBeReq = true
						}
					}
					cancel()
					return nil
				}
			}
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
	if !sawClientReq {
		t.Error("expected TypeRequest in the group")
	}
	if !sawBeReq {
		t.Error("expected TypeBackend in the group (bereq)")
	}
}
