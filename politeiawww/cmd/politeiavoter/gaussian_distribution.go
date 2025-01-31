package main

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"time"
)

const gaussianMaxX = 5

type Gaussian struct {
	sigma float64
	mu    float64
	from  time.Time
	to    time.Time
	// cache
	maxFx        float64
	minFx        float64
	max          int64
	middle       float64
	xFrame       float64
	yFrame       float64
	timeFrame    int64
	XGraph       []int
	YGraph       []int
	YesTimeGraph []int
	NoTimeGraph  []int
	chartLen     int
}

func (g *Gaussian) MaxFx() float64 {
	return g.maxFx
}

func NewGaussian(sigma, mu float64, from, to time.Time, chartLen int) (*Gaussian, error) {
	g := Gaussian{
		sigma: sigma,
		mu:    mu,
		from:  from,
		to:    to,
	}
	diff := to.Unix() - from.Unix()
	if diff <= 0 {
		return nil, fmt.Errorf("from time must be smaller than to time")
	}
	g.maxFx = g.Fx(0)
	g.minFx = g.Fx(1)
	g.max = diff
	g.middle = float64(diff) / 2
	g.xFrame = gaussianMaxX * 2 / float64(chartLen)
	g.yFrame = (g.maxFx - g.minFx) / float64(chartLen)
	g.timeFrame = diff / int64(chartLen)
	g.XGraph = make([]int, chartLen)
	g.YGraph = make([]int, chartLen)
	g.YesTimeGraph = make([]int, chartLen)
	g.NoTimeGraph = make([]int, chartLen)
	return &g, nil
}

func (g *Gaussian) Fx(x float64) float64 {
	return 1 / (g.sigma * math.Sqrt(2*math.Pi)) * math.Exp(-0.5*math.Pow((x-g.mu)/g.sigma, 2))
}

func (g *Gaussian) RandomTime() (time.Time, error) {
	res, err := rand.Int(rand.Reader, big.NewInt(g.max))
	if err != nil {
		return time.Time{}, err
	}
	x := (float64(res.Int64()) - g.middle) / g.middle
	y := g.Fx(x)
	percent := (y - g.minFx) / (g.maxFx - g.minFx)
	unixDiff := percent * g.middle
	var unix int64
	if x > 0 {
		unix = g.from.Unix() + int64(g.middle+unixDiff)
	} else {
		unix = g.from.Unix() + int64(g.middle-unixDiff)
	}
	return time.Unix(unix, 0), nil
}

func (g *Gaussian) GenerateTime(votesToCast []*voteAlarm, milestone time.Time) ([]*voteAlarm, error) {
	if milestone.Unix() > g.to.Unix() {
		return nil, fmt.Errorf("milestone time is out of range")
	}
	var index int
	var timeCasts = len(votesToCast)
	var timeSlice = make([]*voteAlarm, timeCasts)
	for {
		res, err := rand.Int(rand.Reader, big.NewInt(g.max))
		if err != nil {
			return nil, err
		}
		x := (float64(res.Int64()) - g.middle) / g.middle * gaussianMaxX
		frameIndex := int64((x + gaussianMaxX) / g.xFrame)
		frameTime := time.Unix(g.from.Unix()*(g.timeFrame*(frameIndex+1)), 0)
		if frameTime.Unix() <= milestone.Unix() {
			continue
		}
		g.XGraph[frameIndex] = g.XGraph[frameIndex] + 1
		y := g.Fx(x)
		if y == 0 {
			continue
		}
		randCheck, err := rand.Int(rand.Reader, big.NewInt(g.timeFrame))
		if err != nil {
			return nil, err
		}
		if float64(randCheck.Uint64())/float64(g.timeFrame) < y/g.maxFx {
			t, err := g.timePoint(frameIndex)
			if t.Unix() <= milestone.Unix() {
				continue
			}
			if err != nil {
				return nil, err
			}
			voteToCart := votesToCast[index]
			voteToCart.At = t
			timeSlice[index] = voteToCart
			index++
			if voteToCart.Vote.VoteBit == VoteBitYes {
				g.YesTimeGraph[frameIndex] = g.YesTimeGraph[frameIndex] + 1
			} else {
				g.NoTimeGraph[frameIndex] = g.NoTimeGraph[frameIndex] + 1
			}
		}
		if index == timeCasts {
			break
		}
	}
	return timeSlice, nil
}

func (g *Gaussian) timePoint(frameIndex int64) (time.Time, error) {
	randPlus, err := rand.Int(rand.Reader, big.NewInt(g.timeFrame))
	if err != nil {
		return time.Time{}, err
	}
	tUnix := g.from.Unix() + frameIndex*g.timeFrame + randPlus.Int64()
	return time.Unix(tUnix, 0), nil
}
