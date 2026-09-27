package stats

import "testing"

func r(ms int64) Result { return Result{TimeMs: ms} }

var dnfResult = Result{TimeMs: 5_000, Penalty: 2}

func val(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func check(t *testing.T, name string, got *int64, want any) {
	t.Helper()
	if val(got) != want {
		t.Errorf("%s = %v, want %v", name, val(got), want)
	}
}

func TestAverage(t *testing.T) {
	check(t, "ao5", Average([]Result{r(10_000), r(12_000), r(11_000), r(9_000), r(20_000)}), int64(11_000))
	check(t, "one DNF is trimmed", Average([]Result{r(10_000), r(12_000), r(11_000), r(9_000), dnfResult}), int64(11_000))
	check(t, "two DNFs", Average([]Result{r(10_000), r(12_000), r(11_000), dnfResult, dnfResult}), nil)
	check(t, "+2 counts", Average([]Result{r(10_000), {TimeMs: 10_000, Penalty: 1}, r(11_000), r(9_000), r(20_000)}), int64(11_000))
	check(t, "too few", Average([]Result{r(1), r(2)}), nil)
	check(t, "rounds to centiseconds", Average([]Result{r(1), r(10_005), r(10_005), r(10_006), r(99_999)}), int64(10_010))

	var hundred []Result
	for i := int64(1); i <= 95; i++ {
		hundred = append(hundred, r(i*1000))
	}
	for i := 0; i < 5; i++ {
		hundred = append(hundred, dnfResult)
	}
	check(t, "ao100 trims 5", Average(hundred), int64(50_500))
}

func TestMedian(t *testing.T) {
	check(t, "odd", Median([]Result{r(3_000), r(1_000), r(2_000)}), int64(2_000))
	check(t, "even", Median([]Result{r(1_000), r(2_000), r(3_000), r(4_000)}), int64(2_500))
	check(t, "DNF is slowest", Median([]Result{r(1_000), r(2_000), dnfResult}), int64(2_000))
	check(t, "DNF median", Median([]Result{r(1_000), dnfResult, dnfResult}), nil)
	check(t, "empty", Median(nil), nil)
}

func TestStdDev(t *testing.T) {
	// 2, 4, 4, 4, 5, 5, 7, 9 seconds: population σ = 2 s.
	var results []Result
	for _, s := range []int64{2, 4, 4, 4, 5, 5, 7, 9} {
		results = append(results, r(s*1000))
	}
	check(t, "σ", StdDev(results), int64(2_000))
	check(t, "ignores DNFs", StdDev(append(results, dnfResult)), int64(2_000))
	check(t, "too few", StdDev([]Result{r(1_000), dnfResult}), nil)
}
