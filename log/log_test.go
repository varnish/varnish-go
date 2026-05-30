package log_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	varnishlog "github.com/varnish/varnish-go/log"
	"github.com/varnish/varnish-go/vtest"
)

func startVarnish(t *testing.T) vtest.Varnish {
	t.Helper()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv {
			return(synth(200, "OK"));
		}
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newReader(t *testing.T, v *vtest.Varnish, setups ...func(*varnishlog.LogReaderBuilder)) *varnishlog.LogReader {
	t.Helper()
	b := varnishlog.New().
		SetName(v.Name()).
		SetTimeout(5 * time.Second)
	for _, setup := range setups {
		setup(b)
	}
	r, err := b.Attach()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

// afterSettle fires fn after a short delay, giving Run time to acquire a cursor
// and sit at the log tail before new records are written.
func afterSettle(fn func()) {
	go func() {
		time.Sleep(200 * time.Millisecond)
		fn()
	}()
}

func TestTagByName(t *testing.T) {
	t.Parallel()
	tag, err := varnishlog.TagByName("ReqURL")
	if err != nil {
		t.Fatalf("TagByName(ReqURL): %v", err)
	}
	if tag.String() != "ReqURL" {
		t.Errorf("expected tag name ReqURL, got %q", tag.String())
	}

	_, err = varnishlog.TagByName("DoesNotExist")
	if err == nil {
		t.Error("expected error for unknown tag name")
	}
}

func TestReceivesClientRequest(t *testing.T) {
	t.Parallel()

	v := startVarnish(t)
	defer v.Stop()

	r := newReader(t, &v)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	afterSettle(func() { http.Get(v.URL + "/hello") }) //nolint:errcheck

	var found bool
	err := r.Run(ctx, func(txns []varnishlog.Transaction) error {
		for _, txn := range txns {
			if txn.Type != varnishlog.TypeRequest {
				continue
			}
			for _, rec := range txn.Records {
				if rec.Tag == varnishlog.TagReqURL && strings.Contains(rec.Data, "/hello") {
					found = true
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
	if !found {
		t.Error("expected to find a ReqURL /hello record in the VSL")
	}
}

func TestTransactionFields(t *testing.T) {
	t.Parallel()

	v := startVarnish(t)
	defer v.Stop()

	r := newReader(t, &v)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	afterSettle(func() { http.Get(v.URL + "/fields-test") }) //nolint:errcheck

	var txn varnishlog.Transaction
	err := r.Run(ctx, func(txns []varnishlog.Transaction) error {
		for _, tx := range txns {
			if tx.Type == varnishlog.TypeRequest {
				txn = tx
				cancel()
				return nil
			}
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}

	if txn.VXID == 0 {
		t.Error("expected non-zero VXID")
	}
	if txn.Type != varnishlog.TypeRequest {
		t.Errorf("expected TypeRequest, got %v", txn.Type)
	}
	if len(txn.Records) == 0 {
		t.Error("expected records in transaction")
	}
	for _, rec := range txn.Records {
		if rec.Tag.String() == "" || strings.HasPrefix(rec.Tag.String(), "tag#") {
			t.Errorf("record has unrecognised tag: %v", rec.Tag)
		}
		if rec.VXID != uint64(txn.VXID) {
			t.Errorf("record VXID %d doesn't match transaction VXID %d", rec.VXID, txn.VXID)
		}
	}
}

func TestQueryFilter(t *testing.T) {
	t.Parallel()

	v := startVarnish(t)
	defer v.Stop()

	r := newReader(t, &v, func(b *varnishlog.LogReaderBuilder) {
		b.SetQuery(`ReqURL eq "/keep"`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	afterSettle(func() {
		http.Get(v.URL + "/keep") //nolint:errcheck
		http.Get(v.URL + "/drop") //nolint:errcheck
	})

	var sawKeep, sawDrop bool
	err := r.Run(ctx, func(txns []varnishlog.Transaction) error {
		for _, txn := range txns {
			for _, rec := range txn.Records {
				if rec.Tag != varnishlog.TagReqURL {
					continue
				}
				if strings.Contains(rec.Data, "/keep") {
					sawKeep = true
					// wait a bit to make sure /drop doesn't also arrive
					time.AfterFunc(500*time.Millisecond, cancel)
				}
				if strings.Contains(rec.Data, "/drop") {
					sawDrop = true
				}
			}
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
	if !sawKeep {
		t.Error("expected to see /keep through the query filter")
	}
	if sawDrop {
		t.Error("expected /drop to be filtered out by the query")
	}
}

func ExampleLogReader_Run() {
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		panic(err)
	}
	defer v.Stop()

	r, err := varnishlog.New().
		SetName(v.Name()).
		SetTimeout(5 * time.Second).
		Attach()
	if err != nil {
		panic(err)
	}
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	afterSettle(func() { http.Get(v.URL + "/example") }) //nolint:errcheck

	r.Run(ctx, func(txns []varnishlog.Transaction) error { //nolint:errcheck
		for _, txn := range txns {
			for _, rec := range txn.Records {
				if rec.Tag == varnishlog.TagReqURL && strings.Contains(rec.Data, "/example") {
					fmt.Println(rec.Data)
					cancel()
				}
			}
		}
		return nil
	})
	// Output: /example
}
