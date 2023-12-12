package main

import (
	"sync"
	"time"
)

type mirrorCache struct {
	last       time.Time
	refreshDur time.Duration
	mx         sync.Mutex
	yesBits    int
	noBits     int
}

func newMirrorCache(refreshDuration time.Duration) *mirrorCache {
	return &mirrorCache{
		last:       time.Time{},
		refreshDur: refreshDuration,
		mx:         sync.Mutex{},
	}
}

func (mc *mirrorCache) getVoteBit() string {
	return voteOptionYes
}

func (mc *mirrorCache) updateVoteBit(voteBit string) string {
	return voteOptionYes
}
