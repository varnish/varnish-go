package log

// #include <stdint.h>
// #include <stdio.h>
// #include <vapi/vsl.h>
//
// static struct VSL_transaction *transAt(struct VSL_transaction * const trans[], int i) {
//     return trans[i];
// }
//
// static int recTag(const uint32_t *ptr) { return (int)VSL_TAG(ptr); }
// static uint64_t recID(const uint32_t *ptr) { return (uint64_t)VSL_ID(ptr); }
// static const char* recData(const uint32_t *ptr) { return VSL_CDATA(ptr); }
// static int recClient(const uint32_t *ptr) { return VSL_CLIENT(ptr) != 0; }
// static int recBackend(const uint32_t *ptr) { return VSL_BACKEND(ptr) != 0; }
import "C"
import (
	"runtime/cgo"
	"unsafe"
)

func readerFromPriv(priv unsafe.Pointer) *LogReader {
	return cgo.Handle(uintptr(priv)).Value().(*LogReader)
}

//export dispatchCallback
func dispatchCallback(_ *C.struct_VSL_data, ctrans **C.struct_VSL_transaction, priv unsafe.Pointer) C.int {
	r := readerFromPriv(priv)

	var txns []Transaction
	for i := C.int(0); ; i++ {
		t := C.transAt(ctrans, i)
		if t == nil {
			break
		}

		var records []Record
		for t.c != nil && C.VSL_Next(t.c) == C.vsl_more {
			ptr := t.c.rec.ptr
			records = append(records, Record{
				Tag:       Tag(C.recTag(ptr)),
				VXID:      uint64(C.recID(ptr)),
				IsClient:  C.recClient(ptr) != 0,
				IsBackend: C.recBackend(ptr) != 0,
				Data:      C.GoString(C.recData(ptr)),
			})
		}

		txns = append(txns, Transaction{
			Level:      int(t.level),
			VXID:       int64(t.vxid),
			ParentVXID: int64(t.vxid_parent),
			Type:       TransactionType(t._type),
			Reason:     Reason(t.reason),
			Records:    records,
		})
	}

	if err := r.handler(txns); err != nil {
		r.handlerErr = err
		return -1
	}
	return 0
}
