package main

import (
	"crypto/rand"
	"math"
	"sort"
	"time"
)

// BenchResult holds metrics for a single benchmark run.
type BenchResult struct {
	Mode      string // "raw-dtls"
	Run       int
	BytesSent int64
	Duration  time.Duration
	Throughput float64 // Mbps
	SetupTime time.Duration
	Latencies []time.Duration // individual RTT samples
}

// AggregatedResult holds statistics across multiple runs.
type AggregatedResult struct {
	Mode       string
	Runs       int
	AvgThroughput float64
	StdThroughput float64
	MinThroughput float64
	MaxThroughput float64
	AvgSetupTime  time.Duration
	AvgLatency    time.Duration
	P50Latency    time.Duration
	P95Latency    time.Duration
	P99Latency    time.Duration
	Jitter        time.Duration
}

func generatePayload(size int) []byte {
	buf := make([]byte, size)
	rand.Read(buf)
	return buf
}

func aggregate(results []BenchResult) AggregatedResult {
	if len(results) == 0 {
		return AggregatedResult{}
	}

	agg := AggregatedResult{
		Mode:          results[0].Mode,
		Runs:          len(results),
		MinThroughput: math.MaxFloat64,
	}

	var sumTP float64
	var sumSetup time.Duration
	var allLatencies []time.Duration

	for _, r := range results {
		sumTP += r.Throughput
		sumSetup += r.SetupTime
		if r.Throughput < agg.MinThroughput {
			agg.MinThroughput = r.Throughput
		}
		if r.Throughput > agg.MaxThroughput {
			agg.MaxThroughput = r.Throughput
		}
		allLatencies = append(allLatencies, r.Latencies...)
	}

	agg.AvgThroughput = sumTP / float64(len(results))
	agg.AvgSetupTime = sumSetup / time.Duration(len(results))

	// stddev throughput
	var sumSq float64
	for _, r := range results {
		d := r.Throughput - agg.AvgThroughput
		sumSq += d * d
	}
	agg.StdThroughput = math.Sqrt(sumSq / float64(len(results)))

	// latency stats
	if len(allLatencies) > 0 {
		sort.Slice(allLatencies, func(i, j int) bool { return allLatencies[i] < allLatencies[j] })

		var sum time.Duration
		for _, l := range allLatencies {
			sum += l
		}
		agg.AvgLatency = sum / time.Duration(len(allLatencies))
		agg.P50Latency = percentile(allLatencies, 50)
		agg.P95Latency = percentile(allLatencies, 95)
		agg.P99Latency = percentile(allLatencies, 99)

		// jitter = stddev of latencies
		avgNs := float64(agg.AvgLatency.Nanoseconds())
		var jitterSum float64
		for _, l := range allLatencies {
			d := float64(l.Nanoseconds()) - avgNs
			jitterSum += d * d
		}
		agg.Jitter = time.Duration(math.Sqrt(jitterSum/float64(len(allLatencies)))) * time.Nanosecond
	}

	return agg
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(p)/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
