package match_test

import (
	"fmt"
	"testing"

	"github.com/victorcondackdev/reconcile-go/dataset"
	"github.com/victorcondackdev/reconcile-go/match"
)

// BenchmarkReconcile runs the full four-pass pipeline on generated
// datasets of about n statement entries against n expected records.
func BenchmarkReconcile(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		for _, noise := range []float64{0, 0.5} {
			d := dataset.Generate(dataset.Noisy(noise, n, 1))
			cfg := dataset.MatchConfig()
			b.Run(fmt.Sprintf("orders=%d/noise=%.1f", n, noise), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := match.Reconcile(d.Entries, d.Expected, cfg); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(d.Entries)), "entries")
				b.ReportMetric(float64(len(d.Expected)), "expected")
			})
		}
	}
}
