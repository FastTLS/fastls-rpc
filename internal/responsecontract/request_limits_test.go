package responsecontract

import "testing"

func TestRequestBudgetValidation(t *testing.T) {
	for _, maximum := range []int64{-1, 64<<20 + 1} {
		if _, err := NewGuard(Limits{MaxRequestBytes: maximum}); err == nil {
			t.Fatal("invalid request budget accepted", maximum)
		}
	}
	for _, maximum := range []int64{0, 1, 4 << 20, 64 << 20} {
		guard, err := NewGuard(Limits{MaxRequestBytes: maximum})
		want := maximum
		if want == 0 {
			want = 4 << 20
		}
		if err != nil || guard.RequestMaximum() != want {
			t.Fatalf("maximum=%d guard=%v err=%v", maximum, guard, err)
		}
	}
}
