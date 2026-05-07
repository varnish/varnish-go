package stat_test

import (
	"fmt"
	"log"
	"net/http"
	"testing"
	"time"

	"github.com/varnish/varnish-go/stat"
	"github.com/varnish/varnish-go/vtest"
)

func ExampleStatReader_Update() {
	r, err := stat.New().
		SetName("/var/lib/varnish/myinstance").
		SetTimeout(5 * time.Second).
		Attach()
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	for {
		time.Sleep(time.Second)
		r.Update()
		if c, ok := r.Stats["MAIN.client_req"]; ok {
			fmt.Println(*c.Value)
		}
	}
}

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

func newStatReader(t *testing.T, v *vtest.Varnish) *stat.StatReader {
	t.Helper()
	r, err := stat.New().
		SetName(v.Name()).
		SetTimeout(5 * time.Second).
		Attach()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func mustUpdate(t *testing.T, r *stat.StatReader) (added, removed []string) {
	t.Helper()
	added, removed, err := r.Update()
	if err != nil {
		t.Fatal(err)
	}
	return added, removed
}

func TestUpdateAddsCountersOnFirstCall(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)
	added, removed := mustUpdate(t, c)

	if len(added) == 0 {
		t.Error("expected counters to be added on first Update")
	}
	if len(removed) != 0 {
		t.Errorf("expected no counters removed on first Update, got %d", len(removed))
	}
}

func TestUpdateStabilizes(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)

	// Varnish's child process registers its VSM segments shortly after the
	// manager's, so the first few Updates may still report new counters.
	// Drain until the set is stable.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		added, _ := mustUpdate(t, c)
		if len(added) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Once stable, a further Update must report no changes.
	added, removed := mustUpdate(t, c)
	if len(added) != 0 {
		t.Errorf("expected no new counters after stabilization, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected no removed counters after stabilization, got %d", len(removed))
	}
}

func TestCounters(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)
	mustUpdate(t, c)

	if len(c.Stats) == 0 {
		t.Fatal("expected counters, got none")
	}
	counter, ok := c.Stats["MAIN.cache_hit"]
	if !ok {
		t.Fatal("expected MAIN.cache_hit in Stats")
	}
	if counter.Semantics != stat.SemanticsCounter {
		t.Errorf("expected SemanticsCounter, got %v", counter.Semantics)
	}
	if counter.Flags != stat.FlagsInteger {
		t.Errorf("expected FlagsInteger, got %v", counter.Flags)
	}
	if _, ok := c.Stats["MAIN.does_not_exist"]; ok {
		t.Error("expected no counter for unknown name")
	}
}

func TestCounterValue(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)

	for range 3 {
		if _, err := http.Get(v.URL + "/test"); err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(100 * time.Millisecond)
	mustUpdate(t, c)

	val, err := c.Counter("MAIN.client_req")
	if err != nil {
		t.Fatal(err)
	}
	if val != 3 {
		t.Errorf("expected MAIN.client_req == 3 after 3 requests, got %d", val)
	}
}
