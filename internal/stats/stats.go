// Package stats computes the public statistics. Definitions match the app (StatsKit):
// times are in milliseconds, +2 adds 2000 ms, DNFs count as the slowest result.
package stats

import (
	"math"
	"sort"
)

// Result is one solve: its raw time and penalty (0 none, 1 +2, 2 DNF).
type Result struct {
	TimeMs  int64
	Penalty int
}

// dnf sorts after every real time.
const dnf = math.MaxInt64

func (r Result) effective() int64 {
	switch r.Penalty {
	case 1:
		return r.TimeMs + 2000
	case 2:
		return dnf
	default:
		return r.TimeMs
	}
}

func sortedTimes(results []Result) []int64 {
	times := make([]int64, len(results))
	for i, r := range results {
		times[i] = r.effective()
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return times
}

// roundToCentiseconds rounds to the nearest 10 ms, like WCA averages.
func roundToCentiseconds(ms float64) int64 {
	return int64(math.Round(ms/10)) * 10
}

// Average is the trimmed mean: drop ceil(5%) (at least 1) of the results from each end and average
// the rest. Nil if there are fewer than 3 results or a DNF survives the trim.
func Average(results []Result) *int64 {
	n := len(results)
	if n < 3 {
		return nil
	}
	trim := max(1, (n*5+99)/100)
	kept := sortedTimes(results)[trim : n-trim]
	var sum int64
	for _, t := range kept {
		if t == dnf {
			return nil
		}
		sum += t
	}
	v := roundToCentiseconds(float64(sum) / float64(len(kept)))
	return &v
}

// Median of all results, DNFs counting as the slowest. Nil if empty or the median is a DNF.
func Median(results []Result) *int64 {
	n := len(results)
	if n == 0 {
		return nil
	}
	times := sortedTimes(results)
	lo, hi := times[(n-1)/2], times[n/2]
	if hi == dnf {
		return nil
	}
	v := roundToCentiseconds(float64(lo+hi) / 2)
	return &v
}

// StdDev is the population standard deviation of the non-DNF times. Nil with fewer than 2 of them.
func StdDev(results []Result) *int64 {
	var times []float64
	for _, r := range results {
		if t := r.effective(); t != dnf {
			times = append(times, float64(t))
		}
	}
	if len(times) < 2 {
		return nil
	}
	var mean float64
	for _, t := range times {
		mean += t
	}
	mean /= float64(len(times))
	var variance float64
	for _, t := range times {
		variance += (t - mean) * (t - mean)
	}
	v := roundToCentiseconds(math.Sqrt(variance / float64(len(times))))
	return &v
}
