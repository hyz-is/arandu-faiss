package faiss

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
)

// Result is what a registry search answers: the candidates, and what produced
// them, for the application to record as evidence.
type Result struct {
	// Candidates is the stored vectors nearest the query, closest first.
	Candidates []Candidate
	// Provenance is the index that was searched, as it was when it was.
	Provenance Provenance
}

// writeStripes is how many locks the registry's writes are spread over. Two
// writes to one index always take the same lock; two writes to different
// indexes rarely do.
const writeStripes = 64

// Registry keeps one index per tenant and embedding model, loads each from the
// Store the first time it is needed, and writes every change back before the
// call that made it returns.
//
// Every method takes the security.Grant the application's policy issued for
// the call, and the tenant is data.Tenant of it -- never a value the request
// named. Tenants never share an index: a search sees the vectors of the
// Grant's tenant and of no other, because no other tenant's vectors are in
// the index it searches.
//
// Its methods are safe for concurrent use. Searches run in parallel; writes
// to one index run one at a time, each followed by its Save.
type Registry struct {
	models map[string]IndexSpec
	store  Store
	max    int

	mu      sync.Mutex
	entries map[Key]*entry
	tick    uint64
	closed  bool

	writes [writeStripes]sync.Mutex
}

// entry is one index the registry has loaded, or is loading.
//
// idx and err are written once, under the registry's mutex and before ready is
// closed; a caller that waited on ready reads them without the mutex. loaded,
// refs, used and evicted are guarded by the registry's mutex.
type entry struct {
	ready chan struct{}
	idx   *Index
	err   error

	loaded  bool
	refs    int
	used    uint64
	evicted bool
}

// NewRegistry returns a registry over the store, or the reason it cannot be
// built. It loads nothing: each index is loaded the first time a call needs
// it.
func NewRegistry(cfg Config, store Store) (*Registry, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("faiss: NewRegistry needs a Store: every index is written back to it, and there is no mode that keeps one only in memory")
	}
	cfg = cfg.withDefaults()
	models := make(map[string]IndexSpec, len(cfg.Models))
	for _, spec := range cfg.Models {
		models[spec.Model] = spec
	}
	return &Registry{models: models, store: store, max: cfg.MaxIndexes, entries: map[Key]*entry{}}, nil
}

// Models is the specification of every model the registry keeps indexes of,
// defaults filled in.
func (r *Registry) Models() []IndexSpec {
	out := make([]IndexSpec, 0, len(r.models))
	for _, spec := range r.models {
		out = append(out, spec)
	}
	return out
}

// Search returns up to k vectors of the Grant's tenant and the model nearest
// the query, closest first, with the provenance of the index searched. A
// tenant with nothing indexed answers no candidate.
//
// The candidates are evidence for the application's decision and never the
// decision: a high score says two vectors point the same way, not that two
// people are one.
func (r *Registry) Search(ctx context.Context, g security.Grant, model string, query []float32, k int) (Result, error) {
	key, spec, err := r.keyFor(g, model, false)
	if err != nil {
		return Result{}, err
	}
	if len(query) != spec.Dim {
		return Result{}, fmt.Errorf("query: %w: it has %d values and model %q has %d", ErrDimension, len(query), model, spec.Dim)
	}
	e, err := r.acquire(ctx, key, spec)
	if err != nil {
		return Result{}, err
	}
	defer r.release(e)
	candidates, provenance, err := e.idx.search(query, k)
	if err != nil {
		return Result{}, err
	}
	return Result{Candidates: candidates, Provenance: provenance}, nil
}

// Len is how many vectors the index of the Grant's tenant and the model holds.
func (r *Registry) Len(ctx context.Context, g security.Grant, model string) (int, error) {
	key, spec, err := r.keyFor(g, model, false)
	if err != nil {
		return 0, err
	}
	e, err := r.acquire(ctx, key, spec)
	if err != nil {
		return 0, err
	}
	defer r.release(e)
	return e.idx.Len(), nil
}

// Add stores the vectors under the ids in the index of the Grant's tenant and
// the model, and saves the index to the Store before it returns. It checks
// the batch as Index.Add does: one bad element refuses all of it.
//
// When the Save fails the index in memory is discarded, so the next call loads
// what the Store holds: the registry never answers from a change it could not
// keep.
func (r *Registry) Add(ctx context.Context, g security.Grant, model string, ids []int64, vectors [][]float32) error {
	key, spec, err := r.keyFor(g, model, true)
	if err != nil {
		return err
	}
	return r.write(ctx, key, spec, func(idx *Index) (bool, error) {
		if err := idx.Add(ids, vectors); err != nil {
			return false, err
		}
		return len(ids) > 0, nil
	})
}

// Remove deletes the vectors under the ids from the index of the Grant's
// tenant and the model, saves the index to the Store, and reports how many
// were there. When it returns nil, neither the index in memory nor the bytes
// in the Store hold them: it is the erasure path.
func (r *Registry) Remove(ctx context.Context, g security.Grant, model string, ids []int64) (int, error) {
	key, spec, err := r.keyFor(g, model, true)
	if err != nil {
		return 0, err
	}
	removed := 0
	err = r.write(ctx, key, spec, func(idx *Index) (bool, error) {
		n, err := idx.Remove(ids)
		removed = n
		return n > 0, err
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// Rebuild replaces the index of the Grant's tenant and the model with one
// holding exactly these vectors, and saves it. It is how an index is built
// from the application's own templates: the first time, after the
// configuration of a model changed (ErrSpecMismatch on load), and for an index
// whose structure needs training on every vector at once.
//
// The new index is complete and saved before it replaces the old one, so a
// failure leaves the old one in place, in memory and in the Store.
func (r *Registry) Rebuild(ctx context.Context, g security.Grant, model string, ids []int64, vectors [][]float32) error {
	key, spec, err := r.keyFor(g, model, true)
	if err != nil {
		return err
	}
	lock := r.writeLock(key)
	lock.Lock()
	defer lock.Unlock()

	idx, err := NewIndex(spec)
	if err != nil {
		return err
	}
	if err := idx.Add(ids, vectors); err != nil {
		_ = idx.Close()
		return err
	}
	blob, err := idx.Serialize()
	if err != nil {
		_ = idx.Close()
		return err
	}
	if err := r.store.Save(ctx, key, blob); err != nil {
		_ = idx.Close()
		return fmt.Errorf("faiss: saving the rebuilt index: %w", err)
	}
	return r.replace(key, idx)
}

// Drop deletes the index of the Grant's tenant and the model, in memory and in
// the Store: every vector of that tenant and model at once.
func (r *Registry) Drop(ctx context.Context, g security.Grant, model string) error {
	key, _, err := r.keyFor(g, model, true)
	if err != nil {
		return err
	}
	lock := r.writeLock(key)
	lock.Lock()
	defer lock.Unlock()

	if err := r.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("faiss: deleting the index: %w", err)
	}
	r.discard(key)
	return nil
}

// Close closes every loaded index once the calls using it return. Every call
// after Close answers ErrClosed. It is idempotent.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for key, e := range r.entries {
		r.evictLocked(key, e)
	}
	return nil
}

// Loaded is how many indexes are in memory now.
func (r *Registry) Loaded() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// keyFor reads the tenant off the Grant and the specification of the model,
// and refuses a Grant with no tenant, a read-only Grant for a write, and a
// model the registry was not configured with.
func (r *Registry) keyFor(g security.Grant, model string, writes bool) (Key, IndexSpec, error) {
	tenant := data.Tenant(g)
	if tenant == "" {
		return Key{}, IndexSpec{}, ErrNoTenant
	}
	if writes {
		if err := auth.Writable(g); err != nil {
			return Key{}, IndexSpec{}, fmt.Errorf("%w: %w", ErrReadOnly, err)
		}
	}
	spec, ok := r.models[model]
	if !ok {
		return Key{}, IndexSpec{}, fmt.Errorf("%w: %q is not one of the models in Config.Models", ErrUnknownModel, model)
	}
	return Key{Tenant: tenant, Model: model}, spec, nil
}

// write runs a change on the index under the key's write lock, and saves the
// index when the change says it changed anything.
func (r *Registry) write(ctx context.Context, key Key, spec IndexSpec, change func(*Index) (bool, error)) error {
	lock := r.writeLock(key)
	lock.Lock()
	defer lock.Unlock()

	e, err := r.acquire(ctx, key, spec)
	if err != nil {
		return err
	}
	defer r.release(e)

	changed, err := change(e.idx)
	if err != nil {
		if !isRefusal(err) {
			// FAISS failed part way, or the index is in a state nobody can
			// vouch for: load it again from what the Store holds.
			r.discard(key)
		}
		return err
	}
	if !changed {
		return nil
	}
	blob, err := e.idx.Serialize()
	if err == nil {
		err = r.store.Save(ctx, key, blob)
	}
	if err != nil {
		r.discard(key)
		return fmt.Errorf("faiss: saving the index: %w", err)
	}
	return nil
}

// isRefusal reports whether an Index refused a call before changing anything.
func isRefusal(err error) bool {
	for _, refusal := range []error{ErrDimension, ErrInvalidVector, ErrInvalidID, ErrIDExists, ErrInvalidK, ErrClosed} {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}

// writeLock is the lock every write to the key takes.
func (r *Registry) writeLock(key Key) *sync.Mutex {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key.Tenant))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key.Model))
	return &r.writes[h.Sum64()%writeStripes]
}

// acquire returns the loaded index for the key, loading it from the Store if
// nobody has, and holds it open until release. Concurrent callers for one key
// share one load.
func (r *Registry) acquire(ctx context.Context, key Key, spec IndexSpec) (*entry, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrClosed
	}
	r.tick++
	e, found := r.entries[key]
	if found {
		e.refs++
		e.used = r.tick
		r.mu.Unlock()
	} else {
		e = &entry{ready: make(chan struct{}), refs: 1, used: r.tick}
		r.entries[key] = e
		r.mu.Unlock()

		idx, err := r.load(ctx, key, spec)
		r.mu.Lock()
		e.idx, e.err, e.loaded = idx, err, true
		if err != nil {
			if r.entries[key] == e {
				delete(r.entries, key)
			}
		} else {
			r.trimLocked()
		}
		r.mu.Unlock()
		close(e.ready)
	}

	select {
	case <-e.ready:
	case <-ctx.Done():
		r.release(e)
		return nil, ctx.Err()
	}
	if e.err != nil {
		r.release(e)
		return nil, e.err
	}
	return e, nil
}

// load reads the index for the key from the Store, or starts an empty one when
// the Store holds none.
func (r *Registry) load(ctx context.Context, key Key, spec IndexSpec) (*Index, error) {
	blob, err := r.store.Load(ctx, key)
	switch {
	case errors.Is(err, ErrNotStored):
		return NewIndex(spec)
	case err != nil:
		return nil, fmt.Errorf("faiss: loading the index: %w", err)
	}
	idx, err := Deserialize(blob, spec)
	if err != nil {
		return nil, fmt.Errorf("faiss: loading the index of model %q: %w", key.Model, err)
	}
	return idx, nil
}

// release gives back a hold taken by acquire, and closes the index of an
// entry that was evicted while it was held.
func (r *Registry) release(e *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.refs--
	if e.evicted && e.refs == 0 && e.loaded && e.idx != nil {
		_ = e.idx.Close()
	}
	// An index that was held when the registry went past its bound could not
	// be closed then; the bound is restored as soon as one is free.
	if len(r.entries) > r.max {
		r.trimLocked()
	}
}

// replace puts a new, complete index under the key in place of whatever was
// there.
func (r *Registry) replace(key Key, idx *Index) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		_ = idx.Close()
		return ErrClosed
	}
	if old, ok := r.entries[key]; ok {
		r.evictLocked(key, old)
	}
	r.tick++
	e := &entry{ready: make(chan struct{}), idx: idx, loaded: true, used: r.tick}
	close(e.ready)
	r.entries[key] = e
	r.trimLocked()
	return nil
}

// discard forgets the index under the key, so the next call loads it again.
func (r *Registry) discard(key Key) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[key]; ok {
		r.evictLocked(key, e)
	}
}

// evictLocked takes an entry out of the registry, and closes its index now if
// nobody holds it, or when the last holder releases it.
func (r *Registry) evictLocked(key Key, e *entry) {
	if r.entries[key] == e {
		delete(r.entries, key)
	}
	e.evicted = true
	if e.refs == 0 && e.loaded && e.idx != nil {
		_ = e.idx.Close()
	}
}

// trimLocked evicts the least recently used loaded indexes that nobody holds,
// until no more than max are loaded.
func (r *Registry) trimLocked() {
	for len(r.entries) > r.max {
		var victimKey Key
		var victim *entry
		for key, e := range r.entries {
			if e.refs > 0 || !e.loaded {
				continue
			}
			if victim == nil || e.used < victim.used {
				victimKey, victim = key, e
			}
		}
		if victim == nil {
			return
		}
		r.evictLocked(victimKey, victim)
	}
}
