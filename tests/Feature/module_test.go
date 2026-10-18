package feature_test

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"

	faiss "github.com/hyz-is/arandu-faiss"
)

// These tests drive the module the way an application does: build it,
// register it on a router, boot it, ask its health and close it. They run in
// both builds, and say what each one answers.

func moduleConfig() faiss.Config {
	return faiss.Config{Models: []faiss.IndexSpec{{Model: "sface_2021dec", Dim: 128}}}
}

func TestTheModuleIsRegisteredAsAModule(t *testing.T) {
	t.Parallel()

	m, err := faiss.New(moduleConfig(), faiss.NewMemoryStore())
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	var module foundation.Module = m
	if module.Name() != "faiss" {
		t.Fatalf("Name() = %q, want faiss: route names and the boot log are keyed by it", module.Name())
	}
	if m.Registry() == nil {
		t.Fatal("Registry() is nil")
	}
}

// TestTheModuleServesNoRoute holds the reason Routes is empty: a search reads
// a tenant's biometric index, and is reached only through the application's
// controllers, behind its policy.
func TestTheModuleServesNoRoute(t *testing.T) {
	t.Parallel()

	m, err := faiss.New(moduleConfig(), faiss.NewMemoryStore())
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	router := fhttp.NewRouter()
	m.Routes(router.ForModule(m.Name()))
	if routes := router.Routes(); len(routes) != 0 {
		t.Fatalf("the module registered %d routes, want none: %+v", len(routes), routes)
	}
}

// TestNewRefusesAWiringThatCannotWork holds the constructor: a missing store
// or a configuration that could never serve fails where it is written.
func TestNewRefusesAWiringThatCannotWork(t *testing.T) {
	t.Parallel()

	if _, err := faiss.New(moduleConfig(), nil); err == nil {
		t.Error("New with no Store was accepted")
	}
	if _, err := faiss.New(faiss.Config{}, faiss.NewMemoryStore()); !errors.Is(err, faiss.ErrInvalidSpec) {
		t.Errorf("New with no model = %v, want ErrInvalidSpec", err)
	}
}

// TestTheModuleLivesAndCloses holds Boot, Health and Close against whichever
// build this is: with FAISS, Boot builds every model's index and Health is
// nil; without, both answer ErrUnavailable, so an application that wired the
// module into a build without cgo does not start believing it can screen.
func TestTheModuleLivesAndCloses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m, err := faiss.New(moduleConfig(), faiss.NewMemoryStore())
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	available := faiss.Available()
	if err := m.Boot(ctx); !errors.Is(err, available) && !(err == nil && available == nil) {
		t.Fatalf("Boot = %v, want %v", err, available)
	}
	if err := m.Health(ctx); !errors.Is(err, available) && !(err == nil && available == nil) {
		t.Fatalf("Health = %v, want %v", err, available)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if available == nil {
		if err := m.Health(ctx); !errors.Is(err, faiss.ErrClosed) {
			t.Fatalf("Health after Close = %v, want ErrClosed", err)
		}
	}
}

// TestBootRefusesAFactoryFAISSDoesNotKnow holds the point of building each
// model's index at boot: a configuration FAISS refuses stops the application
// before it serves.
func TestBootRefusesAFactoryFAISSDoesNotKnow(t *testing.T) {
	t.Parallel()

	if faiss.Available() != nil {
		t.Skip("no FAISS in this build: Boot answers ErrUnavailable, which TestTheModuleLivesAndCloses holds")
	}
	cfg := faiss.Config{Models: []faiss.IndexSpec{
		{Model: "sface_2021dec", Dim: 128},
		{Model: "other", Dim: 64, Factory: "HNSW32", Parameters: "noSuchKnob=1"},
	}}
	m, err := faiss.New(cfg, faiss.NewMemoryStore())
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	if err := m.Boot(context.Background()); !errors.Is(err, faiss.ErrEngine) {
		t.Fatalf("Boot = %v, want ErrEngine for the model FAISS refused", err)
	}
}
