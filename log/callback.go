package log

// #include <stdint.h>
// #include <stdio.h>
// #include <vapi/vsl.h>
//
// static inline struct VSL_transaction *transAt(struct VSL_transaction * const trans[], int i) {
//     return trans[i];
// }
//
// static inline int recTag(const uint32_t *ptr) { return (int)VSL_TAG(ptr); }
// static inline int recLen(const uint32_t *ptr) { return (int)VSL_LEN(ptr); }
//
// // recDataLen returns the record payload length without the terminating NUL
// // byte that text records include in VSL_LEN. Binary records (SLT_F_BINARY)
// // are not NUL terminated, so their length is returned as is.
// static inline int recDataLen(const uint32_t *ptr) {
//     int len = (int)VSL_LEN(ptr);
//     if (!(VSL_tagflags[VSL_TAG(ptr)] & SLT_F_BINARY) &&
//         len > 0 && VSL_CDATA(ptr)[len - 1] == '\0')
//         len--;
//     return len;
// }
// static inline uint64_t recID(const uint32_t *ptr) { return (uint64_t)VSL_ID(ptr); }
// static inline const char* recData(const uint32_t *ptr) { return VSL_CDATA(ptr); }
// static inline int recClient(const uint32_t *ptr) { return VSL_CLIENT(ptr) != 0; }
// static inline int recBackend(const uint32_t *ptr) { return VSL_BACKEND(ptr) != 0; }
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
		for {
			status := C.VSL_Next(t.c)
			if status == C.vsl_end {
				break
			}
			if status < 0 {
				return C.int(status)
			}
			ptr := t.c.rec.ptr
			records = append(records, Record{
				Tag:       Tag(C.recTag(ptr)),
				VXID:      uint64(C.recID(ptr)),
				IsClient:  C.recClient(ptr) != 0,
				IsBackend: C.recBackend(ptr) != 0,
				Data:      C.GoStringN(C.recData(ptr), C.recDataLen(ptr)),
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
