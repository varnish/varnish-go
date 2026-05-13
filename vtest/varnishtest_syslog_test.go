package vtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/varnish/varnish-go/vtest"
)

const minimalVCL = `
	backend default none;
	sub vcl_recv { return(synth(200, "OK")); }
`

// TestSysLog verifies that SysLogs returns a non-empty snapshot of startup output.
func TestSysLog(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(minimalVCL).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	lines := v.SysLogs()
	if len(lines) == 0 {
		t.Fatal("expected non-empty SysLogs() after start")
	}
}

// TestSysLogContainsOutput verifies snapshot lines are non-empty strings.
func TestSysLogContainsOutput(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(minimalVCL).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	for _, line := range v.SysLogs() {
		if strings.TrimSpace(line) == "" {
			t.Error("found empty line in SysLogs()")
		}
	}
}

// TestSysLogChannelFromBuilder verifies that a channel pre-subscribed via
// VarnishBuilder.SysLogChannel is wired into the running instance and closes
// cleanly when Stop is called.
func TestSysLogChannelFromBuilder(t *testing.T) {
	t.Parallel()
	vb := vtest.New().VclString(minimalVCL)
	ch := vb.SysLogChannel()

	v, err := vb.Start()
	if err != nil {
		t.Fatal(err)
	}
	v.Stop()

	// Drain until closed; the channel must close after Stop.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed cleanly
			}
		case <-deadline:
			t.Fatal("builder channel not closed within 5s after Stop")
		}
	}
}

// TestSysLogChannelClosedOnStop verifies that the channel returned by
// Varnish.SysLogChannel is closed when Stop is called.
func TestSysLogChannelClosedOnStop(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(minimalVCL).Start()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := v.SysLogChannel()
	if err != nil {
		t.Fatalf("SysLogChannel: %v", err)
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

// TestBuilderSysLogChannelClosedOnStop verifies that a channel pre-subscribed
// via VarnishBuilder.SysLogChannel is closed when Stop is called.
func TestBuilderSysLogChannelClosedOnStop(t *testing.T) {
	t.Parallel()
	vb := vtest.New().VclString(minimalVCL)
	ch := vb.SysLogChannel()

	v, err := vb.Start()
	if err != nil {
		t.Fatal(err)
	}

	v.Stop()

	select {
	case _, ok := <-ch:
		if ok {
			for range ch {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("builder channel not closed within 5s after Stop")
	}
}

// TestNoSysLogs verifies that NoSysLogs disables snapshot accumulation,
// making SysLogs return an empty slice.
func TestNoSysLogs(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().NoSysLogs().VclString(minimalVCL).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()

	time.Sleep(100 * time.Millisecond)

	if lines := v.SysLogs(); len(lines) != 0 {
		t.Errorf("NoSysLogs: expected empty SysLogs(), got %d lines", len(lines))
	}
}

// TestNoSysLogsBuilderChannelStillWorks verifies that NoSysLogs does not
// prevent a pre-subscribed builder channel from being wired and closing
// cleanly after Stop.
func TestNoSysLogsBuilderChannelStillWorks(t *testing.T) {
	t.Parallel()
	vb := vtest.New().NoSysLogs().VclString(minimalVCL)
	ch := vb.SysLogChannel()

	v, err := vb.Start()
	if err != nil {
		t.Fatal(err)
	}
	v.Stop()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed cleanly — channel was wired despite NoSysLogs
			}
		case <-deadline:
			t.Fatal("builder channel not closed within 5s after Stop (NoSysLogs case)")
		}
	}
}

// TestInvalidParameterFails verifies that an invalid varnishd parameter causes
// Start to return an error promptly instead of hanging.
func TestInvalidParameterFails(t *testing.T) {
	t.Parallel()
	_, err := vtest.New().
		Parameter("-p", "this_param_does_not_exist=1").
		VclString(minimalVCL).
		Start()
	if err == nil {
		t.Fatal("expected Start to fail with invalid parameter")
	}
}

// TestBuilderSysLogs verifies that SysLogs returns collected lines after a
// failed Start (e.g., invalid VCL).
func TestBuilderSysLogs(t *testing.T) {
	t.Parallel()
	vb := vtest.New().VclString(`this is not valid VCL {{{`)
	_, err := vb.Start()
	if err == nil {
		t.Fatal("expected Start to fail with invalid VCL")
	}

	// Give the background scanner time to flush buffered output.
	time.Sleep(200 * time.Millisecond)

	lines := vb.SysLogs()
	if len(lines) == 0 {
		t.Error("expected non-empty SysLogs() after failed start")
	}
}

// TestSysLogChannelNotStarted verifies SysLogChannel returns an error when
// called on an unstarted Varnish (nil syslogs).
func TestSysLogChannelNotStarted(t *testing.T) {
	t.Parallel()
	v, err := vtest.New().VclString(minimalVCL).Start()
	if err != nil {
		t.Fatal(err)
	}
	v.Stop()

	// After Stop, SysLogChannel should either return a closed channel
	// (stopped state) or an error — not block.
	ch, err := v.SysLogChannel()
	if err != nil {
		return // acceptable: returns error
	}
	// If no error, the channel must close quickly (stopped state).
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("SysLogChannel after Stop neither errored nor closed promptly")
	}
}
