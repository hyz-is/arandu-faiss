package faiss

import "errors"

// The errors this package reports. Each is wrapped with the detail of the
// case, so callers test for them with errors.Is.
var (
	// ErrUnavailable is FAISS not linked into this build: the binary was built
	// with CGO_ENABLED=0 or with the nofaiss tag. Every index operation, and
	// the module's Boot, report it.
	ErrUnavailable = errors.New("faiss: FAISS is not linked into this build (it needs CGO_ENABLED=1 and libfaiss_c)")
	// ErrEngine is FAISS itself refusing an operation, with its own message.
	ErrEngine = errors.New("faiss: FAISS refused the operation")
	// ErrClosed is an index or a registry used after Close.
	ErrClosed = errors.New("faiss: closed")
	// ErrInvalidSpec is an IndexSpec or a Config that cannot be used.
	ErrInvalidSpec = errors.New("faiss: invalid index specification")
	// ErrDimension is a vector whose length is not the dimension of the index.
	ErrDimension = errors.New("faiss: vector dimension mismatch")
	// ErrInvalidVector is a vector holding NaN or an infinity, or the zero
	// vector under the cosine metric, where it has no direction.
	ErrInvalidVector = errors.New("faiss: invalid vector")
	// ErrInvalidID is a negative id. FAISS answers -1 for "no result", so no
	// id below zero can be stored.
	ErrInvalidID = errors.New("faiss: invalid id")
	// ErrIDExists is an id that is already in the index, or twice in one
	// batch. An id names one vector; replacing it is Remove, then Add.
	ErrIDExists = errors.New("faiss: id already in the index")
	// ErrInvalidK is a search for fewer than one or more than MaxK candidates.
	ErrInvalidK = errors.New("faiss: invalid number of candidates")
	// ErrSpecMismatch is a serialized index whose model, dimension, metric or
	// factory string is not the one it is being loaded as. It is rebuilt from
	// the application's templates, never loaded under another specification.
	ErrSpecMismatch = errors.New("faiss: serialized index does not match the specification")
	// ErrCorrupt is a serialized index that is truncated, altered or not one.
	ErrCorrupt = errors.New("faiss: serialized index is corrupt")
	// ErrUnknownModel is a model id the registry was not configured with.
	ErrUnknownModel = errors.New("faiss: unknown model")
	// ErrNoTenant is a Grant that carries no tenant: the zero Grant, or a
	// system grant refused for having none. No index belongs to nobody.
	ErrNoTenant = errors.New("faiss: the grant carries no tenant")
	// ErrReadOnly is a write attempted with a read-only Grant, issued to a
	// subject somebody else is viewing as.
	ErrReadOnly = errors.New("faiss: the grant is read-only")
	// ErrNotStored is what a Store answers Load with when it holds nothing
	// under the key. The registry starts an empty index for it.
	ErrNotStored = errors.New("faiss: nothing stored under the key")
)
