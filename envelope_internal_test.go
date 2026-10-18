package faiss

import (
	"bytes"
	"errors"
	"testing"
)

// The envelope is pure Go, so it is tested here, beside the code, in every
// build -- including the one without FAISS.

func TestTheEnvelopeReadsBackWhatItWrote(t *testing.T) {
	t.Parallel()

	header := envelopeHeader{Model: "sface_2021dec", Dim: 128, Metric: MetricCosine, Factory: "HNSW32", Count: 3}
	payload := bytes.Repeat([]byte{1, 2, 3, 4}, 50)
	gotHeader, gotPayload, err := decodeEnvelope(encodeEnvelope(header, payload))
	if err != nil {
		t.Fatalf("decodeEnvelope: %v", err)
	}
	if gotHeader != header || !bytes.Equal(gotPayload, payload) {
		t.Fatalf("decoded %+v and %d bytes, want %+v and %d bytes", gotHeader, len(gotPayload), header, len(payload))
	}
}

func TestEveryTruncationOfTheEnvelopeIsCorrupt(t *testing.T) {
	t.Parallel()

	blob := encodeEnvelope(envelopeHeader{Model: "m", Dim: 4, Metric: MetricL2, Factory: "Flat", Count: 1}, []byte("payload"))
	for n := 0; n < len(blob); n++ {
		if _, _, err := decodeEnvelope(blob[:n]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("decodeEnvelope of the first %d of %d bytes = %v, want ErrCorrupt", n, len(blob), err)
		}
	}
	if _, _, err := decodeEnvelope(append(bytes.Clone(blob), 0)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decodeEnvelope with a trailing byte = %v, want ErrCorrupt", err)
	}
}

func TestAnAlteredEnvelopeIsCorrupt(t *testing.T) {
	t.Parallel()

	blob := encodeEnvelope(envelopeHeader{Model: "m", Dim: 4, Metric: MetricL2, Factory: "Flat", Count: 1}, []byte("payload"))
	magic := bytes.Clone(blob)
	magic[7] = 2
	payload := bytes.Clone(blob)
	payload[len(payload)-1] ^= 0xff
	for name, bad := range map[string][]byte{"another version": magic, "an altered payload": payload} {
		if _, _, err := decodeEnvelope(bad); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: decodeEnvelope = %v, want ErrCorrupt", name, err)
		}
	}
}
