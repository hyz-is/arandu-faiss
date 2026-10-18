package unit_test

import (
	"errors"
	"strings"
	"testing"

	faiss "github.com/hyz-is/arandu-faiss"
)

func TestTheSpecificationRefusesWhatCannotWork(t *testing.T) {
	t.Parallel()

	for name, spec := range map[string]faiss.IndexSpec{
		"no model":                    {Dim: 128},
		"an uppercase model":          {Model: "SFace", Dim: 128},
		"a model with a slash":        {Model: "sface/2021", Dim: 128},
		"a model too long":            {Model: strings.Repeat("m", faiss.MaxModelLength+1), Dim: 128},
		"no dimension":                {Model: "m"},
		"a negative dimension":        {Model: "m", Dim: -1},
		"a dimension above the cap":   {Model: "m", Dim: faiss.MaxDim + 1},
		"an unknown metric":           {Model: "m", Dim: 8, Metric: "hamming"},
		"a factory with IDMap":        {Model: "m", Dim: 8, Factory: "IDMap2,Flat"},
		"a factory with lowercase id": {Model: "m", Dim: 8, Factory: "idmap,Flat"},
		"a factory with a space":      {Model: "m", Dim: 8, Factory: "HNSW32, Flat"},
		"a factory ending in a comma": {Model: "m", Dim: 8, Factory: "Flat,"},
		"parameters that are not n=v": {Model: "m", Dim: 8, Factory: "HNSW32", Parameters: "efSearch"},
		"parameters with a string":    {Model: "m", Dim: 8, Factory: "HNSW32", Parameters: "efSearch=lots"},
	} {
		if err := spec.Validate(); !errors.Is(err, faiss.ErrInvalidSpec) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidSpec", name, err)
		}
	}
}

func TestTheSpecificationAcceptsWhatFAISSIsGiven(t *testing.T) {
	t.Parallel()

	for _, spec := range []faiss.IndexSpec{
		{Model: "sface_2021dec", Dim: 128},
		{Model: "sface_2021dec", Dim: 128, Metric: faiss.MetricL2},
		{Model: "arcface-r100.v1", Dim: 512, Metric: faiss.MetricInnerProduct, Factory: "HNSW32", Parameters: "efSearch=128"},
		{Model: "m", Dim: 1, Factory: "IVF1024,Flat", Parameters: "nprobe=16"},
		{Model: "m", Dim: faiss.MaxDim, Factory: "IVF256,PQ16x8"},
	} {
		if err := spec.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", spec, err)
		}
	}
}

func TestOnlyTheFlatIndexSearchesExhaustively(t *testing.T) {
	t.Parallel()

	for factory, want := range map[string]string{
		"":             faiss.MethodExhaustive,
		"Flat":         faiss.MethodExhaustive,
		"HNSW32":       faiss.MethodApproximate,
		"IVF1024,Flat": faiss.MethodApproximate,
	} {
		if got := (faiss.IndexSpec{Model: "m", Dim: 8, Factory: factory}).Method(); got != want {
			t.Errorf("Method() for factory %q = %q, want %q", factory, got, want)
		}
	}
}
