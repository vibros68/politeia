package main

import (
	"fmt"
	"time"
)

const timeLayoutFormat = "2006-01-02 15:04:05"

func viewTime(t time.Time) string {
	return t.Format(timeLayoutFormat)
}

func formatDuration(d time.Duration) string {
	if d > time.Second {
		seconds := float64(d) / float64(time.Second)
		return fmt.Sprintf("%.1fs", seconds)
	}
	miliSecond := float64(d) / float64(time.Millisecond)
	return fmt.Sprintf("%.1fms", miliSecond)
}

func findMax(nums []int) int {
	var max int
	for _, num := range nums {
		if max < num {
			max = num
		}
	}
	return max
}
