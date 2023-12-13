package main

import (
	"fmt"
	"sync"
	"time"
)

type mirrorCache struct {
	token           string
	last            time.Time
	refreshDur      time.Duration
	mx              sync.Mutex
	yesBits, noBits int
	me, them        VoteStats
	p               *piv
}

func newMirrorCache(token string, refreshDuration time.Duration, p *piv) *mirrorCache {
	return &mirrorCache{
		token:      token,
		last:       time.Time{},
		refreshDur: refreshDuration,
		mx:         sync.Mutex{},
		p:          p,
	}
}

func (mc *mirrorCache) getVoteBit() string {
	mc.mx.Lock()
	defer mc.mx.Unlock()
	if time.Since(mc.last) > mc.refreshDur {
		me, them, err := mc.p.getTotalVotes(mc.token)
		if err == nil {
			mc.me = *me
			mc.them = *them
			mc.yesBits = 0
			mc.noBits = 0
		}
	}
	fmt.Printf("me rate: %.2f \n", mc.me.Rate())
	fmt.Printf("them rate: %.2f \n", mc.them.Rate())
	if mc.me.Rate() > mc.them.Rate() {
		return VoteBitNo
	}
	return VoteBitYes
}

func (mc *mirrorCache) updateVoteBit(voteBit string) {
	if voteBit == VoteBitYes {
		mc.me.Yes++
		mc.me.Yet--
	}
	if voteBit == VoteBitNo {
		mc.me.No++
		mc.me.Yet--
	}
}
