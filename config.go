package faiss

import "fmt"

// The defaults and limits of the registry. They are constants rather than
// literals inside Config.withDefaults, so the value a reader finds here is the
// value the package uses.
const (
	// DefaultMaxIndexes is how many indexes the registry keeps in memory when
	// Config leaves MaxIndexes at zero.
	DefaultMaxIndexes = 64
	// MaxMaxIndexes is the ceiling MaxIndexes is refused above.
	MaxMaxIndexes = 1 << 20
)

// Config is what the application passes when it wires this package.
//
// Every value comes from the application's own typed configuration. This
// package reads no environment variable: a setting that could arrive from two
// places is a setting nobody can say the value of.
//
// It names no tenant, and that is deliberate. The tenant of every index is the
// one on the Grant each call is made with; a tenant written here would be a
// second place for one to come from.
type Config struct {
	// Models is the embedding models the registry keeps indexes of: for each,
	// its id, its dimension and, optionally, the metric, factory string and
	// search parameters of its indexes. At least one is required, and no id
	// twice. A call naming a model not listed here is refused with
	// ErrUnknownModel.
	Models []IndexSpec

	// Factory is the factory string of a model that leaves its own empty.
	// Empty means DefaultFactory, "Flat": exact search.
	Factory string

	// Metric is the metric of a model that leaves its own empty. Empty means
	// MetricCosine.
	Metric Metric

	// MaxIndexes is how many indexes, across every tenant and model, the
	// registry keeps loaded at once. Past it, the one used least recently is
	// closed; the next call for it loads it from the Store again. Zero means
	// DefaultMaxIndexes. An index in use is never closed under its caller, so
	// a burst of concurrent tenants can exceed it until their calls return.
	MaxIndexes int
}

// Validate reports what the configuration cannot be used with.
//
// It is called by New and NewRegistry, so a setting that cannot work fails
// where it is wired rather than on the first search.
func (c Config) Validate() error {
	if len(c.Models) == 0 {
		return fmt.Errorf("%w: Config.Models is empty, and the registry keeps indexes of the models it is told about", ErrInvalidSpec)
	}
	if c.MaxIndexes < 0 || c.MaxIndexes > MaxMaxIndexes {
		return fmt.Errorf("%w: Config.MaxIndexes is %d, and has to be between 0 and %d, where 0 means %d",
			ErrInvalidSpec, c.MaxIndexes, MaxMaxIndexes, DefaultMaxIndexes)
	}
	if err := (IndexSpec{Model: "default", Dim: 1, Factory: c.Factory, Metric: c.Metric}).Validate(); err != nil {
		return fmt.Errorf("Config.Factory or Config.Metric: %w", err)
	}
	seen := map[string]bool{}
	for n, spec := range c.Models {
		if err := c.inherit(spec).Validate(); err != nil {
			return fmt.Errorf("Config.Models[%d]: %w", n, err)
		}
		if seen[spec.Model] {
			return fmt.Errorf("%w: Config.Models declares %q twice", ErrInvalidSpec, spec.Model)
		}
		seen[spec.Model] = true
	}
	return nil
}

// inherit gives a model the configuration's factory string and metric where
// it leaves its own empty.
func (c Config) inherit(spec IndexSpec) IndexSpec {
	if spec.Factory == "" {
		spec.Factory = c.Factory
	}
	if spec.Metric == "" {
		spec.Metric = c.Metric
	}
	return spec
}

// withDefaults returns the configuration with the optional fields filled in.
//
// It runs after Validate and never before: filling a default in first would
// hide the value somebody actually wrote from the check that would have refused
// it.
func (c Config) withDefaults() Config {
	if c.MaxIndexes == 0 {
		c.MaxIndexes = DefaultMaxIndexes
	}
	models := make([]IndexSpec, len(c.Models))
	for n, spec := range c.Models {
		models[n] = c.inherit(spec).withDefaults()
	}
	c.Models = models
	return c
}
