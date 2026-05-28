// Read VSL logs (like varnishlog, varnishncsa, etc.)
package log

// The main entry point is [New], which returns a [LogReaderBuilder]. Configure it
// with optional name, timeout, query, and grouping, then call [LogReaderBuilder.Attach]
// to get a [LogReader]. Call [LogReader.Run] to start streaming transactions.
//
// # Usage
//
//	r, err := log.New().
//	    SetName("/tmp/my-varnish").
//	    SetTimeout(5 * time.Second).
//	    SetQuery(`ReqURL eq "/health"`).
//	    Attach()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer r.Close()
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	err = r.Run(ctx, func(txns []log.Transaction) error {
//	    for _, txn := range txns {
//	        for _, rec := range txn.Records {
//	            fmt.Printf("%s %s\n", rec.Tag, rec.Data)
//	        }
//	    }
//	    return nil
//	})

// #cgo pkg-config: varnishapi
// #include <stdint.h>
// #include <stdio.h>
// #include <stdlib.h>
// #include <vapi/vsm.h>
// #include <vapi/vsl.h>
//
// extern int dispatchCallback(struct VSL_data*, struct VSL_transaction* const*, void*);
//
// static int callVSLQDispatch(struct VSLQ *vslq, uintptr_t priv) {
//     return VSLQ_Dispatch(vslq, dispatchCallback, (void*)priv);
// }
//
// static int callVSLQFlush(struct VSLQ *vslq, uintptr_t priv) {
//     return VSLQ_Flush(vslq, dispatchCallback, (void*)priv);
// }
//
// static const char* vslTagName(int tag) {
//     if (tag <= 0 || tag >= SLT__MAX) return NULL;
//     return VSL_tags[tag];
// }
//
import "C"

import (
	"context"
	"fmt"
	"runtime/cgo"
	"strconv"
	"time"
	"unsafe"
)

// VSL dispatch status codes, matching the C enum vsl_status.
const (
	vslMore     = 1
	vslEnd      = 0
	vslEEOF     = -1
	vslEAbandon = -2
	vslEOverrun = -3
	vslEIO      = -4
)

// LogErr describes a recoverable VSL read condition — equivalent to the
// diagnostic messages that varnishlog prints to stderr during normal operation.
// These are non-fatal: [LogReader.Run] always reconnects and keeps going, but
// you can specify a callback with [LogReaderBuilder.SetErrHandler] to be notified when they occur.
type LogErr int

const (
	// Varnish wrote log records faster than they were consumed
	// and some were lost (vsl_e_overrun).
	ErrOverrun LogErr = iota

	// The VSL segment was abandoned by the writer, usually because Varnish
	// stopped (possibly killed)
	// (vsl_e_abandon).
	ErrAbandoned

	// Varnish worker process restarted; the
	// current cursor is no longer valid (VSM_WRK_RESTARTED).
	ErrWorkerRestarted

	// VSL_CursorVSM failed to map the log segment,
	// typically because the worker is still starting up.
	ErrCursorLost

	// ErrIO means an I/O read error occurred on the VSL segment (vsl_e_io).
	ErrIO
)

// String returns a human-readable description of the error. Implements [fmt.Stringer].
func (e LogErr) String() string {
	switch e {
	case ErrOverrun:
		return "log overrun"
	case ErrAbandoned:
		return "log abandoned"
	case ErrWorkerRestarted:
		return "worker restarted"
	case ErrCursorLost:
		return "failed to acquire log"
	case ErrIO:
		return "I/O read error"
	default:
		return fmt.Sprintf("LogErr(%d)", int(e))
	}
}

// MarshalText encodes the error as its string name. Implements [encoding.TextMarshaler];
// see https://pkg.go.dev/encoding#TextMarshaler.
func (e LogErr) MarshalText() ([]byte, error) { return []byte(e.String()), nil }

// How VSL records are grouped into transactions.
type Grouping int

const (
	// Each record is delivered as its own transaction, with no parent-child relationships.
	// Use this to notably read Backend_health records and logs occuring outside of sessions and requests.
	GroupingRaw Grouping = Grouping(C.VSL_g_raw)
	// Records are grouped into a single transaction
	GroupingVXID Grouping = Grouping(C.VSL_g_vxid)
	// HTTP transactions are grouped using the Link records, a group will contain the backend requests,
	// restarts and ESI transactions it directly or indirectly triggered.
	GroupingRequest Grouping = Grouping(C.VSL_g_request)
	// Same as [GroupingRequest] but the entry point is the connection itself, meaning the group will
	// contain all the transactions triggered by the connection.
	GroupingSession Grouping = Grouping(C.VSL_g_session)
)

// TransactionType describes what kind of processing a transaction represents.
type TransactionType int

const (
	// Unknown type, should not occur in practice.
	TypeUnknown TransactionType = TransactionType(C.VSL_t_unknown)
	// Session, represents a client connection.
	TypeSession TransactionType = TransactionType(C.VSL_t_sess)
	// Client request
	TypeRequest TransactionType = TransactionType(C.VSL_t_req)
	// Backend request
	TypeBackend TransactionType = TransactionType(C.VSL_t_bereq)
	// Raw log entry
	TypeRaw TransactionType = TransactionType(C.VSL_t_raw)
)

// String returns the name of the transaction type. Implements [fmt.Stringer].
func (t TransactionType) String() string {
	switch t {
	case TypeSession:
		return "session"
	case TypeRequest:
		return "request"
	case TypeBackend:
		return "backend"
	case TypeRaw:
		return "raw"
	default:
		return "unknown"
	}
}

// MarshalText encodes the type as its string name. Implements [encoding.TextMarshaler];
// see https://pkg.go.dev/encoding#TextMarshaler.
func (t TransactionType) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// Reason describes why a transaction was initiated.
type Reason int

const (
	// Unknown reason, should not occur in practice.
	ReasonUnknown Reason = Reason(C.VSL_r_unknown)
	// HTTP/1.x request
	ReasonHTTP1 Reason = Reason(C.VSL_r_http_1)
	// Received request
	ReasonRxReq Reason = Reason(C.VSL_r_rxreq)
	// ESI processing
	ReasonESI Reason = Reason(C.VSL_r_esi)
	// Restarted request
	ReasonRestart Reason = Reason(C.VSL_r_restart)
	// Backend request started because of a pass
	ReasonPass Reason = Reason(C.VSL_r_pass)
	// Backend request started to fetch a cache miss
	ReasonFetch Reason = Reason(C.VSL_r_fetch)
	// Backend request started to refresh a graced object
	ReasonBgFetch Reason = Reason(C.VSL_r_bgfetch)
	// Piped request
	ReasonPipe Reason = Reason(C.VSL_r_pipe)
)

// String returns the name of the reason. Implements [fmt.Stringer].
func (r Reason) String() string {
	switch r {
	case ReasonHTTP1:
		return "http1"
	case ReasonRxReq:
		return "rxreq"
	case ReasonESI:
		return "esi"
	case ReasonRestart:
		return "restart"
	case ReasonPass:
		return "pass"
	case ReasonFetch:
		return "fetch"
	case ReasonBgFetch:
		return "bgfetch"
	case ReasonPipe:
		return "pipe"
	default:
		return "unknown"
	}
}

// MarshalText encodes the reason as its string name. Implements [encoding.TextMarshaler];
// see https://pkg.go.dev/encoding#TextMarshaler.
func (r Reason) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// Tag is a VSL log tag (e.g. [TagReqURL], [TagRespStatus]).
// Use [TagByName] to look up a tag by its string name.
type Tag int

// String returns the tag's name as known to Varnish (e.g. "ReqURL", "RespStatus").
// Implements [fmt.Stringer].
func (t Tag) String() string {
	s := C.vslTagName(C.int(t))
	if s == nil {
		return fmt.Sprintf("tag#%d", int(t))
	}
	return C.GoString(s)
}

// MarshalText encodes the tag as its Varnish name. Implements [encoding.TextMarshaler];
// see https://pkg.go.dev/encoding#TextMarshaler.
func (t Tag) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// TagByName looks up a Tag by name (case-insensitive; prefix match is accepted
// when unambiguous). Returns an error if the name matches zero or multiple tags.
func TagByName(name string) (Tag, error) {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	n := C.VSL_Name2Tag(cs, -1)
	switch {
	case n >= 0:
		return Tag(n), nil
	case n == -2:
		return 0, fmt.Errorf("ambiguous VSL tag name %q", name)
	default:
		return 0, fmt.Errorf("unknown VSL tag %q", name)
	}
}

// Record is a single VSL log entry within a transaction.
type Record struct {
	Tag       Tag    `json:"tag"       yaml:"tag"`
	VXID      uint64 `json:"vxid"      yaml:"vxid"`
	IsClient  bool   `json:"isClient"  yaml:"isClient"`
	IsBackend bool   `json:"isBackend" yaml:"isBackend"`
	Data      string `json:"data"      yaml:"data"`
}

// Transaction is a group of related VSL records sharing a VXID.
type Transaction struct {
	Level      int             `json:"level"      yaml:"level"`
	VXID       int64           `json:"vxid"       yaml:"vxid"`
	ParentVXID int64           `json:"parentVxid" yaml:"parentVxid"`
	Type       TransactionType `json:"type"       yaml:"type"`
	Reason     Reason          `json:"reason"     yaml:"reason"`
	Records    []Record        `json:"records"    yaml:"records"`
}

// LogReaderBuilder configures a connection to the Varnish VSL.
// Obtain one with [New], configure with the Set* methods, then call [LogReaderBuilder.Attach].
type LogReaderBuilder struct {
	vsm        *C.struct_vsm
	vsl        *C.struct_VSL_data
	grouping   Grouping
	query      string
	errHandler func(LogErr)
	backlog    bool   // start cursor at log head instead of tail
	live       *bool  // nil=stop at end, true=follow, false=stop
	file       string // read from binary VSL file instead of live instance
	err        error
}

// New returns a default  LogReaderBuilder with VXID grouping
func New() *LogReaderBuilder {
	vsm := C.VSM_New()
	if vsm == nil {
		return &LogReaderBuilder{err: fmt.Errorf("VSM_New failed")}
	}
	vsl := C.VSL_New()
	if vsl == nil {
		C.VSM_Destroy(&vsm)
		return &LogReaderBuilder{err: fmt.Errorf("VSL_New failed")}
	}
	return &LogReaderBuilder{
		vsm:      vsm,
		vsl:      vsl,
		grouping: GroupingVXID,
	}
}

// SetName sets the Varnish instance name (workdir path, the -n argument to varnishd).
func (b *LogReaderBuilder) SetName(name string) *LogReaderBuilder {
	if b.err != nil {
		return b
	}
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	if ret := C.VSM_Arg(b.vsm, 'n', cs); ret < 0 {
		b.err = fmt.Errorf("VSM_Arg -n: %s", C.GoString(C.VSM_Error(b.vsm)))
	}
	return b
}

// SetTimeout sets how long [LogReaderBuilder.Attach] will wait for the Varnish manager.
// A negative duration disables the timeout (waits forever).
func (b *LogReaderBuilder) SetTimeout(timeout time.Duration) *LogReaderBuilder {
	if b.err != nil {
		return b
	}
	var cs *C.char
	if timeout < 0 {
		cs = C.CString("off")
	} else {
		cs = C.CString(strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64))
	}
	defer C.free(unsafe.Pointer(cs))
	if ret := C.VSM_Arg(b.vsm, 't', cs); ret < 0 {
		b.err = fmt.Errorf("VSM_Arg -t: %s", C.GoString(C.VSM_Error(b.vsm)))
	}
	return b
}

// SetQuery sets a VSL query expression to filter which transactions are delivered.
// See vsl-query(7) for syntax.
func (b *LogReaderBuilder) SetQuery(query string) *LogReaderBuilder {
	b.query = query
	return b
}

// SetGrouping controls how records are grouped into transactions.
// The default is [GroupingVXID].
func (b *LogReaderBuilder) SetGrouping(g Grouping) *LogReaderBuilder {
	b.grouping = g
	return b
}

// SetErrHandler registers a callback that is invoked whenever a recoverable
// VSL read error occurs (overrun, abandon, worker restart, cursor loss).
// If not set, such conditions are silently ignored and Run keeps reconnecting.
func (b *LogReaderBuilder) SetErrHandler(h func(LogErr)) *LogReaderBuilder {
	b.errHandler = h
	return b
}

// SetBacklog makes [LogReader.Run] process existing buffered log records
// before following the live tail. Equivalent to varnishlog's -d flag.
// Has no effect when [SetFile] is used.
func (b *LogReaderBuilder) SetBacklog(enable bool) *LogReaderBuilder {
	b.backlog = enable
	return b
}

// SetLive controls whether [LogReader.Run] keeps streaming after catching up
// to the live tail. By default (SetLive not called), Run follows new records
// indefinitely. Call SetLive(false) to stop cleanly once caught up.
func (b *LogReaderBuilder) SetLive(live bool) *LogReaderBuilder {
	b.live = &live
	if !live {
		b.backlog = true
	}
	return b
}

// SetFile makes [LogReader.Run] read from a binary VSL file written by
// varnishlog -w, instead of connecting to a live Varnish instance.
// When set, [SetName] and [SetTimeout] are ignored.
func (b *LogReaderBuilder) SetFile(filename string) *LogReaderBuilder {
	b.file = filename
	return b
}

// Attach connects to the Varnish shared memory segment and returns a [LogReader].
// On failure, all underlying handles are freed and the builder must not be reused.
func (b *LogReaderBuilder) Attach() (*LogReader, error) {
	if b.err != nil {
		return nil, b.err
	}

	var cquery *C.char
	if b.query != "" {
		cquery = C.CString(b.query)
		defer C.free(unsafe.Pointer(cquery))
	}

	vslq := C.VSLQ_New(b.vsl, nil, C.enum_VSL_grouping_e(b.grouping), cquery)
	if vslq == nil {
		err := fmt.Errorf("VSLQ_New: %s", C.GoString(C.VSL_Error(b.vsl)))
		C.VSL_Delete(b.vsl)
		C.VSM_Destroy(&b.vsm)
		return nil, err
	}

	if b.file == "" {
		if ret := C.VSM_Attach(b.vsm, 0); ret != 0 {
			err := fmt.Errorf("VSM_Attach: %s", C.GoString(C.VSM_Error(b.vsm)))
			C.VSLQ_Delete(&vslq)
			C.VSL_Delete(b.vsl)
			C.VSM_Destroy(&b.vsm)
			return nil, err
		}
	}

	r := &LogReader{
		vsm:        b.vsm,
		vsl:        b.vsl,
		vslq:       vslq,
		errHandler: b.errHandler,
		backlog:    b.backlog,
		live:       b.live,
		file:       b.file,
	}
	r.handle = cgo.NewHandle(r)
	return r, nil
}

// LogReader reads VSL log records from a live Varnish instance or a file.
// Obtain one via [LogReaderBuilder.Attach] and [LogReader.Run] to start streaming.
// Call [LogReader.Close] when done.
type LogReader struct {
	vsm    *C.struct_vsm
	vsl    *C.struct_VSL_data
	vslq   *C.struct_VSLQ
	handle cgo.Handle
	backlog bool  // start cursor at log head instead of tail
	live    *bool // nil=stop at end, true=follow, false=stop
	file    string // non-empty: read from this VSL file

	// set for the duration of a Run call; accessed only on the Run goroutine
	handler    func([]Transaction) error
	handlerErr error
	errHandler func(LogErr)
}

// Run streams VSL transactions, calling handler for each group, until ctx is
// cancelled or an unrecoverable error occurs. It transparently handles Varnish
// worker restarts: if the worker process is not running it waits and retries,
// but it will call the optional error handler if [LogReaderBuilder.SetErrHandler] was used.
// was used.
//
// Run returns nil on clean EOF (file-based reading or if [LogReaderBuilder.SetLive] was used with false), ctx.Err() on cancellation,
// or the first non-nil error returned by the error handler.
func (r *LogReader) Run(ctx context.Context, handler func([]Transaction) error) error {
	r.handler = handler
	defer func() { r.handler = nil }()
	priv := C.uintptr_t(r.handle)
	if r.file != "" {
		return r.runFile(ctx, priv)
	}
	return r.runLive(ctx, priv)
}

func (r *LogReader) runFile(ctx context.Context, priv C.uintptr_t) error {
	cs := C.CString(r.file)
	defer C.free(unsafe.Pointer(cs))
	c := C.VSL_CursorFile(r.vsl, cs, 0)
	if c == nil {
		return fmt.Errorf("VSL_CursorFile: %s", C.GoString(C.VSL_Error(r.vsl)))
	}
	C.VSLQ_SetCursor(r.vslq, &c)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		i := int(C.callVSLQDispatch(r.vslq, priv))
		if err := r.takeHandlerErr(); err != nil {
			return err
		}
		switch i {
		case vslMore:
			// keep going
		case vslEnd, vslEEOF:
			C.callVSLQFlush(r.vslq, priv)
			return r.takeHandlerErr()
		default:
			return fmt.Errorf("VSL read error on %s: status %d", r.file, i)
		}
	}
}

func (r *LogReader) runLive(ctx context.Context, priv C.uintptr_t) error {
	hascursor := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		status := uint(C.VSM_Status(r.vsm))

		if !hascursor {
			if status&uint(C.VSM_WRK_RUNNING) == 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
				continue
			}
			opts := C.uint(C.VSL_COPT_BATCH)
			if !r.backlog {
				opts |= C.uint(C.VSL_COPT_TAIL)
			}
			c := C.VSL_CursorVSM(r.vsl, r.vsm, opts)
			if c == nil {
				r.notifyErr(ErrCursorLost)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
				continue
			}
			C.VSLQ_SetCursor(r.vslq, &c)
			hascursor = true
		} else if status&uint(C.VSM_WRK_RESTARTED|C.VSM_WRK_CHANGED) != 0 {
			// Worker restarted or VSM changed (e.g. VCL reload on Varnish Plus):
			// existing cursor is stale — flush pending records and reconnect.
			r.notifyErr(ErrWorkerRestarted)
			C.callVSLQFlush(r.vslq, priv)
			if err := r.takeHandlerErr(); err != nil {
				return err
			}
			C.VSLQ_SetCursor(r.vslq, nil)
			hascursor = false
			continue
		}

		i := int(C.callVSLQDispatch(r.vslq, priv))
		if err := r.takeHandlerErr(); err != nil {
			return err
		}

		switch i {
		case vslMore:
			// more data available — loop immediately
		case vslEnd:
			// caught up to the live tail
			if r.live != nil && !*r.live {
				C.callVSLQFlush(r.vslq, priv)
				return r.takeHandlerErr()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		case vslEEOF:
			return nil
		case vslEOverrun:
			r.notifyErr(ErrOverrun)
			C.callVSLQFlush(r.vslq, priv)
			if err := r.takeHandlerErr(); err != nil {
				return err
			}
			C.VSLQ_SetCursor(r.vslq, nil)
			hascursor = false
		case vslEIO:
			r.notifyErr(ErrIO)
			C.callVSLQFlush(r.vslq, priv)
			if err := r.takeHandlerErr(); err != nil {
				return err
			}
			C.VSLQ_SetCursor(r.vslq, nil)
			hascursor = false
		default: // vslEAbandon and any unexpected status
			r.notifyErr(ErrAbandoned)
			C.callVSLQFlush(r.vslq, priv)
			if err := r.takeHandlerErr(); err != nil {
				return err
			}
			C.VSLQ_SetCursor(r.vslq, nil)
			hascursor = false
		}
	}
}

func (r *LogReader) notifyErr(e LogErr) {
	if r.errHandler != nil {
		r.errHandler(e)
	}
}

func (r *LogReader) takeHandlerErr() error {
	if r.handlerErr != nil {
		err := r.handlerErr
		r.handlerErr = nil
		return err
	}
	return nil
}

// Close releases all resources held by the LogReader. It must be called exactly
// once when the LogReader is no longer needed.
func (r *LogReader) Close() {
	C.VSLQ_Delete(&r.vslq)
	C.VSL_Delete(r.vsl)
	C.VSM_Destroy(&r.vsm)
	r.handle.Delete()
}
