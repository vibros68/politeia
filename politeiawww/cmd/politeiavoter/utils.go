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
	// Extract seconds with a single decimal place
	seconds := float64(d) / float64(time.Second)

	// Format the seconds with one decimal place
	formattedSeconds := fmt.Sprintf("%.1f", seconds)

	// Return the formatted duration string
	return formattedSeconds + "s"
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
