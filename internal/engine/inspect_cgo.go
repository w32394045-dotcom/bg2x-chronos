//go:build cgo && chronos_native

package engine

/*
#include <stdlib.h>
#include "chronos/chronos.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// inspectModel asks the native core for the ONNX graph metadata.
func inspectModel(path string) (ModelInspection, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	need := int(C.chronos_model_inspect(cpath, nil, 0))
	if need < 0 {
		msg := C.GoString(C.chronos_last_error())
		if msg == "" {
			msg = "native model inspection failed"
		}
		return ModelInspection{}, CodedError(need, "inspect", msg)
	}
	if need == 0 {
		return ModelInspection{}, fmt.Errorf("chronos: empty model inspection")
	}
	buf := make([]byte, need+1)
	n := int(C.chronos_model_inspect(cpath, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
	if n < 0 {
		msg := C.GoString(C.chronos_last_error())
		if msg == "" {
			msg = "native model inspection failed"
		}
		return ModelInspection{}, CodedError(n, "inspect", msg)
	}
	return parseModelInspection(buf[:n])
}
