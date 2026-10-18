package faiss

// Config is what the application passes when it wires this package.
type Config struct{}

// Validate reports what the configuration cannot be used with.
func (c Config) Validate() error { return nil }
