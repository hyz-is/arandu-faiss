//go:build !cgo || nofaiss

package faiss

// handle is empty in a build without FAISS: no index is ever built.
type handle struct{}

// engineAvailable reports that FAISS is not linked into this build, and why.
func engineAvailable() error { return ErrUnavailable }

// engineVersion is empty: no FAISS library is loaded.
func engineVersion() string { return "" }

func engineBuild(IndexSpec) (handle, error)            { return handle{}, ErrUnavailable }
func engineParameters(handle, string) error            { return ErrUnavailable }
func engineFree(handle)                                {}
func engineDim(handle) int                             { return 0 }
func engineCount(handle) int                           { return 0 }
func engineMetricIsL2(handle) bool                     { return false }
func engineTrained(handle) bool                        { return false }
func engineTrain(handle, int, []float32) error         { return ErrUnavailable }
func engineAdd(handle, []int64, []float32) error       { return ErrUnavailable }
func engineRemove(handle, []int64) (int, error)        { return 0, ErrUnavailable }
func engineIDs(handle) []int64                         { return nil }
func engineReconstruct(handle, int64, []float32) error { return ErrUnavailable }
func engineSerialize(handle) ([]byte, error)           { return nil, ErrUnavailable }
func engineDeserialize([]byte) (handle, error)         { return handle{}, ErrUnavailable }
func engineSearch(handle, []float32, int) ([]float32, []int64, error) {
	return nil, nil, ErrUnavailable
}
