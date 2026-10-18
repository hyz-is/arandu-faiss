package faiss

import (
	"fmt"
	"math"
	"runtime"
	"sync"
)

// Candidate is one vector a search found near the query: its id, and how near.
//
// It is a candidate and nothing more. The package says which stored vectors
// are closest under the index's metric; whether any of them is the same person,
// the same document or the same anything is the application's decision, taken
// with its own thresholds and recorded as its own evidence.
type Candidate struct {
	// ID is the id the vector was added under.
	ID int64
	// Score is FAISS's value for the pair, under the index's metric: the
	// cosine similarity for MetricCosine and the inner product for
	// MetricInnerProduct, where higher is closer, and the squared Euclidean
	// distance for MetricL2, where lower is closer. Candidates are ordered
	// closest first.
	Score float32
}

// Provenance is what produced a set of candidates, for the application to
// record beside them.
type Provenance struct {
	// Engine is "faiss".
	Engine string
	// EngineVersion is the version of the FAISS library that searched, as it
	// reports itself.
	EngineVersion string
	// Model is the embedding model the index holds vectors of.
	Model string
	// Dim is the dimension of those vectors.
	Dim int
	// Metric is how they were compared.
	Metric Metric
	// Factory is the FAISS index factory string the index was built with.
	Factory string
	// Parameters is the FAISS search parameters in force, if any.
	Parameters string
	// Method is MethodExhaustive or MethodApproximate.
	Method string
	// Size is how many vectors the index held when it was searched.
	Size int
}

// Index is one FAISS index: vectors of one embedding model, each under an id
// the caller chose.
//
// Its methods are safe for concurrent use. Searches run in parallel with each
// other; Add, Remove and Close wait for the searches in flight and run alone.
// An Index holds memory outside the Go heap, so it is closed when it is no
// longer needed; a finalizer frees one that was not, as a safety net rather
// than a plan.
type Index struct {
	mu     sync.RWMutex
	spec   IndexSpec
	h      handle
	ids    map[int64]struct{}
	closed bool
}

// Available reports whether FAISS is linked into this build. It is nil in a
// build with cgo, and ErrUnavailable in one without, where every index
// operation fails the same way.
func Available() error { return engineAvailable() }

// EngineVersion is the version of the linked FAISS library, as it reports
// itself, or empty when none is linked.
func EngineVersion() string { return engineVersion() }

// NewIndex builds an empty index for the specification.
func NewIndex(spec IndexSpec) (*Index, error) {
	spec, err := spec.normalized()
	if err != nil {
		return nil, err
	}
	h, err := engineBuild(spec)
	if err != nil {
		return nil, err
	}
	return newIndex(spec, h, nil), nil
}

// newIndex wraps a handle FAISS built or read, with the ids it holds.
func newIndex(spec IndexSpec, h handle, ids []int64) *Index {
	idx := &Index{spec: spec, h: h, ids: make(map[int64]struct{}, len(ids))}
	for _, id := range ids {
		idx.ids[id] = struct{}{}
	}
	runtime.SetFinalizer(idx, func(i *Index) { _ = i.Close() })
	return idx
}

// Deserialize reads an index written by Serialize, and refuses it unless it is
// an index of exactly the specification given: the same model, dimension,
// metric and factory string. A specification that changed since the index was
// written is answered with ErrSpecMismatch, and the index is rebuilt from the
// application's templates rather than loaded as something it is not.
//
// The envelope's SHA-256 is checked before FAISS reads a byte, and the index
// FAISS reads is checked again against the envelope.
func Deserialize(blob []byte, spec IndexSpec) (*Index, error) {
	spec, err := spec.normalized()
	if err != nil {
		return nil, err
	}
	if err := engineAvailable(); err != nil {
		return nil, err
	}
	header, payload, err := decodeEnvelope(blob)
	if err != nil {
		return nil, err
	}
	if header.Model != spec.Model || header.Dim != spec.Dim || header.Metric != spec.Metric || header.Factory != spec.Factory {
		return nil, fmt.Errorf("%w: it holds model %q, dim %d, metric %q, factory %q, and was asked for model %q, dim %d, metric %q, factory %q",
			ErrSpecMismatch, header.Model, header.Dim, header.Metric, header.Factory,
			spec.Model, spec.Dim, spec.Metric, spec.Factory)
	}
	h, err := engineDeserialize(payload)
	if err != nil {
		return nil, err
	}
	ids := engineIDs(h)
	switch {
	case engineDim(h) != spec.Dim:
		err = fmt.Errorf("%w: FAISS reads dimension %d where the envelope says %d", ErrCorrupt, engineDim(h), spec.Dim)
	case engineMetricIsL2(h) != (spec.Metric == MetricL2):
		err = fmt.Errorf("%w: FAISS reads another metric than %q", ErrCorrupt, spec.Metric)
	case engineCount(h) != header.Count || len(ids) != header.Count:
		err = fmt.Errorf("%w: FAISS reads %d vectors and %d ids where the envelope says %d", ErrCorrupt, engineCount(h), len(ids), header.Count)
	}
	if err == nil {
		seen := make(map[int64]struct{}, len(ids))
		for _, id := range ids {
			if _, twice := seen[id]; twice || id < 0 {
				err = fmt.Errorf("%w: the id %d is stored twice or is negative", ErrCorrupt, id)
				break
			}
			seen[id] = struct{}{}
		}
	}
	if err == nil {
		err = engineParameters(h, spec.Parameters)
	}
	if err != nil {
		engineFree(h)
		return nil, err
	}
	return newIndex(spec, h, ids), nil
}

// Spec is the specification the index was built with, defaults filled in.
func (i *Index) Spec() IndexSpec {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.spec
}

// Model is the embedding model the index holds vectors of.
func (i *Index) Model() string { return i.Spec().Model }

// Dim is the dimension of every vector in the index.
func (i *Index) Dim() int { return i.Spec().Dim }

// Len is how many vectors the index holds. It is 0 after Close.
func (i *Index) Len() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.ids)
}

// Contains reports whether a vector is stored under the id.
func (i *Index) Contains(id int64) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	_, ok := i.ids[id]
	return ok
}

// Provenance describes the index as a search would record it now.
func (i *Index) Provenance() Provenance {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.provenanceLocked()
}

func (i *Index) provenanceLocked() Provenance {
	return Provenance{
		Engine:        "faiss",
		EngineVersion: engineVersion(),
		Model:         i.spec.Model,
		Dim:           i.spec.Dim,
		Metric:        i.spec.Metric,
		Factory:       i.spec.Factory,
		Parameters:    i.spec.Parameters,
		Method:        i.spec.Method(),
		Size:          len(i.ids),
	}
}

// Add stores each vector under the id at the same position.
//
// The batch is checked whole before FAISS sees any of it: every id is zero or
// more and not already stored, no id appears twice, and every vector has the
// index's dimension and only finite values -- and, under MetricCosine, is not
// the zero vector. One bad element refuses the batch and stores nothing.
// Vectors are copied, and normalized under MetricCosine; the caller's slices
// are never modified.
//
// An index whose factory string needs training (IVF, PQ) is trained on the
// first batch added to it. That batch has to be large and representative, so
// such an index is built with every vector at once.
func (i *Index) Add(ids []int64, vectors [][]float32) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return ErrClosed
	}
	if len(ids) != len(vectors) {
		return fmt.Errorf("faiss: Add was given %d ids and %d vectors", len(ids), len(vectors))
	}
	if len(ids) == 0 {
		return nil
	}
	batch := make(map[int64]struct{}, len(ids))
	for n, id := range ids {
		if id < 0 {
			return fmt.Errorf("%w: ids[%d] is %d, and ids are zero or more", ErrInvalidID, n, id)
		}
		if _, stored := i.ids[id]; stored {
			return fmt.Errorf("%w: ids[%d] is %d", ErrIDExists, n, id)
		}
		if _, twice := batch[id]; twice {
			return fmt.Errorf("%w: ids[%d] is %d, which appears earlier in the batch", ErrIDExists, n, id)
		}
		batch[id] = struct{}{}
	}
	flat := make([]float32, 0, len(vectors)*i.spec.Dim)
	for n, vector := range vectors {
		var err error
		if flat, err = appendVector(flat, vector, i.spec); err != nil {
			return fmt.Errorf("vectors[%d]: %w", n, err)
		}
	}
	if !engineTrained(i.h) {
		if err := engineTrain(i.h, len(ids), flat); err != nil {
			return err
		}
	}
	if err := engineAdd(i.h, append([]int64(nil), ids...), flat); err != nil {
		return err
	}
	for _, id := range ids {
		i.ids[id] = struct{}{}
	}
	return nil
}

// Remove deletes the vectors stored under the ids, and reports how many were
// there. An id that is not stored is not an error: removing what is absent
// leaves the index as the caller wants it, which is what an erasure request
// needs.
//
// The exhaustive "Flat" index removes in place. Every other structure is
// rebuilt without the removed vectors, from the vectors it stores: HNSW cannot
// remove from its graph at all, and an inverted-file index under FAISS's IDMap2
// would remove the wrong rows, because IDMap2 renumbers its ids on the
// assumption that the index below renumbers too, and IVF does not. The rebuild
// is exact for structures over full vectors ("HNSW32", "IVF1024,Flat") and
// lossy for compressed ones (PQ, SQ), which the application rebuilds from its
// own templates instead. It costs a pass over the index, which is what an
// erasure is worth.
func (i *Index) Remove(ids []int64) (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return 0, ErrClosed
	}
	present := make([]int64, 0, len(ids))
	drop := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, stored := i.ids[id]; !stored {
			continue
		}
		if _, twice := drop[id]; twice {
			continue
		}
		drop[id] = struct{}{}
		present = append(present, id)
	}
	if len(present) == 0 {
		return 0, nil
	}
	if i.spec.Factory == DefaultFactory {
		removed, err := engineRemove(i.h, present)
		if err != nil {
			return 0, err
		}
		if removed != len(present) {
			return 0, fmt.Errorf("%w: FAISS removed %d vectors of the %d asked for", ErrEngine, removed, len(present))
		}
	} else if err := i.rebuildWithoutLocked(drop); err != nil {
		return 0, err
	}
	for id := range drop {
		delete(i.ids, id)
	}
	return len(present), nil
}

// rebuildWithoutLocked replaces the FAISS index with one that holds every
// vector but the dropped ones, read back from the index itself. The old index
// is kept until the new one is complete, so a failure changes nothing.
func (i *Index) rebuildWithoutLocked(drop map[int64]struct{}) error {
	kept := make([]int64, 0, len(i.ids))
	for _, id := range engineIDs(i.h) {
		if _, dropped := drop[id]; !dropped {
			kept = append(kept, id)
		}
	}
	h, err := engineBuild(i.spec)
	if err != nil {
		return err
	}
	if len(kept) > 0 {
		flat := make([]float32, len(kept)*i.spec.Dim)
		for n, id := range kept {
			if err := engineReconstruct(i.h, id, flat[n*i.spec.Dim:(n+1)*i.spec.Dim]); err != nil {
				engineFree(h)
				return fmt.Errorf("this index can neither remove in place nor read its vectors back to rebuild without them; rebuild it from the templates: %w", err)
			}
		}
		if !engineTrained(h) {
			if err := engineTrain(h, len(kept), flat); err != nil {
				engineFree(h)
				return err
			}
		}
		if err := engineAdd(h, kept, flat); err != nil {
			engineFree(h)
			return err
		}
	}
	engineFree(i.h)
	i.h = h
	return nil
}

// Search returns up to k stored vectors nearest the query, closest first. An
// empty index answers no candidate and no error; an index holding fewer than k
// vectors answers all of them.
//
// The query is checked as Add checks a vector, and normalized under
// MetricCosine on a copy.
func (i *Index) Search(query []float32, k int) ([]Candidate, error) {
	candidates, _, err := i.search(query, k)
	return candidates, err
}

// search is Search, with the provenance read under the same lock as the
// candidates, so the size it reports is the size that was searched.
func (i *Index) search(query []float32, k int) ([]Candidate, Provenance, error) {
	if k < 1 || k > MaxK {
		return nil, Provenance{}, fmt.Errorf("%w: k is %d, and has to be between 1 and %d", ErrInvalidK, k, MaxK)
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.closed {
		return nil, Provenance{}, ErrClosed
	}
	q, err := appendVector(make([]float32, 0, i.spec.Dim), query, i.spec)
	if err != nil {
		return nil, Provenance{}, fmt.Errorf("query: %w", err)
	}
	provenance := i.provenanceLocked()
	if len(i.ids) == 0 {
		return []Candidate{}, provenance, nil
	}
	k = min(k, len(i.ids))
	scores, labels, err := engineSearch(i.h, q, k)
	if err != nil {
		return nil, Provenance{}, err
	}
	out := make([]Candidate, 0, k)
	for n, label := range labels {
		if label < 0 {
			continue
		}
		out = append(out, Candidate{ID: label, Score: scores[n]})
	}
	return out, provenance, nil
}

// Serialize writes the index to bytes: FAISS's own format inside an envelope
// that records the specification, the count and a SHA-256 of the FAISS bytes.
// Deserialize reads it back. The bytes hold every vector the index stores; for
// face embeddings that is biometric data, and the application seals them
// before they leave the process.
func (i *Index) Serialize() ([]byte, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.closed {
		return nil, ErrClosed
	}
	payload, err := engineSerialize(i.h)
	if err != nil {
		return nil, err
	}
	return encodeEnvelope(envelopeHeader{
		Model:   i.spec.Model,
		Dim:     i.spec.Dim,
		Metric:  i.spec.Metric,
		Factory: i.spec.Factory,
		Count:   len(i.ids),
	}, payload), nil
}

// Close frees the FAISS index. It waits for the operations in flight, is
// idempotent, and leaves an index on which every operation answers ErrClosed.
func (i *Index) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return nil
	}
	i.closed = true
	engineFree(i.h)
	i.h = handle{}
	i.ids = map[int64]struct{}{}
	runtime.SetFinalizer(i, nil)
	return nil
}

// appendVector checks one vector against the specification and appends it to
// dst, normalized under MetricCosine.
func appendVector(dst, vector []float32, spec IndexSpec) ([]float32, error) {
	if len(vector) != spec.Dim {
		return dst, fmt.Errorf("%w: the vector has %d values and the index of model %q holds %d",
			ErrDimension, len(vector), spec.Model, spec.Dim)
	}
	var sum float64
	for n, v := range vector {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return dst, fmt.Errorf("%w: value %d is %v", ErrInvalidVector, n, v)
		}
		sum += f * f
	}
	if spec.Metric != MetricCosine {
		return append(dst, vector...), nil
	}
	norm := math.Sqrt(sum)
	if norm == 0 || math.IsInf(norm, 0) {
		return dst, fmt.Errorf("%w: under the cosine metric a vector needs a direction, and this one has norm %v", ErrInvalidVector, norm)
	}
	for _, v := range vector {
		dst = append(dst, float32(float64(v)/norm))
	}
	return dst, nil
}
