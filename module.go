// Package faiss is an Arandu module that searches vectors through FAISS and
// answers with candidates.
//
// It owns no table, serves no route and decides nothing.
//
// An application builds it once in bootstrap/app.go and registers it there.
// There is no service provider, no container and no discovery.
package faiss

import (
	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
)

// Module is what the application registers.
type Module struct {
	cfg Config
}

// Compile-time proof that the module honors the contracts it claims.
var _ foundation.Module = (*Module)(nil)

// New returns the module, or the reason it cannot be built.
func New(cfg Config) (*Module, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Module{cfg: cfg}, nil
}

// Name is the module identifier: a lowercase slug, stable, no spaces.
func (m *Module) Name() string { return "faiss" }

// Routes registers nothing.
func (m *Module) Routes(*fhttp.Router) {}
