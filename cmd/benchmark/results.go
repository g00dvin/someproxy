package main

import (
	"fmt"
	"strings"
	"time"
)

func printResults(results map[string][]BenchResult, sizeMB int, conns int) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("  Benchmark Results")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  Data size: %d MB per run | TURN connections: %d\n", sizeMB, conns)
	fmt.Println(strings.Repeat("-", 60))

	var aggregated []AggregatedResult

	order := []string{"raw-dtls"}
	for _, mode := range order {
		runs, ok := results[mode]
		if !ok || len(runs) == 0 {
			continue
		}
		agg := aggregate(runs)
		aggregated = append(aggregated, agg)
		printMode(agg)
	}

	if len(aggregated) > 1 {
		fmt.Println(strings.Repeat("-", 60))
		fmt.Println("  Comparison (vs raw-dtls)")
		fmt.Println(strings.Repeat("-", 60))
		baseline := aggregated[0]
		for _, agg := range aggregated[1:] {
			tpDiff := (agg.AvgThroughput - baseline.AvgThroughput) / baseline.AvgThroughput * 100
			sign := "+"
			if tpDiff < 0 {
				sign = ""
			}
			fmt.Printf("  %-28s %s%.1f%% throughput", agg.Mode, sign, tpDiff)
			if baseline.AvgLatency > 0 && agg.AvgLatency > 0 {
				latDiff := float64(agg.AvgLatency-baseline.AvgLatency) / float64(baseline.AvgLatency) * 100
				latSign := "+"
				if latDiff < 0 {
					latSign = ""
				}
				fmt.Printf(", %s%.1f%% latency", latSign, latDiff)
			}
			fmt.Println()
		}
	}
	fmt.Println(strings.Repeat("=", 60))
}

func printMode(agg AggregatedResult) {
	fmt.Println()
	fmt.Printf("  --- %s (%d runs) ---\n", agg.Mode, agg.Runs)
	fmt.Printf("  Setup Time:  %v\n", agg.AvgSetupTime.Round(time.Millisecond))
	fmt.Printf("  Throughput:  %.2f Mbps +/- %.2f (min %.2f, max %.2f)\n",
		agg.AvgThroughput, agg.StdThroughput, agg.MinThroughput, agg.MaxThroughput)
	if agg.AvgLatency > 0 {
		fmt.Printf("  Avg Latency: %v\n", agg.AvgLatency.Round(time.Millisecond))
		fmt.Printf("  P50 Latency: %v\n", agg.P50Latency.Round(time.Millisecond))
		fmt.Printf("  P95 Latency: %v\n", agg.P95Latency.Round(time.Millisecond))
		fmt.Printf("  P99 Latency: %v\n", agg.P99Latency.Round(time.Millisecond))
		fmt.Printf("  Jitter:      %v\n", agg.Jitter.Round(time.Millisecond))
	}
}
