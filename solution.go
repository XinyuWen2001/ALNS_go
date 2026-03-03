package solution

import "alns_go/internal/instance"

type Trip struct {
	L int
	T int
}

type Solution struct {
	Dep [][]int // Dep[l] sorted unique departure times  [n][t]

	AssignedLine []int // -1 none, else line index
	Trips        [][]Trip
	Charges      [][]int

	Occ        [][]bool
	ChargerUse []int
	SOC        [][]float64 // [n][t], length T+1
}

func NewEmpty(inst *instance.Instance) *Solution {
	L := len(inst.Lines)
	N := len(inst.Vehicles)
	T := inst.T

	s := &Solution{
		Dep: make([][]int, L),

		AssignedLine: make([]int, N),
		Trips:        make([][]Trip, N),
		Charges:      make([][]int, N),

		Occ:        make([][]bool, N),
		ChargerUse: make([]int, T),
		SOC:        make([][]float64, N),
	}

	for n := 0; n < N; n++ {
		s.AssignedLine[n] = -1
		s.Occ[n] = make([]bool, T)
		s.SOC[n] = make([]float64, T+1)
	}
	return s
}
