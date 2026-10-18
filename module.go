// Package faiss is an Arandu module that searches vectors with FAISS and
// answers with candidates.
//
// It keeps one index per tenant and embedding model, persisted through a Store
// the application passes in, and answers a query with the stored vectors
// nearest it: their ids, their scores and the provenance of the search. It is
// built for 1:N duplicate detection of face embeddings -- "this new selfie is
// near these enrolled templates" -- and it decides nothing: whether a
// candidate is the same person is the application's decision, taken with its
// own thresholds.
//
// It owns no table and serves no route. It links FAISS through its C API with
// cgo; built without cgo, every operation reports ErrUnavailable.
//
// An application builds it once in bootstrap/app.go and registers it there.
// There is no service provider, no container and no discovery.
package faiss

import (
	"context"
	"errors"
	"fmt"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
)

// Module is what the application registers, and what hands it the Registry.
//
// Besides foundation.Module it implements foundation.Bootable, which checks
// that FAISS is linked and builds every configured model's index once,
// foundation.Health, which reports whether the engine is usable, and
// foundation.Closable, which frees every loaded index at shutdown.
type Module struct {
	cfg      Config
	registry *Registry
}

// Compile-time proof that the module honors the contracts it claims.
var (
	_ foundation.Module   = (*Module)(nil)
	_ foundation.Bootable = (*Module)(nil)
	_ foundation.Health   = (*Module)(nil)
	_ foundation.Closable = (*Module)(nil)
)

// New returns the module, or the reason it cannot be built.
//
// It returns an error rather than panicking or carrying on, because everything
// it refuses is a wiring mistake, and a wiring mistake found at boot costs one
// restart.
//
// It touches no FAISS and no Store: Boot checks the engine, and each index is
// loaded the first time a call needs it, so building the module in a test or
// a command that never boots it needs neither.
func New(cfg Config, store Store) (*Module, error) {
	registry, err := NewRegistry(cfg, store)
	if err != nil {
		return nil, err
	}
	return &Module{cfg: cfg.withDefaults(), registry: registry}, nil
}

// Registry is the per-tenant index registry the application calls, with the
// Grant its policy issued.
func (m *Module) Registry() *Registry { return m.registry }

// Name is the module identifier: a lowercase slug, stable, no spaces.
func (m *Module) Name() string { return "faiss" }

// Routes registers nothing.
//
// A search reads a tenant's biometric index, and is reached only through the
// application's own controllers, behind its policy and with its Grant. A route
// here would search for anybody who sent a vector.
func (m *Module) Routes(*fhttp.Router) {}

// Boot checks that FAISS is linked into the binary and builds, then frees, an
// empty index of every configured model, so a factory string or a search
// parameter FAISS does not accept stops the boot instead of the first search.
func (m *Module) Boot(context.Context) error {
	if err := Available(); err != nil {
		return err
	}
	var errs []error
	for _, spec := range m.cfg.Models {
		idx, err := NewIndex(spec)
		if err != nil {
			errs = append(errs, fmt.Errorf("model %q: %w", spec.Model, err))
			continue
		}
		_ = idx.Close()
	}
	return errors.Join(errs...)
}

// Health reports whether FAISS is linked and the registry is open.
func (m *Module) Health(context.Context) error {
	if err := Available(); err != nil {
		return err
	}
	m.registry.mu.Lock()
	defer m.registry.mu.Unlock()
	if m.registry.closed {
		return ErrClosed
	}
	return nil
}

// Close frees every loaded index once the calls using it return.
func (m *Module) Close(context.Context) error { return m.registry.Close() }
