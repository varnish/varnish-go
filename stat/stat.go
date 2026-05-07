// Reads Varnish statistics (like `varnishstat` does) from the Varnish Shared Memory (VSM)
// using libvarnishapi's VSC (Varnish Statistics Counters) API.
//
// Each counter has a fully-qualified name (e.g. "MAIN.cache_hit"), a current
// value, and metadata describing its semantics and display format.
//
// # Usage
//
// Dump all counters as JSON:
//
//	b := stat.New().SetTimeout(*timeout)
//	if *name != "" {
//	    b = b.SetName(*name)
//	}
//
//	r, err := b.Attach()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer r.Close()
//
//	if _, _, err := r.Update(); err != nil {
//	    log.Fatal(err)
//	}
//
//	enc := json.NewEncoder(os.Stdout)
//	enc.Encode(r.Counters())

package stat

// #cgo pkg-config: varnishapi
// #include <stdint.h>
// #include <stdlib.h>
// #include <vapi/vsm.h>
// #include <vapi/vsc.h>
//
// extern void *newPointCallback(void *priv, const struct VSC_point *const pt);
// extern void  delPointCallback(void *priv, const struct VSC_point *const pt);
// extern int   iterCallback(void *priv, const struct VSC_point *pt);
//
// static void callVSCState(struct vsc *vsc, void *priv) {
//     VSC_State(vsc, newPointCallback, delPointCallback, priv);
// }
//
// static int callVSCIter(struct vsc *vsc, struct vsm *vsm, void *priv) {
//     return VSC_Iter(vsc, vsm, iterCallback, priv);
// }
import "C"
import (
	"fmt"
	"runtime/cgo"
	"strconv"
	"time"
	"unsafe"
)

// Semantics describes how a counter's value should be interpreted.
type Semantics int

const (
	SemanticsUnknown Semantics = iota // unrecognised semantics character
	SemanticsCounter                  // 'c': monotonically increasing count
	SemanticsGauge                    // 'g': instantaneous level, may decrease
	SemanticsBitmap                   // 'b': bitmask
	SemanticsBoolean                  // 'q': boolean flag
)

// Flags describes the preferred display format of a counter's value.
type Flags int

const (
	FlagsUnknown  Flags = iota // unrecognised format character
	FlagsInteger               // 'i': plain integer
	FlagsBytes                 // 'B': byte quantity
	FlagsBitmap                // 'b': bitmask
	FlagsBoolean               // 'q': boolean
	FlagsDuration              // 'd': duration in seconds
)

func (s Semantics) String() string {
	switch s {
	case SemanticsCounter:
		return "counter"
	case SemanticsGauge:
		return "gauge"
	case SemanticsBitmap:
		return "bitmap"
	case SemanticsBoolean:
		return "boolean"
	default:
		return "unknown"
	}
}

// MarshalText implements encoding.TextMarshaler so that Semantics serializes
// as a human-readable string in JSON, YAML, and other text-based encodings.
func (s Semantics) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

func (f Flags) String() string {
	switch f {
	case FlagsInteger:
		return "integer"
	case FlagsBytes:
		return "bytes"
	case FlagsBitmap:
		return "bitmap"
	case FlagsBoolean:
		return "boolean"
	case FlagsDuration:
		return "duration"
	default:
		return "unknown"
	}
}

// MarshalText implements encoding.TextMarshaler so that Flags serializes
// as a human-readable string in JSON, YAML, and other text-based encodings.
func (f Flags) MarshalText() ([]byte, error) {
	return []byte(f.String()), nil
}

func semanticsFromC(c C.int) Semantics {
	switch c {
	case 'c':
		return SemanticsCounter
	case 'g':
		return SemanticsGauge
	case 'b':
		return SemanticsBitmap
	case 'q':
		return SemanticsBoolean
	default:
		return SemanticsUnknown
	}
}

func flagsFromC(c C.int) Flags {
	switch c {
	case 'i':
		return FlagsInteger
	case 'B':
		return FlagsBytes
	case 'b':
		return FlagsBitmap
	case 'q':
		return FlagsBoolean
	case 'd':
		return FlagsDuration
	default:
		return FlagsUnknown
	}
}

// Counter is a snapshot of a single Varnish statistic, mirroring VSC_point.
type Counter struct {
	Name      string    `json:"name"        yaml:"name"`                      // fully-qualified name, e.g. "MAIN.cache_hit"
	SDesc     string    `json:"description" yaml:"description"`               // short description
	LDesc     string    `json:"longDescription"       yaml:"longDescription"` // long description
	Value     uint64    `json:"value"       yaml:"value"`                     // current value at the time of the last Update
	Semantics Semantics `json:"semantics"   yaml:"semantics"`
	Flags     Flags     `json:"flags"       yaml:"flags"`
}

// StatReaderBuilder configures the connection to a Varnish instance.
// Obtain one with [New], optionally call [StatReaderBuilder.SetName]
// and [StatReaderBuilder.SetTimeout], then call [StatReaderBuilder.Attach] to
// get a [StatReader].
type StatReaderBuilder struct {
	vsm *C.struct_vsm
	vsc *C.struct_vsc
	err error
}

// New returns a new StatReaderBuilder with default settings.
// You can customize it with [StatReaderBuilder.SetName],
// [StatReaderBuilder.SetTimeout], etc. before calling [StatReaderBuilder.Attach] to get a [StatReader].
func New() *StatReaderBuilder {
	vsm := C.VSM_New()
	if vsm == nil {
		return &StatReaderBuilder{err: fmt.Errorf("VSM_New failed")}
	}
	vsc := C.VSC_New()
	if vsc == nil {
		C.VSM_Destroy(&vsm)
		return &StatReaderBuilder{err: fmt.Errorf("VSC_New failed")}
	}
	return &StatReaderBuilder{vsm: vsm, vsc: vsc}
}

// SetName sets the Varnish instance name (workdir path) to connect to.
// Corresponds to the -n flag of varnishd.
func (b *StatReaderBuilder) SetName(name string) *StatReaderBuilder {
	if b.err != nil {
		return b
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	if ret := C.VSM_Arg(b.vsm, 'n', cname); ret < 0 {
		b.err = fmt.Errorf("VSM_Arg -n: %s", C.GoString(C.VSM_Error(b.vsm)))
	}
	return b
}

// SetTimeout sets how long [StatReaderBuilder.Attach] will wait for the
// Varnish manager to become available. A zero duration uses the libvarnishapi
// default (five seconds).
func (b *StatReaderBuilder) SetTimeout(timeout time.Duration) *StatReaderBuilder {
	if b.err != nil {
		return b
	}
	ct := C.CString(strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64))
	defer C.free(unsafe.Pointer(ct))
	if ret := C.VSM_Arg(b.vsm, 't', ct); ret < 0 {
		b.err = fmt.Errorf("VSM_Arg -t: %s", C.GoString(C.VSM_Error(b.vsm)))
	}
	return b
}

// Attach connects to the Varnish shared memory segment and returns a
// [StatReader]. If progress is a non-negative file descriptor, a period is
// written to it for each second spent waiting; pass -1 to suppress progress
// output. On failure the underlying VSM and VSC handles are freed; the
// StatReaderBuilder must not be used again.
func (b *StatReaderBuilder) Attach() (*StatReader, error) {
	if b.err != nil {
		return nil, b.err
	}
	if ret := C.VSM_Attach(b.vsm, 0); ret != 0 {
		err := fmt.Errorf("VSM_Attach: %s", C.GoString(C.VSM_Error(b.vsm)))
		C.VSC_Destroy(&b.vsc, b.vsm)
		C.VSM_Destroy(&b.vsm)
		return nil, err
	}
	r := &StatReader{
		vsm:    b.vsm,
		vsc:    b.vsc,
		points: make(map[unsafe.Pointer]Counter),
	}
	r.handle = cgo.NewHandle(r)
	C.callVSCState(b.vsc, unsafe.Pointer(uintptr(r.handle)))
	return r, nil
}

// StatReader reads VSC statistics from a Varnish instance.
// Obtain one via [StatReaderBuilder.Attach]. Call [StatReader.Update] to
// refresh the counter set, then query it with [StatReader.Counter],
// [StatReader.Counters], or [StatReader.CounterValue]. Call [StatReader.Close]
// when done.
type StatReader struct {
	vsm     *C.struct_vsm
	vsc     *C.struct_vsc
	points  map[unsafe.Pointer]Counter
	handle  cgo.Handle
	added   []unsafe.Pointer
	removed []unsafe.Pointer
}

// Update refreshes the counter set. It detects VSM changes (child start/stop)
// and updates the value of every known counter. It returns the opaque keys of
// counters added and removed since the previous call; pass a key to
// [StatReader.Counter] after an add, or use it to invalidate cached state
// after a remove.
func (r *StatReader) Update() (added, removed []unsafe.Pointer, err error) {
	r.added = r.added[:0]
	r.removed = r.removed[:0]
	C.VSM_Status(r.vsm)
	ret := C.callVSCIter(r.vsc, r.vsm, unsafe.Pointer(uintptr(r.handle)))
	if ret != 0 {
		return nil, nil, fmt.Errorf("VSC_Iter returned %d", ret)
	}
	return r.added, r.removed, nil
}

// Counters returns a snapshot of all currently known counters, indexed by
// their full name. The map is newly allocated on each call.
func (r *StatReader) Counters() map[string]Counter {
	m := make(map[string]Counter)
	for _, counter := range r.points {
		m[counter.Name] = counter
	}
	return m
}

// Counter returns the counter with the given fully-qualified name (e.g.
// "MAIN.cache_hit") and true, or a zero Counter and false if not found.
func (r *StatReader) Counter(name string) (Counter, bool) {
	for _, counter := range r.points {
		if counter.Name == name {
			return counter, true
		}
	}
	return Counter{}, false
}

// CounterValue returns the current value of the named counter and true, or 0
// and false if the counter is not found.
func (r *StatReader) CounterValue(name string) (uint64, bool) {
	counter, ok := r.Counter(name)
	return counter.Value, ok
}

// Close releases all resources held by the StatReader. It must be called
// exactly once when the StatReader is no longer needed.
func (r *StatReader) Close() {
	C.VSC_Destroy(&r.vsc, r.vsm)
	C.VSM_Destroy(&r.vsm)
	r.handle.Delete()
}
