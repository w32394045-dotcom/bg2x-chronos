//go:build !(cgo && chronos_native)

package engine

// inspectModel is unavailable without the native core: returning a clear error
// keeps the import flow honest instead of silently guessing the scale.
func inspectModel(string) (ModelInspection, error) {
	return ModelInspection{}, ErrInspectUnavailable
}
