package faiss

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// The envelope a serialized index travels in.
//
// FAISS's own format says what kind of index it holds and nothing about what
// its vectors mean. The envelope adds what this package checks before FAISS
// reads a byte: the model the vectors come from, their dimension, the metric,
// the factory string, how many vectors there are, and the SHA-256 of the FAISS
// bytes, so a truncated or altered blob is refused rather than parsed.
//
// The layout is little-endian and versioned by its magic:
//
//	magic     8 bytes   "ARFAISS" and the version byte 1
//	model     u16 length, bytes
//	dim       u32
//	metric    u16 length, bytes
//	factory   u16 length, bytes
//	count     u64
//	digest    32 bytes, SHA-256 of the payload
//	payload   u64 length, bytes: what faiss::write_index wrote
//
// It is integrity, not secrecy and not authenticity: the application seals the
// blob before it leaves the process, and the store it lands in is its choice.
var envelopeMagic = [8]byte{'A', 'R', 'F', 'A', 'I', 'S', 'S', 1}

// envelopeHeader is what the envelope says about the index it carries.
type envelopeHeader struct {
	Model   string
	Dim     int
	Metric  Metric
	Factory string
	Count   int
}

// encodeEnvelope wraps a FAISS payload.
func encodeEnvelope(h envelopeHeader, payload []byte) []byte {
	var b bytes.Buffer
	b.Grow(len(payload) + 128)
	b.Write(envelopeMagic[:])
	writeShort(&b, h.Model)
	_ = binary.Write(&b, binary.LittleEndian, uint32(h.Dim))
	writeShort(&b, string(h.Metric))
	writeShort(&b, h.Factory)
	_ = binary.Write(&b, binary.LittleEndian, uint64(h.Count))
	digest := sha256.Sum256(payload)
	b.Write(digest[:])
	_ = binary.Write(&b, binary.LittleEndian, uint64(len(payload)))
	b.Write(payload)
	return b.Bytes()
}

// writeShort writes a string with a two-byte length. Every string written is
// bounded by IndexSpec.Validate well below that.
func writeShort(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.LittleEndian, uint16(len(s)))
	b.WriteString(s)
}

// decodeEnvelope unwraps a blob written by encodeEnvelope, and refuses one that
// is truncated, has bytes after its payload, or whose payload does not have
// the digest it was written with.
func decodeEnvelope(blob []byte) (envelopeHeader, []byte, error) {
	r := envelopeReader{rest: blob}
	magic := r.take(len(envelopeMagic))
	if r.err == nil && !bytes.Equal(magic, envelopeMagic[:]) {
		return envelopeHeader{}, nil, fmt.Errorf("%w: it does not start with this package's envelope", ErrCorrupt)
	}
	var h envelopeHeader
	h.Model = r.short()
	h.Dim = int(r.u32())
	h.Metric = Metric(r.short())
	h.Factory = r.short()
	count := r.u64()
	digest := r.take(sha256.Size)
	size := r.u64()
	if r.err != nil {
		return envelopeHeader{}, nil, r.err
	}
	if count > math.MaxInt32 || size != uint64(len(r.rest)) {
		return envelopeHeader{}, nil, fmt.Errorf("%w: the payload is %d bytes and the envelope says %d", ErrCorrupt, len(r.rest), size)
	}
	h.Count = int(count)
	payload := r.rest
	if sum := sha256.Sum256(payload); !bytes.Equal(sum[:], digest) {
		return envelopeHeader{}, nil, fmt.Errorf("%w: the payload's SHA-256 is not the one it was written with", ErrCorrupt)
	}
	return h, payload, nil
}

// envelopeReader reads the fixed fields of an envelope, remembering the first
// short read so the caller checks once.
type envelopeReader struct {
	rest []byte
	err  error
}

func (r *envelopeReader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.rest) {
		r.err = fmt.Errorf("%w: it ends before its header does", ErrCorrupt)
		return nil
	}
	out := r.rest[:n]
	r.rest = r.rest[n:]
	return out
}

func (r *envelopeReader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

func (r *envelopeReader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (r *envelopeReader) u64() uint64 {
	if b := r.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}

func (r *envelopeReader) short() string {
	n := r.u16()
	return string(r.take(int(n)))
}
