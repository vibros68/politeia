package main

import (
	"fmt"
	"math"
	"sort"
)

type (
	vote struct{}
)

func displayChart(votes []int, rows int) {
	if len(votes) == 0 {
		return
	}
	var ch, sorted []int
	var scale float64

	ch, sorted = make([]int, len(votes)), make([]int, len(votes))
	copy(sorted, votes)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i] > sorted[j]
	})

	max := sorted[0]

	scale = float64(rows) / float64(max)
	if rows == 0 || rows > max {
		scale = 1
	}

	for k, c := range votes {
		scaled := int(math.Round(float64(c) * scale))
		ch[k] = scaled
	}

	layout := make([][]*vote, len(ch))
	for k, c := range ch {
		bar := make([]*vote, rows)
		for i := 0; i < rows; i++ {
			if c > 0 {
				bar[i] = &vote{}
				c--
			} else {
				bar[i] = nil
			}
		}
		layout[k] = bar
	}

	count := 0
	printIndex := rows - 1
	for i := 0; i < rows; i++ {
		for _, bar := range layout {
			l := bar[printIndex]
			count++
			if l == nil {
				fmt.Printf(".")
			} else {
				fmt.Printf("#")
			}
		}
		fmt.Println()
		printIndex--
	}
}
