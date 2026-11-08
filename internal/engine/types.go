package engine

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Hard limits from the architecture spec (docs/CONTRACTS.md 搂0 rule 5).
const (
	MaxConcurrency  = 8
	DefaultTile     = 256
	DefaultOverlap  = 16
	DefaultScale    = 4
	DefaultThreads  = 0 // 0 => native default (hardware_concurrency, capped by C++ at 8)
	MaxTileSize     = 4096
	MaxScale        = 8
	progressPollGap = 50 * time.Millisecond
)

// Error codes shared with the C ABI (chronos.h).
const (
	CodeOK        = 0
	CodeIO        = -1
	CodeModel     = -2
	CodeInfer     = -3
	CodeParam     = -4
	CodeBackend   = -5
	CodeCancelled = -6
	CodeUnknown   = -99
)

// Sentinel errors, matched with errors.Is. Native failures additionally carry a
// *Error with the raw C code for diagnostics.
var (
	ErrIO        = errors.New("chronos: image io error")
	ErrModel     = errors.New("chronos: model error")
	ErrInfer     = errors.New("chronos: inference error")
	ErrParam     = errors.New("chronos: invalid parameter")
	ErrBackend   = errors.New("chronos: backend error")
	ErrCancelled = errors.New("chronos: cancelled")
	ErrUnknown   = errors.New("chronos: unknown error")

	// ErrNativeUnavailable is returned by the native constructor when the
	// process was built without cgo / the chronos shared library.
	ErrNativeUnavailable = errors.New("chronos: native core unavailable")
)

// Error is a coded engine failure. Code is one of the Code* constants.
type Error struct {
	Code int
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("chronos: %s: %s (code %d)", e.Op, e.Msg, e.Code)
	}
	return fmt.Sprintf("chronos: %s (code %d)", e.Msg, e.Code)
}

// Unwrap maps an Error to its sentinel so errors.Is works everywhere.
func (e *Error) Unwrap() error {
	switch e.Code {
	case CodeIO:
		return ErrIO
	case CodeModel:
		return ErrModel
	case CodeInfer:
		return ErrInfer
	case CodeParam:
		return ErrParam
	case CodeBackend:
		return ErrBackend
	case CodeCancelled:
		return ErrCancelled
	}
	return ErrUnknown
}

// CodedError builds an *Error carrying the given C-style code.
func CodedError(code int, op, msg string) *Error {
	return &Error{Code: code, Op: op, Msg: msg}
}

// Config describes one processing pipeline (one model + one tiling setup).
type Config struct {
	ModelPath   string // empty => stub (high quality resample) backend
	Scale       int    // 1..8, model native scale or desired output scale
	TileSize    int
	Overlap     int
	Threads     int
	DeviceIndex int
	Fast        bool // bilinear resample instead of Lanczos in stub backend
}

// Normalize fills defaults and clamps values into supported ranges.
func (c Config) Normalize() Config {
	if c.Scale <= 0 {
		c.Scale = DefaultScale
	}
	if c.Scale > MaxScale {
		c.Scale = MaxScale
	}
	if c.TileSize <= 0 {
		c.TileSize = DefaultTile
	}
	if c.TileSize > MaxTileSize {
		c.TileSize = MaxTileSize
	}
	if c.Overlap <= 0 {
		c.Overlap = DefaultOverlap
	}
	if c.Overlap >= c.TileSize {
		c.Overlap = c.TileSize / 4
	}
	if c.Threads < 0 {
		c.Threads = 0
	}
	if c.Threads > 64 {
		c.Threads = 64
	}
	return c
}

// Validate reports whether the config can be used at all.
func (c Config) Validate() error {
	if c.Scale < 1 || c.Scale > MaxScale {
		return CodedError(CodeParam, "config", fmt.Sprintf("scale %d out of range 1..%d", c.Scale, MaxScale))
	}
	if c.TileSize <= 0 || c.Overlap < 0 || c.Overlap >= c.TileSize {
		return CodedError(CodeParam, "config", "invalid tile/overlap")
	}
	return nil
}

// Info describes the active backend.
type Info struct {
	Backend    string `json:"backend"` // "openvino" | "stub" | "go-lanczos"
	Device     string `json:"device"`  // "GPU" | "CPU" | "n/a"
	Detail     string `json:"detail"`
	ABIVersion int    `json:"abiVersion"`
	Native     bool   `json:"native"`
}

// Progress is emitted while one image is being processed.
type Progress struct {
	Done  int
	Total int
}

// Request is a single image job (paths only; pixels never cross this boundary).
type Request struct {
	Input      string
	Output     string
	OnProgress func(Progress)
}

// Result describes one processed image.
type Result struct {
	Output    string        `json:"output"`
	Width     int           `json:"width"`
	Height    int           `json:"height"`
	Tiles     int           `json:"tiles"`
	Backend   string        `json:"backend"`
	Elapsed   time.Duration `json:"elapsed"`
	ElapsedMs int64         `json:"elapsedMs"`
}

// Engine processes one image at a time. Implementations are NOT safe for
// concurrent Process calls; use a Provider to obtain one engine per worker.
type Engine interface {
	Info() Info
	Process(ctx context.Context, req Request) (Result, error)
	Close() error
}

// Provider hands out engines. Engines returned by Acquire must be returned with
// Release; the provider owns their lifetime.
type Provider interface {
	Info() Info
	Acquire(ctx context.Context) (Engine, error)
	Release(Engine)
	Close() error
}
