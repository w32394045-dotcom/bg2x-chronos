//go:build cgo && chronos_native

package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#cgo LDFLAGS: -L${SRCDIR}/../../native/build -lchronos
#include <stdlib.h>
#include "chronos.h"

static int chronos_abi(void) { return CHRONOS_ABI_VERSION; }
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

type nativeEngine struct {
	cfg  Config
	sess *C.chronos_session
	mu   sync.Mutex

	pollMu   sync.Mutex
	pollStop chan struct{}
}

func newNativeEngine(cfg Config) (Engine, error) {
	if int(C.chronos_abi()) != 1 {
		return nil, CodedError(CodeBackend, "native", "unexpected ABI version")
	}
	opts := C.chronos_options{}
	opts.struct_size = C.int(C.sizeof_chronos_options)
	opts.tile_size = C.int(cfg.TileSize)
	opts.overlap = C.int(cfg.Overlap)
	opts.threads = C.int(cfg.Threads)
	opts.device_index = C.int(cfg.DeviceIndex)
	if cfg.Fast {
		opts.flags |= C.int(C.CHRONOS_FLAG_FAST)
	}

	var (
		modelPtr *C.char
		errBuf   = make([]byte, 512)
	)
	if cfg.ModelPath != "" {
		modelPtr = C.CString(cfg.ModelPath)
		defer C.free(unsafe.Pointer(modelPtr))
	}
	eb := (*C.char)(unsafe.Pointer(&errBuf[0]))
	sess := C.chronos_session_create(modelPtr, C.int(cfg.Scale), &opts, eb, C.int(len(errBuf)))
	if sess == nil {
		msg := C.GoString(eb)
		if msg == "" {
			msg = C.GoString(C.chronos_last_error())
		}
		return nil, CodedError(CodeModel, "session_create", msg)
	}
	e := &nativeEngine{cfg: cfg, sess: sess}
	return e, nil
}

func (e *nativeEngine) Info() Info {
	info := Info{Native: true, ABIVersion: int(C.chronos_abi())}
	backend := int(C.chronos_session_backend(e.sess))
	info.Backend = C.GoString(C.chronos_backend_name(C.int(backend)))
	buf := make([]byte, 256)
	if n := C.chronos_session_describe(e.sess, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))); n > 0 {
		info.Detail = C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
	}
	info.Device = "CPU"
	switch info.Backend {
	case "openvino":
		info.Device = "AUTO"
	}
	if info.Detail == "" {
		info.Detail = "native core"
	}
	return info
}

func (e *nativeEngine) Process(ctx context.Context, req Request) (Result, error) {
	if req.Input == "" || req.Output == "" {
		return Result{}, CodedError(CodeParam, "process", "input and output paths are required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	start := time.Now()
	config := e.cfg
	info := e.Info()

	cin := C.CString(req.Input)
	cout := C.CString(req.Output)
	defer C.free(unsafe.Pointer(cin))
	defer C.free(unsafe.Pointer(cout))

	errBuf := make([]byte, 1024)
	eb := (*C.char)(unsafe.Pointer(&errBuf[0]))
	var w, h C.int

	C.chronos_session_reset_progress(e.sess)
	stop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			C.chronos_session_cancel(e.sess)
		case <-stop:
		}
	}()
	go e.pollProgress(ctx, req.OnProgress, stop)

	code := int(C.chronos_process(e.sess, cin, cout, &w, &h, eb, C.int(len(errBuf))))
	close(stop)
	<-watchDone

	if code != 0 {
		msg := C.GoString(eb)
		if msg == "" {
			msg = C.GoString(C.chronos_last_error())
		}
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrCancelled, ctx.Err())
		}
		return Result{}, CodedError(code, "process", msg)
	}
	res := Result{
		Output:    req.Output,
		Width:     int(w),
		Height:    int(h),
		Backend:   info.Backend,
		Elapsed:   time.Since(start),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	_ = config
	return res, nil
}

func (e *nativeEngine) pollProgress(ctx context.Context, cb func(Progress), stop <-chan struct{}) {
	if cb == nil {
		return
	}
	ticker := time.NewTicker(progressPollGap)
	defer ticker.Stop()
	var done, total C.int
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			C.chronos_session_progress(e.sess, &done, &total)
			if total > 0 {
				cb(Progress{Done: int(done), Total: int(total)})
			}
		}
	}
}

func (e *nativeEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sess != nil {
		C.chronos_session_free(e.sess)
		e.sess = nil
	}
	return nil
}
