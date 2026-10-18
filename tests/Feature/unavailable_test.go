//go:build !cgo || nofaiss

package feature_test

import (
	"errors"
	"testing"

	faiss "github.com/hyz-is/arandu-faiss"
)

// These run only in a build without FAISS -- CGO_ENABLED=0, or the nofaiss
// tag -- and hold that such a build says so, everywhere, instead of answering
// an empty list of candidates that would read as "no duplicate found".

func TestABuildWithoutFAISSReportsItUnavailable(t *testing.T) {
	t.Parallel()

	if err := faiss.Available(); !errors.Is(err, faiss.ErrUnavailable) {
		t.Fatalf("Available() = %v, want ErrUnavailable", err)
	}
	if v := faiss.EngineVersion(); v != "" {
		t.Fatalf("EngineVersion() = %q with no FAISS linked", v)
	}
	spec := faiss.IndexSpec{Model: "sface_2021dec", Dim: 128}
	if _, err := faiss.NewIndex(spec); !errors.Is(err, faiss.ErrUnavailable) {
		t.Fatalf("NewIndex = %v, want ErrUnavailable", err)
	}
	if _, err := faiss.Deserialize([]byte("anything"), spec); !errors.Is(err, faiss.ErrUnavailable) {
		t.Fatalf("Deserialize = %v, want ErrUnavailable", err)
	}
}

// TestAWrongSpecificationIsStillReportedAsWrong holds the order: a
// specification that could never work is reported as that, in either build, so
// moving to a build with FAISS does not reveal a second mistake.
func TestAWrongSpecificationIsStillReportedAsWrong(t *testing.T) {
	t.Parallel()

	if _, err := faiss.NewIndex(faiss.IndexSpec{Model: "", Dim: 128}); !errors.Is(err, faiss.ErrInvalidSpec) {
		t.Fatalf("NewIndex of an invalid spec = %v, want ErrInvalidSpec", err)
	}
}
