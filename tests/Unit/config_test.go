package unit_test

import (
	"errors"
	"testing"

	faiss "github.com/hyz-is/arandu-faiss"
)

func TestTheConfigurationRefusesWhatCannotWork(t *testing.T) {
	t.Parallel()

	sface := faiss.IndexSpec{Model: "sface_2021dec", Dim: 128}
	for name, cfg := range map[string]faiss.Config{
		"no model":                     {},
		"a model twice":                {Models: []faiss.IndexSpec{sface, sface}},
		"a model with no dimension":    {Models: []faiss.IndexSpec{{Model: "m"}}},
		"a negative MaxIndexes":        {Models: []faiss.IndexSpec{sface}, MaxIndexes: -1},
		"MaxIndexes above the cap":     {Models: []faiss.IndexSpec{sface}, MaxIndexes: faiss.MaxMaxIndexes + 1},
		"a default factory with IDMap": {Models: []faiss.IndexSpec{sface}, Factory: "IDMap,Flat"},
		"an unknown default metric":    {Models: []faiss.IndexSpec{sface}, Metric: "jaccard"},
	} {
		if err := cfg.Validate(); !errors.Is(err, faiss.ErrInvalidSpec) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidSpec", name, err)
		}
		if _, err := faiss.NewRegistry(cfg, faiss.NewMemoryStore()); !errors.Is(err, faiss.ErrInvalidSpec) {
			t.Errorf("%s: NewRegistry = %v, want ErrInvalidSpec", name, err)
		}
	}
}

// TestAModelInheritsTheDefaultsItLeavesEmpty holds how Config's factory string
// and metric reach the models that do not name their own.
func TestAModelInheritsTheDefaultsItLeavesEmpty(t *testing.T) {
	t.Parallel()

	cfg := faiss.Config{
		Factory: "HNSW32",
		Metric:  faiss.MetricInnerProduct,
		Models: []faiss.IndexSpec{
			{Model: "inherits", Dim: 8},
			{Model: "own", Dim: 8, Factory: "Flat", Metric: faiss.MetricL2},
		},
	}
	r, err := faiss.NewRegistry(cfg, faiss.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	got := map[string]faiss.IndexSpec{}
	for _, spec := range r.Models() {
		got[spec.Model] = spec
	}
	if spec := got["inherits"]; spec.Factory != "HNSW32" || spec.Metric != faiss.MetricInnerProduct {
		t.Errorf("a model leaving both empty got %+v, want HNSW32 and inner_product", spec)
	}
	if spec := got["own"]; spec.Factory != "Flat" || spec.Metric != faiss.MetricL2 {
		t.Errorf("a model naming its own got %+v, want Flat and l2", spec)
	}

	plain, err := faiss.NewRegistry(faiss.Config{Models: []faiss.IndexSpec{{Model: "m", Dim: 8}}}, faiss.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if spec := plain.Models()[0]; spec.Factory != faiss.DefaultFactory || spec.Metric != faiss.MetricCosine {
		t.Errorf("the zero defaults gave %+v, want Flat and cosine", spec)
	}
}

func TestNewRegistryNeedsAStore(t *testing.T) {
	t.Parallel()

	if _, err := faiss.NewRegistry(faiss.Config{Models: []faiss.IndexSpec{{Model: "m", Dim: 8}}}, nil); err == nil {
		t.Fatal("NewRegistry with no Store was accepted")
	}
}
