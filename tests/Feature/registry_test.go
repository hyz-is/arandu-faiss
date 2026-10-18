//go:build cgo && !nofaiss

package feature_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/arandu-io/framework/security"

	faiss "github.com/hyz-is/arandu-faiss"
)

// The action the application's policy authorized. The registry does not read
// it: it reads the tenant off the Grant and nothing else.
const screen security.Action = "identity.screen"

const model = "sface_2021dec"

func sfaceConfig() faiss.Config {
	return faiss.Config{Models: []faiss.IndexSpec{{Model: model, Dim: sfaceDim}}}
}

func grantFor(tenant string) security.Grant { return security.SystemGrant(screen, tenant) }

func newRegistry(t *testing.T, cfg faiss.Config, store faiss.Store) *faiss.Registry {
	t.Helper()
	r, err := faiss.NewRegistry(cfg, store)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// recordingStore is a MemoryStore that counts its calls and can be told to
// fail the next Save.
type recordingStore struct {
	*faiss.MemoryStore
	loads, saves atomic.Int64
	failSave     atomic.Bool
}

func newRecordingStore() *recordingStore { return &recordingStore{MemoryStore: faiss.NewMemoryStore()} }

func (s *recordingStore) Load(ctx context.Context, key faiss.Key) ([]byte, error) {
	s.loads.Add(1)
	return s.MemoryStore.Load(ctx, key)
}

var errStoreDown = errors.New("store unavailable")

func (s *recordingStore) Save(ctx context.Context, key faiss.Key, blob []byte) error {
	if s.failSave.Load() {
		return errStoreDown
	}
	s.saves.Add(1)
	return s.MemoryStore.Save(ctx, key, blob)
}

// TestTenantsNeverSeeEachOthersVectors is the isolation property: each
// tenant's search answers its own vectors and nobody else's, even for a query
// that is exactly another tenant's stored vector.
func TestTenantsNeverSeeEachOthersVectors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := faiss.NewMemoryStore()
	r := newRegistry(t, sfaceConfig(), store)
	alpha, beta := grantFor("tenant-alpha"), grantFor("tenant-beta")

	alphaIDs, alphaVectors := randomIDs(40, 50), randomVectors(41, 50, sfaceDim)
	betaIDs, betaVectors := randomIDs(42, 50), randomVectors(43, 50, sfaceDim)
	if err := r.Add(ctx, alpha, model, alphaIDs, alphaVectors); err != nil {
		t.Fatalf("Add for alpha: %v", err)
	}

	got, err := r.Search(ctx, beta, model, alphaVectors[0], 10)
	if err != nil {
		t.Fatalf("Search for beta: %v", err)
	}
	if len(got.Candidates) != 0 || got.Provenance.Size != 0 {
		t.Fatalf("beta, with nothing indexed, searched alpha's vector and got %+v", got)
	}

	if err := r.Add(ctx, beta, model, betaIDs, betaVectors); err != nil {
		t.Fatalf("Add for beta: %v", err)
	}
	isAlpha := map[int64]bool{}
	for _, id := range alphaIDs {
		isAlpha[id] = true
	}
	for i := range betaVectors {
		got, err := r.Search(ctx, alpha, model, betaVectors[i], 50)
		if err != nil {
			t.Fatalf("Search for alpha: %v", err)
		}
		for _, c := range got.Candidates {
			if !isAlpha[c.ID] {
				t.Fatalf("alpha's search answered id %d, which is not alpha's", c.ID)
			}
		}
	}
	got, err = r.Search(ctx, beta, model, betaVectors[3], 1)
	if err != nil || len(got.Candidates) != 1 || got.Candidates[0].ID != betaIDs[3] {
		t.Fatalf("beta's own vector = %+v, %v; want id %d", got, err, betaIDs[3])
	}

	keys := store.Keys()
	if len(keys) != 2 {
		t.Fatalf("the store holds %d keys, want one per tenant: %v", len(keys), keys)
	}
	for _, key := range keys {
		if key.Model != model || (key.Tenant != "tenant-alpha" && key.Tenant != "tenant-beta") {
			t.Fatalf("the store holds the key %+v", key)
		}
	}
}

// TestEveryWriteIsInTheStoreWhenItReturns holds the write-through promise: a
// second registry over the same store, as another process would be, sees each
// change -- including an erasure.
func TestEveryWriteIsInTheStoreWhenItReturns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := faiss.NewMemoryStore()
	tenant := grantFor("tenant-gamma")
	ids, vectors := randomIDs(44, 30), randomVectors(45, 30, sfaceDim)

	first := newRegistry(t, sfaceConfig(), store)
	if err := first.Add(ctx, tenant, model, ids, vectors); err != nil {
		t.Fatalf("Add: %v", err)
	}

	second := newRegistry(t, sfaceConfig(), store)
	if n, err := second.Len(ctx, tenant, model); err != nil || n != 30 {
		t.Fatalf("a second registry's Len = %d, %v; want 30", n, err)
	}
	removed, err := second.Remove(ctx, tenant, model, ids[:5])
	if err != nil || removed != 5 {
		t.Fatalf("Remove = %d, %v; want 5", removed, err)
	}

	third := newRegistry(t, sfaceConfig(), store)
	got, err := third.Search(ctx, tenant, model, vectors[0], 30)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, id := range ids[:5] {
		if containsID(got.Candidates, id) {
			t.Fatalf("a registry loaded after the erasure answered the erased id %d", id)
		}
	}
	if len(got.Candidates) != 25 {
		t.Fatalf("a registry loaded after the erasure answered %d candidates, want 25", len(got.Candidates))
	}
}

// TestAGrantWithoutATenantReachesNoIndex holds the other half of isolation:
// the zero Grant, and a system grant refused for its empty tenant, belong to
// nobody and reach nothing.
func TestAGrantWithoutATenantReachesNoIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRecordingStore()
	r := newRegistry(t, sfaceConfig(), store)
	vector := randomVectors(46, 1, sfaceDim)[0]

	for name, g := range map[string]security.Grant{
		"the zero grant":                   {},
		"a system grant with no tenant":    security.SystemGrant(screen, ""),
		"a system grant with no tenant II": security.SystemGrant("", ""),
	} {
		if _, err := r.Search(ctx, g, model, vector, 1); !errors.Is(err, faiss.ErrNoTenant) {
			t.Errorf("%s: Search = %v, want ErrNoTenant", name, err)
		}
		if err := r.Add(ctx, g, model, []int64{1}, [][]float32{vector}); !errors.Is(err, faiss.ErrNoTenant) {
			t.Errorf("%s: Add = %v, want ErrNoTenant", name, err)
		}
		if _, err := r.Remove(ctx, g, model, []int64{1}); !errors.Is(err, faiss.ErrNoTenant) {
			t.Errorf("%s: Remove = %v, want ErrNoTenant", name, err)
		}
		if err := r.Rebuild(ctx, g, model, nil, nil); !errors.Is(err, faiss.ErrNoTenant) {
			t.Errorf("%s: Rebuild = %v, want ErrNoTenant", name, err)
		}
		if err := r.Drop(ctx, g, model); !errors.Is(err, faiss.ErrNoTenant) {
			t.Errorf("%s: Drop = %v, want ErrNoTenant", name, err)
		}
		if _, err := r.Len(ctx, g, model); !errors.Is(err, faiss.ErrNoTenant) {
			t.Errorf("%s: Len = %v, want ErrNoTenant", name, err)
		}
	}
	if store.loads.Load() != 0 || store.saves.Load() != 0 || r.Loaded() != 0 {
		t.Fatalf("refused calls reached the store (%d loads, %d saves) or loaded %d indexes",
			store.loads.Load(), store.saves.Load(), r.Loaded())
	}
}

// allowAll is a policy that allows everything, which is what it takes for
// security.Authorize to issue a Grant to an impersonated subject in a test.
type allowAll struct{}

func (allowAll) Can(context.Context, security.Subject, security.Action, struct{}) error { return nil }

// TestAReadOnlyGrantSearchesAndWritesNothing holds impersonation: a subject a
// platform administrator is viewing as can be searched for, and nothing it
// does changes the index.
func TestAReadOnlyGrantSearchesAndWritesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRecordingStore()
	r := newRegistry(t, sfaceConfig(), store)
	ids, vectors := randomIDs(47, 10), randomVectors(48, 10, sfaceDim)
	if err := r.Add(ctx, grantFor("tenant-delta"), model, ids, vectors); err != nil {
		t.Fatalf("Add: %v", err)
	}
	saves := store.saves.Load()

	viewed := security.Subject{ID: "member-1", Tenant: "tenant-delta",
		Impersonator: &security.Subject{ID: "platform-admin", Tenant: "platform"}}
	g, err := security.Authorize(ctx, allowAll{}, viewed, screen, struct{}{})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !g.ReadOnly() {
		t.Fatal("the Grant of an impersonated subject is not read-only; the test does not test what it says")
	}
	if got, err := r.Search(ctx, g, model, vectors[0], 1); err != nil || got.Candidates[0].ID != ids[0] {
		t.Fatalf("Search with a read-only grant = %+v, %v; want id %d", got, err, ids[0])
	}
	if err := r.Add(ctx, g, model, []int64{1}, vectors[:1]); !errors.Is(err, faiss.ErrReadOnly) || !errors.Is(err, security.ErrForbidden) {
		t.Errorf("Add with a read-only grant = %v, want ErrReadOnly and a forbidden", err)
	}
	if _, err := r.Remove(ctx, g, model, ids); !errors.Is(err, faiss.ErrReadOnly) {
		t.Errorf("Remove with a read-only grant = %v, want ErrReadOnly", err)
	}
	if err := r.Rebuild(ctx, g, model, nil, nil); !errors.Is(err, faiss.ErrReadOnly) {
		t.Errorf("Rebuild with a read-only grant = %v, want ErrReadOnly", err)
	}
	if err := r.Drop(ctx, g, model); !errors.Is(err, faiss.ErrReadOnly) {
		t.Errorf("Drop with a read-only grant = %v, want ErrReadOnly", err)
	}
	if store.saves.Load() != saves {
		t.Fatal("a read-only grant saved the index")
	}
}

// TestTheModelAndTheDimensionAreCheckedOnEveryCall holds that a vector reaches
// only an index of the model it was declared for, at its dimension.
func TestTheModelAndTheDimensionAreCheckedOnEveryCall(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRecordingStore()
	cfg := faiss.Config{Models: []faiss.IndexSpec{{Model: model, Dim: sfaceDim}, {Model: "arcface_r100", Dim: 512}}}
	r := newRegistry(t, cfg, store)
	g := grantFor("tenant-epsilon")
	sface := randomVectors(49, 1, sfaceDim)

	if _, err := r.Search(ctx, g, "facenet", sface[0], 1); !errors.Is(err, faiss.ErrUnknownModel) {
		t.Errorf("Search of an unconfigured model = %v, want ErrUnknownModel", err)
	}
	if err := r.Add(ctx, g, "facenet", []int64{1}, sface); !errors.Is(err, faiss.ErrUnknownModel) {
		t.Errorf("Add to an unconfigured model = %v, want ErrUnknownModel", err)
	}
	if _, err := r.Search(ctx, g, "arcface_r100", sface[0], 1); !errors.Is(err, faiss.ErrDimension) {
		t.Errorf("Search of a 512-d model with a 128-d query = %v, want ErrDimension", err)
	}
	if err := r.Add(ctx, g, "arcface_r100", []int64{1}, sface); !errors.Is(err, faiss.ErrDimension) {
		t.Errorf("Add of a 128-d vector to a 512-d model = %v, want ErrDimension", err)
	}
	if store.saves.Load() != 0 {
		t.Fatal("a refused Add saved an index")
	}
	if err := r.Add(ctx, g, model, []int64{1}, sface); err != nil {
		t.Fatalf("Add to the right model: %v", err)
	}
	if n, err := r.Len(ctx, g, "arcface_r100"); err != nil || n != 0 {
		t.Fatalf("Len of the other model = %d, %v; want 0: models never share an index", n, err)
	}
}

// TestAnIndexIsNeverLoadedUnderAChangedSpecification holds what happens when
// the configuration of a model changes under stored indexes: they are refused,
// and Rebuild is how they come back.
func TestAnIndexIsNeverLoadedUnderAChangedSpecification(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := faiss.NewMemoryStore()
	g := grantFor("tenant-zeta")
	ids, vectors := randomIDs(50, 40), randomVectors(51, 40, sfaceDim)

	before := newRegistry(t, sfaceConfig(), store)
	if err := before.Add(ctx, g, model, ids, vectors); err != nil {
		t.Fatalf("Add: %v", err)
	}

	changed := faiss.Config{Models: []faiss.IndexSpec{{Model: model, Dim: sfaceDim, Factory: "HNSW32"}}}
	after := newRegistry(t, changed, store)
	if _, err := after.Search(ctx, g, model, vectors[0], 1); !errors.Is(err, faiss.ErrSpecMismatch) {
		t.Fatalf("Search under a changed factory = %v, want ErrSpecMismatch", err)
	}
	if err := after.Rebuild(ctx, g, model, ids, vectors); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	got, err := after.Search(ctx, g, model, vectors[0], 1)
	if err != nil || got.Candidates[0].ID != ids[0] || got.Provenance.Factory != "HNSW32" {
		t.Fatalf("Search after Rebuild = %+v, %v", got, err)
	}
}

// TestRebuildReplacesTheWholeIndex holds Rebuild: exactly the vectors given,
// in memory and in the store.
func TestRebuildReplacesTheWholeIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := faiss.NewMemoryStore()
	r := newRegistry(t, sfaceConfig(), store)
	g := grantFor("tenant-eta")
	oldIDs, oldVectors := randomIDs(52, 20), randomVectors(53, 20, sfaceDim)
	newIDs, newVectors := randomIDs(54, 15), randomVectors(55, 15, sfaceDim)

	if err := r.Add(ctx, g, model, oldIDs, oldVectors); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Rebuild(ctx, g, model, newIDs, newVectors); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	for _, registry := range []*faiss.Registry{r, newRegistry(t, sfaceConfig(), store)} {
		got, err := registry.Search(ctx, g, model, oldVectors[0], 20)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(got.Candidates) != 15 {
			t.Fatalf("after Rebuild with 15 vectors, Search answered %d", len(got.Candidates))
		}
		for _, id := range oldIDs {
			if containsID(got.Candidates, id) {
				t.Fatalf("after Rebuild, Search answered the old id %d", id)
			}
		}
	}

	// A rebuild that is refused leaves the index as it was.
	if err := r.Rebuild(ctx, g, model, []int64{1, 1}, randomVectors(56, 2, sfaceDim)); !errors.Is(err, faiss.ErrIDExists) {
		t.Fatalf("Rebuild with a repeated id = %v, want ErrIDExists", err)
	}
	if n, err := r.Len(ctx, g, model); err != nil || n != 15 {
		t.Fatalf("Len after a refused Rebuild = %d, %v; want 15", n, err)
	}
}

// TestDropErasesTheTenantsIndex holds Drop: nothing in memory, nothing in the
// store, and an empty index for the next call.
func TestDropErasesTheTenantsIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := faiss.NewMemoryStore()
	r := newRegistry(t, sfaceConfig(), store)
	g, other := grantFor("tenant-theta"), grantFor("tenant-iota")
	if err := r.Add(ctx, g, model, randomIDs(57, 10), randomVectors(58, 10, sfaceDim)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Add(ctx, other, model, randomIDs(59, 10), randomVectors(60, 10, sfaceDim)); err != nil {
		t.Fatalf("Add for the other tenant: %v", err)
	}
	if err := r.Drop(ctx, g, model); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if _, err := store.Load(ctx, faiss.Key{Tenant: "tenant-theta", Model: model}); !errors.Is(err, faiss.ErrNotStored) {
		t.Fatalf("the store still holds the dropped index: %v", err)
	}
	if n, err := r.Len(ctx, g, model); err != nil || n != 0 {
		t.Fatalf("Len after Drop = %d, %v; want 0", n, err)
	}
	if n, err := r.Len(ctx, other, model); err != nil || n != 10 {
		t.Fatalf("the other tenant's Len after Drop = %d, %v; want 10", n, err)
	}
}

// TestAWriteTheStoreRefusedIsNeverAnswered holds the rule that the store is
// the truth: a change whose Save failed is discarded from memory too.
func TestAWriteTheStoreRefusedIsNeverAnswered(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRecordingStore()
	r := newRegistry(t, sfaceConfig(), store)
	g := grantFor("tenant-kappa")
	ids, vectors := randomIDs(61, 10), randomVectors(62, 10, sfaceDim)
	if err := r.Add(ctx, g, model, ids[:5], vectors[:5]); err != nil {
		t.Fatalf("Add: %v", err)
	}

	store.failSave.Store(true)
	if err := r.Add(ctx, g, model, ids[5:], vectors[5:]); !errors.Is(err, errStoreDown) {
		t.Fatalf("Add with the store down = %v, want the store's error", err)
	}
	if _, err := r.Remove(ctx, g, model, ids[:1]); !errors.Is(err, errStoreDown) {
		t.Fatalf("Remove with the store down = %v, want the store's error", err)
	}
	store.failSave.Store(false)

	got, err := r.Search(ctx, g, model, vectors[7], 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Candidates) != 5 || containsID(got.Candidates, ids[7]) || !containsID(got.Candidates, ids[0]) {
		t.Fatalf("after two refused saves, Search answered %v; want the five saved vectors, id %d among them",
			candidateIDs(got.Candidates), ids[0])
	}
}

// TestACorruptStoredIndexIsReportedAndNotKept holds the load path: a blob that
// is not an index is an error, and the next call tries the store again.
func TestACorruptStoredIndexIsReportedAndNotKept(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRecordingStore()
	key := faiss.Key{Tenant: "tenant-lambda", Model: model}
	if err := store.MemoryStore.Save(ctx, key, []byte("not an index")); err != nil {
		t.Fatalf("seeding the store: %v", err)
	}
	r := newRegistry(t, sfaceConfig(), store)
	g := grantFor("tenant-lambda")
	query := randomVectors(63, 1, sfaceDim)[0]
	for range 2 {
		if _, err := r.Search(ctx, g, model, query, 1); !errors.Is(err, faiss.ErrCorrupt) {
			t.Fatalf("Search over a corrupt stored index = %v, want ErrCorrupt", err)
		}
	}
	if store.loads.Load() != 2 || r.Loaded() != 0 {
		t.Fatalf("%d loads and %d indexes loaded; a failed load is retried and never kept", store.loads.Load(), r.Loaded())
	}
}

// TestTheRegistryKeepsAtMostMaxIndexesLoaded holds the memory bound: past it,
// the least recently used index is closed, and loaded again when needed.
func TestTheRegistryKeepsAtMostMaxIndexesLoaded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newRecordingStore()
	cfg := sfaceConfig()
	cfg.MaxIndexes = 2
	r := newRegistry(t, cfg, store)
	tenants := []string{"t-1", "t-2", "t-3", "t-4"}
	vectors := randomVectors(64, len(tenants), sfaceDim)
	for i, tenant := range tenants {
		if err := r.Add(ctx, grantFor(tenant), model, []int64{int64(i)}, vectors[i:i+1]); err != nil {
			t.Fatalf("Add for %s: %v", tenant, err)
		}
		if r.Loaded() > 2 {
			t.Fatalf("%d indexes loaded with MaxIndexes 2", r.Loaded())
		}
	}
	loads := store.loads.Load()
	got, err := r.Search(ctx, grantFor("t-1"), model, vectors[0], 1)
	if err != nil || len(got.Candidates) != 1 || got.Candidates[0].ID != 0 {
		t.Fatalf("Search of an evicted tenant = %+v, %v; want its own vector", got, err)
	}
	if store.loads.Load() != loads+1 {
		t.Fatalf("searching an evicted index loaded it %d times, want once", store.loads.Load()-loads)
	}
}

// TestAClosedRegistryAnswersErrClosed holds Close.
func TestAClosedRegistryAnswersErrClosed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r, err := faiss.NewRegistry(sfaceConfig(), faiss.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	g := grantFor("tenant-mu")
	vectors := randomVectors(65, 1, sfaceDim)
	if err := r.Add(ctx, g, model, []int64{1}, vectors); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("a second Close = %v", err)
	}
	if _, err := r.Search(ctx, g, model, vectors[0], 1); !errors.Is(err, faiss.ErrClosed) {
		t.Errorf("Search after Close = %v, want ErrClosed", err)
	}
	if err := r.Add(ctx, g, model, []int64{2}, vectors); !errors.Is(err, faiss.ErrClosed) {
		t.Errorf("Add after Close = %v, want ErrClosed", err)
	}
	if err := r.Rebuild(ctx, g, model, []int64{2}, vectors); !errors.Is(err, faiss.ErrClosed) {
		t.Errorf("Rebuild after Close = %v, want ErrClosed", err)
	}
}

// TestTenantsAreServedConcurrently runs searches and writes for many tenants
// at once, with fewer slots than tenants so indexes are evicted and loaded
// under load, and checks every tenant ends with exactly its own vectors.
func TestTenantsAreServedConcurrently(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := faiss.NewMemoryStore()
	cfg := sfaceConfig()
	cfg.MaxIndexes = 3
	r := newRegistry(t, cfg, store)

	const tenants, rounds, batch = 6, 8, 5
	var wg sync.WaitGroup
	errs := make(chan error, tenants*4)
	for n := 0; n < tenants; n++ {
		g := grantFor("tenant-" + string(rune('a'+n)))
		base := int64(n) * 1_000_000
		wg.Add(2)
		go func() {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				ids := make([]int64, batch)
				for i := range ids {
					ids[i] = base + int64(round*batch+i)
				}
				if err := r.Add(ctx, g, model, ids, randomVectors(uint64(base)+uint64(round), batch, sfaceDim)); err != nil {
					errs <- err
					return
				}
			}
			if _, err := r.Remove(ctx, g, model, []int64{base}); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			for i, query := range randomVectors(uint64(900+n), 20, sfaceDim) {
				got, err := r.Search(ctx, g, model, query, 3+i%4)
				if err != nil {
					errs <- err
					return
				}
				for _, c := range got.Candidates {
					if c.ID < base || c.ID >= base+1_000_000 {
						errs <- errors.New("a search answered another tenant's id")
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent use failed: %v", err)
	}
	for n := 0; n < tenants; n++ {
		g := grantFor("tenant-" + string(rune('a'+n)))
		if got, err := r.Len(ctx, g, model); err != nil || got != rounds*batch-1 {
			t.Fatalf("tenant %d ends with Len %d, %v; want %d", n, got, err, rounds*batch-1)
		}
	}
	if r.Loaded() > 3 {
		t.Fatalf("%d indexes loaded after the calls returned, with MaxIndexes 3", r.Loaded())
	}
}
