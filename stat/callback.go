package stat

// #include <stdint.h>
// #include <vapi/vsc.h>
import "C"
import (
	"runtime/cgo"
	"unsafe"
)

func clientFromPriv(priv unsafe.Pointer) *StatReader {
	return cgo.Handle(uintptr(priv)).Value().(*StatReader)
}

//export newPointCallback
func newPointCallback(priv unsafe.Pointer, pt *C.struct_VSC_point) unsafe.Pointer {
	c := clientFromPriv(priv)
	key := unsafe.Pointer(pt)
	c.points[key] = Counter{
		Name:      C.GoString(pt.name),
		SDesc:     C.GoString(pt.sdesc),
		LDesc:     C.GoString(pt.ldesc),
		Value:     uint64(C.VSC_Value(pt)),
		Semantics: semanticsFromC(C.int(pt.semantics)),
		Flags:     flagsFromC(C.int(pt.format)),
	}
	c.added = append(c.added, key)

	return nil
}

//export delPointCallback
func delPointCallback(priv unsafe.Pointer, pt *C.struct_VSC_point) {
	c := clientFromPriv(priv)
	key := unsafe.Pointer(pt)
	delete(c.points, key)
	c.removed = append(c.removed, key)
}

//export iterCallback
func iterCallback(priv unsafe.Pointer, pt *C.struct_VSC_point) C.int {
	c := clientFromPriv(priv)
	key := unsafe.Pointer(pt)
	if counter, ok := c.points[key]; ok {
		counter.Value = uint64(C.VSC_Value(pt))
		c.points[key] = counter
	}
	return 0
}
