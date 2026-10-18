//go:build cgo && !nofaiss

package feature_test

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"

	faiss "github.com/hyz-is/arandu-faiss"
)

// Every vector in these tests is synthetic: drawn from a seeded generator, so
// a failure reproduces, and standing for nobody.

// sfaceDim is the dimension of an SFace embedding, which is what the module
// was built to index.
const sfaceDim = 128

// randomVectors draws n vectors of dim values in [-1, 1).
func randomVectors(seed uint64, n, dim int) [][]float32 {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dim)
		for j := range v {
			v[j] = float32(r.Float64()*2 - 1)
		}
		out[i] = v
	}
	return out
}

// randomIDs draws n distinct non-negative ids, deliberately not 0..n-1, so a
// test that confused positions with ids fails.
func randomIDs(seed uint64, n int) []int64 {
	r := rand.New(rand.NewPCG(seed, 7))
	seen := map[int64]bool{}
	out := make([]int64, 0, n)
	for len(out) < n {
		id := r.Int64N(1 << 40)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// bruteForce is the answer an exhaustive search must give, computed in float64
// without FAISS: every stored vector scored against the query under the
// metric, closest first.
func bruteForce(metric faiss.Metric, ids []int64, vectors [][]float32, query []float32, k int) []faiss.Candidate {
	type scored struct {
		id    int64
		score float64
	}
	all := make([]scored, len(ids))
	for i, v := range vectors {
		all[i] = scored{ids[i], score(metric, v, query)}
	}
	lowerIsCloser := metric == faiss.MetricL2
	sort.Slice(all, func(a, b int) bool {
		if lowerIsCloser {
			return all[a].score < all[b].score
		}
		return all[a].score > all[b].score
	})
	out := make([]faiss.Candidate, 0, k)
	for _, s := range all[:min(k, len(all))] {
		out = append(out, faiss.Candidate{ID: s.id, Score: float32(s.score)})
	}
	return out
}

func score(metric faiss.Metric, a, b []float32) float64 {
	var dot, na, nb, l2 float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
		l2 += (x - y) * (x - y)
	}
	switch metric {
	case faiss.MetricL2:
		return l2
	case faiss.MetricInnerProduct:
		return dot
	default:
		return dot / (math.Sqrt(na) * math.Sqrt(nb))
	}
}

// sameCandidates fails unless got is want: the same ids in the same order,
// and scores within a float32 tolerance.
func sameCandidates(t *testing.T, label string, got, want []faiss.Candidate) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d candidates, want %d", label, len(got), len(want))
	}
	for i := range want {
		tolerance := 1e-4 * math.Max(1, math.Abs(float64(want[i].Score)))
		if got[i].ID != want[i].ID || math.Abs(float64(got[i].Score-want[i].Score)) > tolerance {
			t.Fatalf("%s: candidate %d is %+v, want %+v", label, i, got[i], want[i])
		}
	}
}

// newIndex builds an index and closes it when the test ends.
func newIndex(t *testing.T, spec faiss.IndexSpec) *faiss.Index {
	t.Helper()
	idx, err := faiss.NewIndex(spec)
	if err != nil {
		t.Fatalf("NewIndex(%+v): %v", spec, err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func candidateIDs(candidates []faiss.Candidate) []int64 {
	out := make([]int64, len(candidates))
	for i, c := range candidates {
		out[i] = c.ID
	}
	return out
}

func containsID(candidates []faiss.Candidate, id int64) bool {
	return slices.Contains(candidateIDs(candidates), id)
}
