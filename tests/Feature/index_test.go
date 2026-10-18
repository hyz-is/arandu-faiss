//go:build cgo && !nofaiss

package feature_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	faiss "github.com/hyz-is/arandu-faiss"
)

// TestTheEngineIsLinked is the line that says the rest of this file ran
// against a real FAISS.
func TestTheEngineIsLinked(t *testing.T) {
	t.Parallel()

	if err := faiss.Available(); err != nil {
		t.Fatalf("Available() = %v in a cgo build", err)
	}
	if v := faiss.EngineVersion(); v == "" {
		t.Fatal("EngineVersion() is empty in a cgo build")
	} else {
		t.Logf("FAISS %s", v)
	}
}

// TestAnExhaustiveSearchIsTheBruteForceAnswer holds the default index to the
// answer computed without FAISS, under each metric: the same ids, in the same
// order, with the same scores.
func TestAnExhaustiveSearchIsTheBruteForceAnswer(t *testing.T) {
	t.Parallel()

	for _, metric := range []faiss.Metric{faiss.MetricCosine, faiss.MetricInnerProduct, faiss.MetricL2} {
		t.Run(string(metric), func(t *testing.T) {
			t.Parallel()

			idx := newIndex(t, faiss.IndexSpec{Model: "sface_2021dec", Dim: sfaceDim, Metric: metric})
			ids := randomIDs(1, 600)
			vectors := randomVectors(2, 600, sfaceDim)
			if err := idx.Add(ids, vectors); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if idx.Len() != 600 {
				t.Fatalf("Len() = %d, want 600", idx.Len())
			}
			for q, query := range randomVectors(3, 25, sfaceDim) {
				got, err := idx.Search(query, 10)
				if err != nil {
					t.Fatalf("Search: %v", err)
				}
				sameCandidates(t, string(metric)+" query "+string(rune('a'+q)), got, bruteForce(metric, ids, vectors, query, 10))
			}
		})
	}
}

// TestAStoredVectorFindsItselfFirst is the property duplicate detection rests
// on: under cosine, a query equal to a stored vector answers that vector first,
// with a score of 1.
func TestAStoredVectorFindsItselfFirst(t *testing.T) {
	t.Parallel()

	idx := newIndex(t, faiss.IndexSpec{Model: "sface_2021dec", Dim: sfaceDim})
	ids := randomIDs(4, 200)
	vectors := randomVectors(5, 200, sfaceDim)
	if err := idx.Add(ids, vectors); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for i := range ids {
		got, err := idx.Search(vectors[i], 3)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if got[0].ID != ids[i] || math.Abs(float64(got[0].Score)-1) > 1e-5 {
			t.Fatalf("searching stored vector %d answered %+v first, want id %d with score 1", i, got[0], ids[i])
		}
	}
}

// TestASearchAsksForNoMoreThanTheIndexHolds covers the small cases: an empty
// index answers nothing, and a k above the size answers every vector once.
func TestASearchAsksForNoMoreThanTheIndexHolds(t *testing.T) {
	t.Parallel()

	idx := newIndex(t, faiss.IndexSpec{Model: "m", Dim: 8})
	query := randomVectors(6, 1, 8)[0]
	got, err := idx.Search(query, 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("Search on an empty index = %v, %v; want no candidate and no error", got, err)
	}
	if err := idx.Add([]int64{10, 20, 30}, randomVectors(7, 3, 8)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err = idx.Search(query, 100)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	ids := candidateIDs(got)
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{10, 20, 30}) {
		t.Fatalf("Search(k=100) on three vectors answered ids %v, want 10, 20, 30", ids)
	}
}

// TestRemovedVectorsAreNeverAnsweredAgain is the erasure property: after
// Remove, no search returns the id, whatever the query.
func TestRemovedVectorsAreNeverAnsweredAgain(t *testing.T) {
	t.Parallel()

	for _, factory := range []string{"Flat", "IVF4,Flat", "HNSW32"} {
		t.Run(factory, func(t *testing.T) {
			t.Parallel()

			spec := faiss.IndexSpec{Model: "sface_2021dec", Dim: sfaceDim, Factory: factory}
			if factory == "IVF4,Flat" {
				spec.Parameters = "nprobe=4"
			}
			if factory == "HNSW32" {
				spec.Parameters = "efSearch=128"
			}
			idx := newIndex(t, spec)
			ids := randomIDs(8, 400)
			vectors := randomVectors(9, 400, sfaceDim)
			if err := idx.Add(ids, vectors); err != nil {
				t.Fatalf("Add: %v", err)
			}

			erased := []int64{ids[0], ids[17], ids[399], ids[200]}
			removed, err := idx.Remove(append(erased, ids[17], 1<<50))
			if err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if removed != len(erased) {
				t.Fatalf("Remove reported %d removed, want %d: an absent or repeated id counts for nothing", removed, len(erased))
			}
			if idx.Len() != 400-len(erased) {
				t.Fatalf("Len() = %d after removing %d of 400", idx.Len(), len(erased))
			}
			for _, id := range erased {
				if idx.Contains(id) {
					t.Fatalf("Contains(%d) after Remove", id)
				}
			}
			// The erased vectors themselves are the queries most likely to find
			// them again.
			for _, position := range []int{0, 17, 399, 200} {
				got, err := idx.Search(vectors[position], 20)
				if err != nil {
					t.Fatalf("Search: %v", err)
				}
				for _, id := range erased {
					if containsID(got, id) {
						t.Fatalf("Search answered the removed id %d", id)
					}
				}
			}
			// What was kept is still found.
			got, err := idx.Search(vectors[1], 1)
			if err != nil || len(got) != 1 || got[0].ID != ids[1] {
				t.Fatalf("Search for a kept vector = %v, %v; want id %d first", got, err, ids[1])
			}
			if again, err := idx.Remove(erased); err != nil || again != 0 {
				t.Fatalf("removing the same ids again = %d, %v; want 0, nil", again, err)
			}
		})
	}
}

// TestAnIVFIndexIsTrainedOnItsFirstBatch holds the IVF path: trained on the
// vectors added first, and, searched across every list, exact.
func TestAnIVFIndexIsTrainedOnItsFirstBatch(t *testing.T) {
	t.Parallel()

	idx := newIndex(t, faiss.IndexSpec{Model: "sface_2021dec", Dim: sfaceDim, Factory: "IVF4,Flat", Parameters: "nprobe=4"})
	ids := randomIDs(10, 500)
	vectors := randomVectors(11, 500, sfaceDim)
	if err := idx.Add(ids, vectors); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := idx.Provenance().Method; got != faiss.MethodApproximate {
		t.Fatalf("Provenance().Method = %q for IVF, want %q", got, faiss.MethodApproximate)
	}
	for _, query := range randomVectors(12, 10, sfaceDim) {
		got, err := idx.Search(query, 5)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		sameCandidates(t, "IVF with every list probed", got, bruteForce(faiss.MetricCosine, ids, vectors, query, 5))
	}
}

// TestASerializedIndexReadsBackAsTheSameIndex is the persistence round trip:
// the same size, the same ids, the same answers, and an index that keeps
// working after it is read.
func TestASerializedIndexReadsBackAsTheSameIndex(t *testing.T) {
	t.Parallel()

	for _, spec := range []faiss.IndexSpec{
		{Model: "sface_2021dec", Dim: sfaceDim},
		{Model: "sface_2021dec", Dim: sfaceDim, Metric: faiss.MetricL2},
		{Model: "sface_2021dec", Dim: sfaceDim, Factory: "HNSW32", Parameters: "efSearch=64"},
		{Model: "sface_2021dec", Dim: sfaceDim, Factory: "IVF4,Flat", Parameters: "nprobe=4"},
	} {
		t.Run(spec.Factory+string(spec.Metric), func(t *testing.T) {
			t.Parallel()

			idx := newIndex(t, spec)
			ids := randomIDs(13, 300)
			vectors := randomVectors(14, 300, sfaceDim)
			if err := idx.Add(ids, vectors); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if _, err := idx.Remove(ids[:10]); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			blob, err := idx.Serialize()
			if err != nil {
				t.Fatalf("Serialize: %v", err)
			}

			read, err := faiss.Deserialize(blob, spec)
			if err != nil {
				t.Fatalf("Deserialize: %v", err)
			}
			t.Cleanup(func() { _ = read.Close() })
			if read.Len() != idx.Len() {
				t.Fatalf("read Len() = %d, want %d", read.Len(), idx.Len())
			}
			for _, id := range ids[:10] {
				if read.Contains(id) {
					t.Fatalf("the read index holds %d, which was removed before Serialize", id)
				}
			}
			if read.Spec() != idx.Spec() {
				t.Fatalf("read Spec() = %+v, want %+v", read.Spec(), idx.Spec())
			}
			for _, query := range randomVectors(15, 10, sfaceDim) {
				want, err := idx.Search(query, 8)
				if err != nil {
					t.Fatalf("Search: %v", err)
				}
				got, err := read.Search(query, 8)
				if err != nil {
					t.Fatalf("Search on the read index: %v", err)
				}
				sameCandidates(t, "read index", got, want)
			}
			if err := read.Add(ids[:1], vectors[:1]); err != nil {
				t.Fatalf("Add to the read index of an id removed before Serialize: %v", err)
			}
			if err := read.Add(ids[20:21], vectors[20:21]); !errors.Is(err, faiss.ErrIDExists) {
				t.Fatalf("Add of an id the read index holds = %v, want ErrIDExists", err)
			}
			if removed, err := read.Remove(ids[30:31]); err != nil || removed != 1 || read.Contains(ids[30]) {
				t.Fatalf("Remove on the read index = %d, %v; want the id gone", removed, err)
			}
		})
	}
}

// TestAnIndexIsNeverReadAsAnotherSpecification holds the model and dimension
// check on load: an index of one model is not an index of another, whatever
// their dimensions.
func TestAnIndexIsNeverReadAsAnotherSpecification(t *testing.T) {
	t.Parallel()

	spec := faiss.IndexSpec{Model: "sface_2021dec", Dim: sfaceDim}
	idx := newIndex(t, spec)
	if err := idx.Add(randomIDs(16, 20), randomVectors(17, 20, sfaceDim)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	blob, err := idx.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	for name, other := range map[string]faiss.IndexSpec{
		"another model":     {Model: "arcface_r100", Dim: sfaceDim},
		"another dimension": {Model: "sface_2021dec", Dim: 512},
		"another metric":    {Model: "sface_2021dec", Dim: sfaceDim, Metric: faiss.MetricL2},
		"another factory":   {Model: "sface_2021dec", Dim: sfaceDim, Factory: "HNSW32"},
	} {
		if read, err := faiss.Deserialize(blob, other); !errors.Is(err, faiss.ErrSpecMismatch) {
			if read != nil {
				_ = read.Close()
			}
			t.Errorf("%s: Deserialize = %v, want ErrSpecMismatch", name, err)
		}
	}
}

// TestACorruptIndexIsRefusedBeforeFAISSReadsIt holds the envelope: a flipped
// byte, a truncation or bytes that were never an index are refused with
// ErrCorrupt.
func TestACorruptIndexIsRefusedBeforeFAISSReadsIt(t *testing.T) {
	t.Parallel()

	spec := faiss.IndexSpec{Model: "sface_2021dec", Dim: sfaceDim}
	idx := newIndex(t, spec)
	if err := idx.Add(randomIDs(18, 50), randomVectors(19, 50, sfaceDim)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	blob, err := idx.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	flipped := slices.Clone(blob)
	flipped[len(flipped)-100] ^= 0x01
	for name, bad := range map[string][]byte{
		"a flipped payload byte": flipped,
		"truncated":              blob[:len(blob)-1],
		"only the header":        blob[:40],
		"longer than written":    append(slices.Clone(blob), 0),
		"empty":                  nil,
		"not an index":           []byte(strings.Repeat("x", 200)),
	} {
		if read, err := faiss.Deserialize(bad, spec); !errors.Is(err, faiss.ErrCorrupt) {
			if read != nil {
				_ = read.Close()
			}
			t.Errorf("%s: Deserialize = %v, want ErrCorrupt", name, err)
		}
	}
}

// TestTheIndexRefusesWhatItCannotHold covers every refusal of Add and Search,
// and that a refused batch stores nothing of itself.
func TestTheIndexRefusesWhatItCannotHold(t *testing.T) {
	t.Parallel()

	idx := newIndex(t, faiss.IndexSpec{Model: "sface_2021dec", Dim: 4})
	good := [][]float32{{1, 0, 0, 0}, {0, 1, 0, 0}}
	if err := idx.Add([]int64{1, 2}, good); err != nil {
		t.Fatalf("Add: %v", err)
	}

	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	for name, c := range map[string]struct {
		ids     []int64
		vectors [][]float32
		want    error
	}{
		"a short vector":           {[]int64{3}, [][]float32{{1, 2, 3}}, faiss.ErrDimension},
		"a long vector":            {[]int64{3}, [][]float32{{1, 2, 3, 4, 5}}, faiss.ErrDimension},
		"a NaN":                    {[]int64{3}, [][]float32{{1, nan, 0, 0}}, faiss.ErrInvalidVector},
		"an infinity":              {[]int64{3}, [][]float32{{1, inf, 0, 0}}, faiss.ErrInvalidVector},
		"the zero vector":          {[]int64{3}, [][]float32{{0, 0, 0, 0}}, faiss.ErrInvalidVector},
		"a negative id":            {[]int64{-1}, [][]float32{{1, 1, 0, 0}}, faiss.ErrInvalidID},
		"an id already stored":     {[]int64{2}, [][]float32{{1, 1, 0, 0}}, faiss.ErrIDExists},
		"an id twice in the batch": {[]int64{5, 5}, [][]float32{{1, 1, 0, 0}, {1, 0, 1, 0}}, faiss.ErrIDExists},
		"a bad vector after a good one": {[]int64{6, 7},
			[][]float32{{1, 1, 1, 0}, {1, 2}}, faiss.ErrDimension},
	} {
		if err := idx.Add(c.ids, c.vectors); !errors.Is(err, c.want) {
			t.Errorf("%s: Add = %v, want %v", name, err, c.want)
		}
	}
	if err := idx.Add([]int64{8, 9}, good[:1]); err == nil {
		t.Error("Add with two ids and one vector was accepted")
	}
	if idx.Len() != 2 || idx.Contains(6) {
		t.Fatalf("Len() = %d and Contains(6) = %v after refused batches; a refused batch stores nothing",
			idx.Len(), idx.Contains(6))
	}

	for name, c := range map[string]struct {
		query []float32
		k     int
		want  error
	}{
		"a short query":    {[]float32{1, 0, 0}, 1, faiss.ErrDimension},
		"a NaN query":      {[]float32{nan, 0, 0, 0}, 1, faiss.ErrInvalidVector},
		"the zero query":   {[]float32{0, 0, 0, 0}, 1, faiss.ErrInvalidVector},
		"k of zero":        {[]float32{1, 0, 0, 0}, 0, faiss.ErrInvalidK},
		"k above the cap":  {[]float32{1, 0, 0, 0}, faiss.MaxK + 1, faiss.ErrInvalidK},
		"a negative k":     {[]float32{1, 0, 0, 0}, -3, faiss.ErrInvalidK},
		"a query too long": {[]float32{1, 0, 0, 0, 0}, 1, faiss.ErrDimension},
	} {
		if _, err := idx.Search(c.query, c.k); !errors.Is(err, c.want) {
			t.Errorf("%s: Search = %v, want %v", name, err, c.want)
		}
	}

	// The zero vector has no direction under cosine and is a point like any
	// other under L2.
	l2 := newIndex(t, faiss.IndexSpec{Model: "m", Dim: 4, Metric: faiss.MetricL2})
	if err := l2.Add([]int64{1}, [][]float32{{0, 0, 0, 0}}); err != nil {
		t.Fatalf("Add of the zero vector under L2: %v", err)
	}
}

// TestTheCallersSlicesAreNeverModified holds that normalization works on a
// copy: the application's template, decrypted for the call, is left as it was.
func TestTheCallersSlicesAreNeverModified(t *testing.T) {
	t.Parallel()

	idx := newIndex(t, faiss.IndexSpec{Model: "m", Dim: 3})
	vector := []float32{3, 4, 12}
	ids := []int64{1}
	if err := idx.Add(ids, [][]float32{vector}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	query := []float32{6, 8, 24}
	got, err := idx.Search(query, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !slices.Equal(vector, []float32{3, 4, 12}) || !slices.Equal(query, []float32{6, 8, 24}) || ids[0] != 1 {
		t.Fatalf("the caller's slices changed: vector %v, query %v, ids %v", vector, query, ids)
	}
	if math.Abs(float64(got[0].Score)-1) > 1e-6 {
		t.Fatalf("two vectors in the same direction scored %v under cosine, want 1", got[0].Score)
	}
}

// TestAClosedIndexAnswersErrClosed holds Close: idempotent, and final.
func TestAClosedIndexAnswersErrClosed(t *testing.T) {
	t.Parallel()

	idx, err := faiss.NewIndex(faiss.IndexSpec{Model: "m", Dim: 2})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	if err := idx.Add([]int64{1}, [][]float32{{1, 0}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("a second Close = %v, want nil", err)
	}
	if err := idx.Add([]int64{2}, [][]float32{{0, 1}}); !errors.Is(err, faiss.ErrClosed) {
		t.Errorf("Add after Close = %v, want ErrClosed", err)
	}
	if _, err := idx.Remove([]int64{1}); !errors.Is(err, faiss.ErrClosed) {
		t.Errorf("Remove after Close = %v, want ErrClosed", err)
	}
	if _, err := idx.Search([]float32{1, 0}, 1); !errors.Is(err, faiss.ErrClosed) {
		t.Errorf("Search after Close = %v, want ErrClosed", err)
	}
	if _, err := idx.Serialize(); !errors.Is(err, faiss.ErrClosed) {
		t.Errorf("Serialize after Close = %v, want ErrClosed", err)
	}
	if idx.Len() != 0 {
		t.Errorf("Len() after Close = %d, want 0", idx.Len())
	}
}

// TestFAISSRefusalsArriveAsErrEngine holds the boundary with FAISS: a factory
// string or a parameter it does not know is its refusal, with its message.
func TestFAISSRefusalsArriveAsErrEngine(t *testing.T) {
	t.Parallel()

	for name, spec := range map[string]faiss.IndexSpec{
		"an unknown factory string": {Model: "m", Dim: 8, Factory: "Bogus42"},
		"an unknown parameter":      {Model: "m", Dim: 8, Factory: "HNSW32", Parameters: "noSuchKnob=3"},
	} {
		idx, err := faiss.NewIndex(spec)
		if !errors.Is(err, faiss.ErrEngine) {
			if idx != nil {
				_ = idx.Close()
			}
			t.Errorf("%s: NewIndex = %v, want ErrEngine", name, err)
			continue
		}
		t.Logf("%s: %v", name, err)
	}
}

// TestProvenanceSaysWhatSearched holds what the application records beside
// the candidates.
func TestProvenanceSaysWhatSearched(t *testing.T) {
	t.Parallel()

	idx := newIndex(t, faiss.IndexSpec{Model: "sface_2021dec", Dim: sfaceDim})
	if err := idx.Add(randomIDs(20, 7), randomVectors(21, 7, sfaceDim)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	want := faiss.Provenance{
		Engine:        "faiss",
		EngineVersion: faiss.EngineVersion(),
		Model:         "sface_2021dec",
		Dim:           sfaceDim,
		Metric:        faiss.MetricCosine,
		Factory:       faiss.DefaultFactory,
		Method:        faiss.MethodExhaustive,
		Size:          7,
	}
	if got := idx.Provenance(); got != want {
		t.Fatalf("Provenance() = %+v, want %+v", got, want)
	}
}
