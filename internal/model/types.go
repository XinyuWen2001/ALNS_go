package model

import "math"

type Vehicle struct {
	ID   int
	Emin float64
	Emax float64
}

type Line struct {
	ID       int
	TripStep int
	Energy   float64
	FT       []float64
	OpCost   []float64
}

type Instance struct {
	Name       string
	T          int
	HeadwayMin int
	HeadwayMax int
	ChargeLen  int
	Chargers   int
	Alpha      float64
	Beta       float64
	Gamma      float64
	Vehicles   []Vehicle
	Lines      []Line
}

type Trip struct {
	LineID int
	Start  int
}

type Solution struct {
	Dep          [][]int
	AssignedLine []int
	Trips        [][]Trip
	Charges      [][]int
	SOC          [][]float64
	Occ          [][]bool
	ChargerUse   []int
}

type Eval struct {
	Objective   float64
	Revenue     float64
	OpCost      float64
	ChargeCost  float64
	TopUpCost   float64
	TotalDepart int
}

func NewEmptySolution(inst *Instance) *Solution {
	L, N, T := len(inst.Lines), len(inst.Vehicles), inst.T
	s := &Solution{
		Dep:          make([][]int, L),
		AssignedLine: make([]int, N),
		Trips:        make([][]Trip, N),
		Charges:      make([][]int, N),
		SOC:          make([][]float64, N),
		Occ:          make([][]bool, N),
		ChargerUse:   make([]int, T),
	}
	for n := 0; n < N; n++ {
		s.AssignedLine[n] = -1
		s.SOC[n] = make([]float64, T+1)
		s.Occ[n] = make([]bool, T)
		s.SOC[n][0] = inst.Vehicles[n].Emax
	}
	return s
}

func DeepCopy(s *Solution) *Solution {
	cp := &Solution{
		Dep:          make([][]int, len(s.Dep)),
		AssignedLine: append([]int(nil), s.AssignedLine...),
		Trips:        make([][]Trip, len(s.Trips)),
		Charges:      make([][]int, len(s.Charges)),
		SOC:          make([][]float64, len(s.SOC)),
		Occ:          make([][]bool, len(s.Occ)),
		ChargerUse:   append([]int(nil), s.ChargerUse...),
	}
	for i := range s.Dep {
		cp.Dep[i] = append([]int(nil), s.Dep[i]...)
	}
	for i := range s.Trips {
		cp.Trips[i] = append([]Trip(nil), s.Trips[i]...)
	}
	for i := range s.Charges {
		cp.Charges[i] = append([]int(nil), s.Charges[i]...)
	}
	for i := range s.SOC {
		cp.SOC[i] = append([]float64(nil), s.SOC[i]...)
	}
	for i := range s.Occ {
		cp.Occ[i] = append([]bool(nil), s.Occ[i]...)
	}
	return cp
}

func (s *Solution) Evaluate(inst *Instance) Eval {
	res := Eval{}
	for l := range inst.Lines {
		for _, t := range s.Dep[l] {
			res.TotalDepart++
			res.Revenue += inst.Alpha * inst.Lines[l].FT[t]
			if t < len(inst.Lines[l].OpCost) {
				res.OpCost += inst.Lines[l].OpCost[t]
			}
		}
	}
	for n := range s.Charges {
		res.ChargeCost += float64(len(s.Charges[n])) * inst.Beta
		if len(s.SOC[n]) > 0 {
			end := s.SOC[n][len(s.SOC[n])-1]
			res.TopUpCost += inst.Gamma * math.Max(0, inst.Vehicles[n].Emax-end)
		}
	}
	res.Objective = res.Revenue - res.OpCost - res.ChargeCost - res.TopUpCost
	return res
}
