//go:build cgo && !nofaiss

package faiss

/*
#cgo darwin,arm64 CFLAGS: -I/opt/homebrew/opt/faiss/include
#cgo darwin,arm64 LDFLAGS: -L/opt/homebrew/opt/faiss/lib
#cgo darwin,amd64 CFLAGS: -I/usr/local/opt/faiss/include
#cgo darwin,amd64 LDFLAGS: -L/usr/local/opt/faiss/lib
#cgo linux CFLAGS: -I/usr/local/include
#cgo linux LDFLAGS: -L/usr/local/lib
#cgo LDFLAGS: -lfaiss_c

#include <stdio.h>
#include <stdlib.h>

#include <faiss/c_api/faiss_c.h>
#include <faiss/c_api/error_c.h>
#include <faiss/c_api/Index_c.h>
#include <faiss/c_api/MetaIndexes_c.h>
#include <faiss/c_api/AutoTune_c.h>
#include <faiss/c_api/index_factory_c.h>
#include <faiss/c_api/IndexIVF_c.h>
#include <faiss/c_api/index_io_c.h>
#include <faiss/c_api/impl/AuxIndexStructures_c.h>
#include <faiss/c_api/utils/utils_c.h>

// arandu_faiss_write serializes an index into a buffer malloc'd by
// open_memstream. The caller frees *buf whatever the result. A negative code
// below -100 is a failure of the stream rather than of FAISS.
static int arandu_faiss_write(FaissIndex* index, char** buf, size_t* len) {
	*buf = NULL;
	*len = 0;
	FILE* f = open_memstream(buf, len);
	if (f == NULL) {
		return -101;
	}
	int rc = faiss_write_index(index, f);
	if (fclose(f) != 0 && rc == 0) {
		rc = -102;
	}
	return rc;
}

// arandu_faiss_read reads an index out of a buffer the caller owns for the
// duration of the call.
static int arandu_faiss_read(void* data, size_t len, FaissIndex** out) {
	*out = NULL;
	FILE* f = fmemopen(data, len, "r");
	if (f == NULL) {
		return -101;
	}
	int rc = faiss_read_index(f, 0, out);
	fclose(f);
	return rc;
}

// arandu_faiss_remove removes a batch of external ids.
static int arandu_faiss_remove(FaissIndex* index, size_t n, const idx_t* ids, size_t* removed) {
	FaissIDSelectorBatch* sel = NULL;
	*removed = 0;
	int rc = faiss_IDSelectorBatch_new(&sel, n, ids);
	if (rc != 0) {
		return rc;
	}
	rc = faiss_Index_remove_ids(index, (const FaissIDSelector*)sel, removed);
	faiss_IDSelector_free((FaissIDSelector*)sel);
	return rc;
}

// arandu_faiss_parameters applies a parameter string such as "efSearch=64".
static int arandu_faiss_parameters(FaissIndex* index, const char* params) {
	FaissParameterSpace* space = NULL;
	int rc = faiss_ParameterSpace_new(&space);
	if (rc != 0) {
		return rc;
	}
	rc = faiss_ParameterSpace_set_index_parameters(space, index, params);
	faiss_ParameterSpace_free(space);
	return rc;
}

// arandu_faiss_is_idmap2 reports whether the index is an IndexIDMap2.
static int arandu_faiss_is_idmap2(FaissIndex* index) {
	return faiss_IndexIDMap2_cast(index) != NULL;
}

// arandu_faiss_direct_map makes an inverted-file index under the IDMap2 keep a
// direct map from its internal ids to their place in the lists, which is what
// lets it read a stored vector back. Any other index is left as it is.
static int arandu_faiss_direct_map(FaissIndex* index) {
	FaissIndex* sub = faiss_IndexIDMap2_sub_index(faiss_IndexIDMap2_cast(index));
	FaissIndexIVF* ivf = sub == NULL ? NULL : faiss_IndexIVF_cast(sub);
	if (ivf == NULL) {
		return 0;
	}
	return faiss_IndexIVF_make_direct_map(ivf, 1);
}

// arandu_faiss_id_map exposes the external ids, in storage order.
static void arandu_faiss_id_map(FaissIndex* index, idx_t** ids, size_t* n) {
	faiss_IndexIDMap2_id_map(faiss_IndexIDMap2_cast(index), ids, n);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"
)

// handle is the FAISS index a Index owns: always an IndexIDMap2 around the
// index the factory string describes, so the ids are the caller's own.
type handle struct {
	p *C.FaissIndex
}

// engineAvailable reports that FAISS is linked into this build.
func engineAvailable() error { return nil }

// engineVersion is the version string of the FAISS library loaded.
func engineVersion() string {
	if v := C.faiss_get_version(); v != nil {
		return C.GoString(v)
	}
	return ""
}

// call runs one FAISS function and turns its failure into an error.
//
// The C API keeps the last error per thread, and a goroutine may move between
// threads from one cgo call to the next. The thread is locked from the call to
// the read of its error, so the message is the one this call left.
func call(op string, fn func() C.int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	rc := fn()
	if rc == 0 {
		return nil
	}
	message := ""
	if rc > -100 {
		if last := C.faiss_get_last_error(); last != nil {
			message = strings.TrimSpace(C.GoString(last))
		}
	} else {
		message = "the in-memory stream could not be opened or closed"
	}
	if message == "" {
		message = fmt.Sprintf("code %d", int(rc))
	}
	return fmt.Errorf("%w: %s: %s", ErrEngine, op, message)
}

// metricType is the FAISS metric for a Metric. Cosine is inner product over
// vectors normalized before they reach FAISS.
func metricType(m Metric) C.FaissMetricType {
	if m == MetricL2 {
		return C.METRIC_L2
	}
	return C.METRIC_INNER_PRODUCT
}

// engineBuild builds an empty index for the spec, which has been normalized.
func engineBuild(spec IndexSpec) (handle, error) {
	description := C.CString("IDMap2," + spec.Factory)
	defer C.free(unsafe.Pointer(description))

	var p *C.FaissIndex
	if err := call("index_factory", func() C.int {
		return C.faiss_index_factory(&p, C.int(spec.Dim), description, metricType(spec.Metric))
	}); err != nil {
		return handle{}, err
	}
	h := handle{p: p}
	if C.arandu_faiss_is_idmap2(p) == 0 {
		engineFree(h)
		return handle{}, fmt.Errorf("%w: the factory string %q did not produce an IndexIDMap2", ErrEngine, spec.Factory)
	}
	if err := engineDirectMap(h); err != nil {
		engineFree(h)
		return handle{}, err
	}
	if err := engineParameters(h, spec.Parameters); err != nil {
		engineFree(h)
		return handle{}, err
	}
	return h, nil
}

// engineDirectMap keeps an inverted-file index able to read its vectors back,
// which Remove needs to rebuild it without the removed ones.
func engineDirectMap(h handle) error {
	return call("make_direct_map", func() C.int { return C.arandu_faiss_direct_map(h.p) })
}

// engineParameters applies the spec's search parameters, if any.
func engineParameters(h handle, parameters string) error {
	if parameters == "" {
		return nil
	}
	cparams := C.CString(parameters)
	defer C.free(unsafe.Pointer(cparams))
	return call("set_index_parameters", func() C.int {
		return C.arandu_faiss_parameters(h.p, cparams)
	})
}

// engineFree releases the index.
func engineFree(h handle) {
	if h.p != nil {
		C.faiss_Index_free(h.p)
	}
}

// engineDim is the dimension FAISS holds for the index.
func engineDim(h handle) int { return int(C.faiss_Index_d(h.p)) }

// engineCount is how many vectors FAISS holds.
func engineCount(h handle) int { return int(C.faiss_Index_ntotal(h.p)) }

// engineMetricIsL2 reports whether FAISS searches the index by L2 distance.
func engineMetricIsL2(h handle) bool { return C.faiss_Index_metric_type(h.p) == C.METRIC_L2 }

// engineTrained reports whether the index needs no training before adding.
func engineTrained(h handle) bool { return C.faiss_Index_is_trained(h.p) != 0 }

// engineTrain trains the index on n vectors laid out row after row.
func engineTrain(h handle, n int, flat []float32) error {
	return call("train", func() C.int {
		return C.faiss_Index_train(h.p, C.idx_t(n), (*C.float)(unsafe.Pointer(&flat[0])))
	})
}

// engineAdd adds n vectors under their external ids.
func engineAdd(h handle, ids []int64, flat []float32) error {
	return call("add_with_ids", func() C.int {
		return C.faiss_Index_add_with_ids(h.p, C.idx_t(len(ids)),
			(*C.float)(unsafe.Pointer(&flat[0])), (*C.idx_t)(unsafe.Pointer(&ids[0])))
	})
}

// engineRemove removes the external ids and reports how many FAISS removed.
func engineRemove(h handle, ids []int64) (int, error) {
	var removed C.size_t
	err := call("remove_ids", func() C.int {
		return C.arandu_faiss_remove(h.p, C.size_t(len(ids)), (*C.idx_t)(unsafe.Pointer(&ids[0])), &removed)
	})
	return int(removed), err
}

// engineSearch searches one query and fills k scores and labels.
func engineSearch(h handle, query []float32, k int) ([]float32, []int64, error) {
	scores := make([]float32, k)
	labels := make([]int64, k)
	err := call("search", func() C.int {
		return C.faiss_Index_search(h.p, 1, (*C.float)(unsafe.Pointer(&query[0])), C.idx_t(k),
			(*C.float)(unsafe.Pointer(&scores[0])), (*C.idx_t)(unsafe.Pointer(&labels[0])))
	})
	return scores, labels, err
}

// engineIDs is every external id the index holds, in storage order.
func engineIDs(h handle) []int64 {
	var p *C.idx_t
	var n C.size_t
	C.arandu_faiss_id_map(h.p, &p, &n)
	if n == 0 || p == nil {
		return nil
	}
	return append([]int64(nil), unsafe.Slice((*int64)(unsafe.Pointer(p)), int(n))...)
}

// engineReconstruct writes the stored vector of an external id into dst.
func engineReconstruct(h handle, id int64, dst []float32) error {
	return call("reconstruct", func() C.int {
		return C.faiss_Index_reconstruct(h.p, C.idx_t(id), (*C.float)(unsafe.Pointer(&dst[0])))
	})
}

// engineSerialize writes the index in FAISS's own format, in memory.
func engineSerialize(h handle) ([]byte, error) {
	var buf *C.char
	var size C.size_t
	err := call("write_index", func() C.int {
		return C.arandu_faiss_write(h.p, &buf, &size)
	})
	defer C.free(unsafe.Pointer(buf))
	if err != nil {
		return nil, err
	}
	return C.GoBytes(unsafe.Pointer(buf), C.int(size)), nil
}

// engineDeserialize reads an index written by engineSerialize.
func engineDeserialize(payload []byte) (handle, error) {
	if len(payload) == 0 {
		return handle{}, fmt.Errorf("%w: the FAISS payload is empty", ErrCorrupt)
	}
	var p *C.FaissIndex
	if err := call("read_index", func() C.int {
		return C.arandu_faiss_read(unsafe.Pointer(&payload[0]), C.size_t(len(payload)), &p)
	}); err != nil {
		return handle{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	h := handle{p: p}
	if C.arandu_faiss_is_idmap2(p) == 0 {
		engineFree(h)
		return handle{}, fmt.Errorf("%w: the FAISS payload is not an IndexIDMap2", ErrCorrupt)
	}
	if err := engineDirectMap(h); err != nil {
		engineFree(h)
		return handle{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return h, nil
}
