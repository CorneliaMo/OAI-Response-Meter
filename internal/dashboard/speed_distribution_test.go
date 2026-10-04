package dashboard

import "testing"

func TestSpeedDistributionNearestRank(t *testing.T) {
	for _, tc := range []struct {
		name           string
		values         []float64
		mean, p50, p95 float64
	}{
		{"empty", nil, 0, 0, 0},
		{"one", []float64{7}, 7, 7, 7},
		{"two", []float64{20, 10}, 15, 10, 20},
		{"repeated", []float64{3, 3, 3}, 3, 3, 3},
		{"outlier", []float64{100, 1, 1, 1, 1}, 20.8, 1, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := speedDistribution(tc.values)
			if got.Samples != len(tc.values) {
				t.Fatalf("samples=%d", got.Samples)
			}
			if len(tc.values) == 0 {
				if got.Mean != nil || got.P50 != nil || got.P95 != nil {
					t.Fatalf("empty=%+v", got)
				}
				return
			}
			if *got.Mean != tc.mean || *got.P50 != tc.p50 || *got.P95 != tc.p95 {
				t.Fatalf("mean=%v p50=%v p95=%v", *got.Mean, *got.P50, *got.P95)
			}
		})
	}
	values := make([]float64, 20)
	for i := range values {
		values[i] = float64(20 - i)
	}
	got := speedDistribution(values)
	if *got.P50 != 10 || *got.P95 != 19 {
		t.Fatalf("20 sample p50=%v p95=%v", *got.P50, *got.P95)
	}
}
