package stat

// #include <stdint.h>
// #include <stdio.h>
// #include <vdef.h>
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
	key := C.GoString(pt.name)
	c.Stats[key] = Counter{
		SDesc:     C.GoString(pt.sdesc),
		LDesc:     C.GoString(pt.ldesc),
		Value:     (*uint64)(unsafe.Pointer(pt.ptr)),
		Semantics: semanticsFromC(C.int(pt.semantics)),
		Flags:     flagsFromC(C.int(pt.format)),
	}
	c.added = append(c.added, key)

	return nil
}

//export delPointCallback
func delPointCallback(priv unsafe.Pointer, pt *C.struct_VSC_point) {
	c := clientFromPriv(priv)
	key := C.GoString(pt.name)
	delete(c.Stats, key)
	c.removed = append(c.removed, key)
}

//export iterCallback
func iterCallback(priv unsafe.Pointer, pt *C.struct_VSC_point) C.int {
	_, _ = priv, pt
	return 0
}
