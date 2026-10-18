package faiss

import (
	"bytes"
	"context"
	"sync"
)

// Key names one index: the tenant it belongs to and the embedding model its
// vectors come from.
//
// The registry builds every Key itself, with the tenant read from the Grant
// the application passed. A Store receives it and never has to trust a tenant
// from anywhere else.
type Key struct {
	// Tenant is data.Tenant of the Grant the call was made with.
	Tenant string
	// Model is the embedding model id, as the Config declares it.
	Model string
}

// Store is where the registry keeps serialized indexes between processes. The
// application implements it.
//
// What a Store receives is every vector of one tenant's index -- for face
// embeddings, biometric data. The application seals the bytes before they
// leave the process (in KUAA, encrypted and kept in a private R2 bucket), and
// derives the object key from Key itself, server side; Tenant and Model are
// not escaped for any storage, so a Store that builds a path from them
// encodes them first. The package never names a file or a bucket.
//
// Its methods may be called concurrently for different keys. The registry
// never calls Save or Delete concurrently for the same key.
type Store interface {
	// Load returns the bytes last saved under the key, or an error wrapping
	// ErrNotStored when nothing is.
	Load(ctx context.Context, key Key) ([]byte, error)
	// Save replaces whatever is stored under the key. It returns only once the
	// bytes are durable: the registry reports a write as done when Save has
	// returned nil.
	Save(ctx context.Context, key Key, blob []byte) error
	// Delete removes what is stored under the key. Nothing stored is not an
	// error.
	Delete(ctx context.Context, key Key) error
}

// MemoryStore is a Store that keeps the bytes in memory, for tests and for
// development. Everything it holds is lost when the process exits, and it
// seals nothing; an application in production passes a Store of its own.
type MemoryStore struct {
	mu    sync.Mutex
	blobs map[Key][]byte
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{blobs: map[Key][]byte{}} }

// Load returns a copy of the bytes saved under the key.
func (s *MemoryStore) Load(_ context.Context, key Key) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, ok := s.blobs[key]
	if !ok {
		return nil, ErrNotStored
	}
	return bytes.Clone(blob), nil
}

// Save keeps a copy of the bytes under the key.
func (s *MemoryStore) Save(_ context.Context, key Key, blob []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[key] = bytes.Clone(blob)
	return nil
}

// Delete forgets the bytes under the key.
func (s *MemoryStore) Delete(_ context.Context, key Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blobs, key)
	return nil
}

// Keys lists the keys that hold bytes, in no particular order.
func (s *MemoryStore) Keys() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Key, 0, len(s.blobs))
	for key := range s.blobs {
		out = append(out, key)
	}
	return out
}
