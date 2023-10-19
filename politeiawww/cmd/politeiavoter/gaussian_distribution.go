package main

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"time"
)

type Gaussian struct {
	sigma float64
	mu    float64
	from  time.Time
	to    time.Time
	// cache
	maxFx  float64
	minFx  float64
	max    int64
	middle float64
}

func (g *Gaussian) MaxFx() float64 {
	return g.maxFx
}

func NewGaussian(sigma, mu float64, from, to time.Time) (*Gaussian, error) {
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
