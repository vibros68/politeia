package main

import "time"

const timeLayoutFormat = "2006-01-02 15:04:05"

func viewTime(t time.Time) string {
	return t.Format(timeLayoutFormat)
}
