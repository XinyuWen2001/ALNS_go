package alns

import (
	"math"
	"math/rand"
)

func roulette(weights []float64, rng *rand.Rand) int {
	sum := 0.0
	for _, w := range weights {
		sum += w
	}
	if sum <= 0 {
		return rng.Intn(len(weights))
	}
	r := rng.Float64() * sum
	acc := 0.0
	for i, w := range weights {
		acc += w
		if r <= acc {
			return i
		}
	}
	return len(weights) - 1
}

func acceptSA(delta, temp float64, rng *rand.Rand) bool {
	if delta >= 0 {
		return true
	}
	if temp <= 0 {
		return false
	}
	return rng.Float64() < math.Exp(delta/temp)
}
