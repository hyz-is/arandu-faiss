//go:build cgo && !nofaiss

package feature_test

import (
	"sync"
	"testing"

	faiss "github.com/hyz-is/arandu-faiss"
)

// These are meant for `go test -race`: they hold the index to its promise that
// searches run beside each other and beside writes without corrupting either.

// TestAnIndexSearchesWhileItIsWrittenTo runs searches, adds, removes and
// serializations on one index at once, for every structure Remove treats
// differently, and checks the index adds up at the end.
func TestAnIndexSearchesWhileItIsWrittenTo(t *testing.T) {
	t.Parallel()

	for _, spec := range []faiss.IndexSpec{
		{Model: "sface_2021dec", Dim: sfaceDim},
		{Model: "sface_2021dec", Dim: sfaceDim, Factory: "HNSW16"},
	} {
		t.Run(spec.Method(), func(t *testing.T) {
			t.Parallel()

			idx := newIndex(t, spec)
			seedIDs := randomIDs(30, 200)
			if err := idx.Add(seedIDs, randomVectors(31, 200, sfaceDim)); err != nil {
				t.Fatalf("Add: %v", err)
			}

			const writers, batches, batch = 3, 10, 20
			queries := randomVectors(32, 50, sfaceDim)
			var wg sync.WaitGroup
			errs := make(chan error, 64)
			stop := make(chan struct{})

			for s := 0; s < 6; s++ {
				wg.Add(1)
				go func(s int) {
					defer wg.Done()
					for n := 0; ; n++ {
						select {
						case <-stop:
							return
						default:
						}
						if _, err := idx.Search(queries[(s+n)%len(queries)], 5); err != nil {
							errs <- err
							return
						}
					}
				}(s)
			}
			var writing sync.WaitGroup
			for w := 0; w < writers; w++ {
				writing.Add(1)
				go func(w int) {
					defer writing.Done()
					for b := 0; b < batches; b++ {
						ids := make([]int64, batch)
						for i := range ids {
							ids[i] = int64(1_000_000*(w+1) + b*batch + i)
						}
						if err := idx.Add(ids, randomVectors(uint64(100+w*batches+b), batch, sfaceDim)); err != nil {
							errs <- err
							return
						}
					}
				}(w)
			}
			writing.Add(2)
			go func() {
				defer writing.Done()
				for i := 0; i < 50; i++ {
					if _, err := idx.Remove(seedIDs[i : i+1]); err != nil {
						errs <- err
						return
					}
				}
			}()
			go func() {
				defer writing.Done()
				for i := 0; i < 5; i++ {
					if _, err := idx.Serialize(); err != nil {
						errs <- err
						return
					}
				}
			}()
			writing.Wait()
			close(stop)
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("concurrent use failed: %v", err)
			}

			if want := 200 - 50 + writers*batches*batch; idx.Len() != want {
				t.Fatalf("Len() = %d after the concurrent writes, want %d", idx.Len(), want)
			}
			for _, id := range seedIDs[:50] {
				if idx.Contains(id) {
					t.Fatalf("Contains(%d) after it was removed", id)
				}
			}
		})
	}
}
