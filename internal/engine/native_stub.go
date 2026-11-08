//go:build !(cgo && chronos_native)

package engine

// newNativeEngine is the stub used when the binary is built without the C++
// core. Callers fall back to the pure Go pipeline (see newEngine).
func newNativeEngine(Config) (Engine, error) {
	return nil, ErrNativeUnavailable
}

// runtimeProbe reports no native capabilities when the C++ core is absent.
func runtimeProbe() Probe { return Probe{} }
