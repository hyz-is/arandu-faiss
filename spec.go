package faiss

import (
	"fmt"
	"regexp"
	"strings"
)

// Metric is how two vectors are compared.
type Metric string

// The metrics an IndexSpec may name.
const (
	// MetricCosine is cosine similarity. Every vector is L2-normalized before
	// it reaches FAISS, which then searches by inner product, so a Candidate's
	// Score is the cosine of the angle: 1 for the same direction, -1 for the
	// opposite one, higher is closer. It is what face embeddings such as SFace
	// are compared with, and the default.
	MetricCosine Metric = "cosine"
	// MetricInnerProduct is the raw inner product, with no normalization.
	// Higher is closer.
	MetricInnerProduct Metric = "inner_product"
	// MetricL2 is the squared Euclidean distance. Lower is closer.
	MetricL2 Metric = "l2"
)

// DefaultFactory is the FAISS index factory string an IndexSpec uses when it
// leaves Factory empty: an exhaustive, exact search over every vector.
const DefaultFactory = "Flat"

// The limits an IndexSpec and a search are held to. A value past them is a
// typo, not a tuning decision.
const (
	// MaxDim is the largest dimension an index may have.
	MaxDim = 65536
	// MaxK is the most candidates one search may ask for.
	MaxK = 4096
	// MaxModelLength is the longest a model id may be.
	MaxModelLength = 128
)

var (
	modelPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	factoryPattern    = regexp.MustCompile(`^[A-Za-z0-9_]+(,[A-Za-z0-9_]+)*$`)
	parametersPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=[0-9]+(\.[0-9]+)?(,[A-Za-z_][A-Za-z0-9_]*=[0-9]+(\.[0-9]+)?)*$`)
)

// IndexSpec is what an index is: which embedding model its vectors come from,
// their dimension, how they are compared, and how FAISS stores them.
//
// The model id is part of the identity of an index, not a label on it. Two
// embedding models produce vectors that cannot be compared with each other
// even when they have the same dimension, so an index built for one is refused
// when it is loaded as the other, and a vector from one is never searched in
// the other's index.
type IndexSpec struct {
	// Model is the id of the embedding model whose vectors the index holds,
	// as the application's model registry names it ("sface_2021dec"). It is
	// lowercase letters, digits, '.', '_' and '-', at most MaxModelLength.
	Model string

	// Dim is the length of every vector, between 1 and MaxDim. SFace is 128.
	Dim int

	// Metric is how vectors are compared. Empty means MetricCosine.
	Metric Metric

	// Factory is the FAISS index factory string describing how the vectors
	// are stored and searched. Empty means DefaultFactory, "Flat": exact,
	// exhaustive search, the right choice up to some hundreds of thousands of
	// vectors per index.
	//
	// "HNSW32" (a graph) and "IVF1024,Flat" (inverted lists) are approximate:
	// they search faster at scale and may miss a true nearest neighbour. Their
	// recall depends on Parameters and, for IVF, on the vectors it was trained
	// on; measure it against "Flat" on the application's own data before
	// relying on one. The package wraps every index in FAISS's IDMap2 itself,
	// so the string never starts with "IDMap".
	Factory string

	// Parameters is the FAISS search-time parameters to apply whenever the
	// index is built or loaded, as FAISS's ParameterSpace spells them:
	// "efSearch=128" for HNSW, "nprobe=16" for IVF. Empty applies none. They
	// trade speed for recall and change no stored vector.
	Parameters string
}

// Validate reports what the specification cannot be used with.
func (s IndexSpec) Validate() error {
	switch {
	case s.Model == "":
		return fmt.Errorf("%w: Model is empty, and an index has to say which embedding model its vectors come from", ErrInvalidSpec)
	case len(s.Model) > MaxModelLength:
		return fmt.Errorf("%w: Model is %d bytes long, more than %d", ErrInvalidSpec, len(s.Model), MaxModelLength)
	case !modelPattern.MatchString(s.Model):
		return fmt.Errorf("%w: Model %q has to be lowercase letters, digits, '.', '_' and '-'", ErrInvalidSpec, s.Model)
	case s.Dim < 1 || s.Dim > MaxDim:
		return fmt.Errorf("%w: Dim is %d, and has to be between 1 and %d", ErrInvalidSpec, s.Dim, MaxDim)
	}
	switch s.Metric {
	case "", MetricCosine, MetricInnerProduct, MetricL2:
	default:
		return fmt.Errorf("%w: Metric %q is not one of %q, %q or %q", ErrInvalidSpec, s.Metric, MetricCosine, MetricInnerProduct, MetricL2)
	}
	if s.Factory != "" {
		if !factoryPattern.MatchString(s.Factory) {
			return fmt.Errorf("%w: Factory %q is not a FAISS index factory string", ErrInvalidSpec, s.Factory)
		}
		if strings.HasPrefix(strings.ToUpper(s.Factory), "IDMAP") {
			return fmt.Errorf("%w: Factory %q starts with IDMap, which the package adds itself", ErrInvalidSpec, s.Factory)
		}
	}
	if s.Parameters != "" && !parametersPattern.MatchString(s.Parameters) {
		return fmt.Errorf("%w: Parameters %q is not a list of name=number pairs such as efSearch=64", ErrInvalidSpec, s.Parameters)
	}
	return nil
}

// withDefaults returns the specification with the optional fields filled in.
//
// It runs after Validate and never before: filling a default in first would
// hide the value somebody actually wrote from the check that would have refused
// it.
func (s IndexSpec) withDefaults() IndexSpec {
	if s.Metric == "" {
		s.Metric = MetricCosine
	}
	if s.Factory == "" {
		s.Factory = DefaultFactory
	}
	return s
}

// normalized validates the specification and fills its defaults.
func (s IndexSpec) normalized() (IndexSpec, error) {
	if err := s.Validate(); err != nil {
		return IndexSpec{}, err
	}
	return s.withDefaults(), nil
}

// Method says how the index finds its candidates: "exhaustive" when it
// compares the query with every vector (the "Flat" factory string), and
// "approximate" for every other structure, which may miss a true nearest
// neighbour.
func (s IndexSpec) Method() string {
	if s.withDefaults().Factory == DefaultFactory {
		return MethodExhaustive
	}
	return MethodApproximate
}

// The values IndexSpec.Method and Provenance.Method take.
const (
	// MethodExhaustive is a search that compared the query with every vector.
	MethodExhaustive = "exhaustive"
	// MethodApproximate is a search over a structure that may miss a true
	// nearest neighbour.
	MethodApproximate = "approximate"
)
