package model

import (
	"fmt"
	"math"
	"sort"
)

type FleetRepairOptions struct {
	ChargeSearchBackSteps int
	UseLinePools          bool
	PoolBuffer            int
}

func DefaultFleetRepairOptions(inst *Instance) FleetRepairOptions {
	back := 240
	if inst.T < back {
		back = inst.T
	}
	return FleetRepairOptions{
		ChargeSearchBackSteps: back,
		UseLinePools:          true,
		PoolBuffer:            1,
	}
}

// RebuildFleet keeps backward compatibility and uses strong default options.
func (s *Solution) RebuildFleet(inst *Instance) error {
	return s.RebuildFleetWithOptions(inst, DefaultFleetRepairOptions(inst))
}

// RebuildFleetWithOptions greedily assigns each departure to vehicles and inserts full charges.
// Stronger rules include: line pools, SOC-priority candidate selection, and backward charge search.
func (s *Solution) RebuildFleetWithOptions(inst *Instance, opt FleetRepairOptions) error {
	N, T, L := len(inst.Vehicles), inst.T, len(inst.Lines)
	initFleetState(s, inst)

	type ev struct{ l, t int }
	events := make([]ev, 0, 1024)
	for l := 0; l < L; l++ {
		sort.Ints(s.Dep[l])
		s.Dep[l] = uniqueTimes(s.Dep[l])
		for _, t := range s.Dep[l] {
			if t >= 0 && t < T {
				events = append(events, ev{l: l, t: t})
			}
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		return events[i].l < events[j].l
	})

	var inPool [][]bool
	if opt.UseLinePools {
		inPool = buildLinePools(inst, opt.PoolBuffer)
	}

	for _, e := range events {
		if ok := s.assignOneWithOptions(inst, e.l, e.t, opt, inPool); !ok {
			return fmt.Errorf("no feasible vehicle for line=%d depart=%d", e.l, e.t)
		}
	}

	if err := s.ValidateFleet(inst); err != nil {
		return err
	}
	if len(s.AssignedLine) != N {
		return fmt.Errorf("fleet state size mismatch")
	}
	return nil
}

func initFleetState(s *Solution, inst *Instance) {
	N, T := len(inst.Vehicles), inst.T
	if len(s.AssignedLine) != N {
		s.AssignedLine = make([]int, N)
	}
	if len(s.Trips) != N {
		s.Trips = make([][]Trip, N)
	}
	if len(s.Charges) != N {
		s.Charges = make([][]int, N)
	}
	if len(s.Occ) != N {
		s.Occ = make([][]bool, N)
	}
	if len(s.SOC) != N {
		s.SOC = make([][]float64, N)
	}
	if len(s.ChargerUse) != T {
		s.ChargerUse = make([]int, T)
	}
	for t := 0; t < T; t++ {
		s.ChargerUse[t] = 0
	}
	for n := 0; n < N; n++ {
		s.AssignedLine[n] = -1
		s.Trips[n] = s.Trips[n][:0]
		s.Charges[n] = s.Charges[n][:0]
		if len(s.Occ[n]) != T {
			s.Occ[n] = make([]bool, T)
		} else {
			for t := 0; t < T; t++ {
				s.Occ[n][t] = false
			}
		}
		if len(s.SOC[n]) != T+1 {
			s.SOC[n] = make([]float64, T+1)
		}
		for t := 0; t <= T; t++ {
			s.SOC[n][t] = inst.Vehicles[n].Emax
		}
	}
}

func (s *Solution) assignOneWithOptions(inst *Instance, l, t int, opt FleetRepairOptions, inPool [][]bool) bool {
	N := len(inst.Vehicles)
	assigned := make([]int, 0, N)
	unassigned := make([]int, 0, N)
	for n := 0; n < N; n++ {
		if inPool != nil && !inPool[l][n] {
			continue
		}
		if s.AssignedLine[n] == l {
			assigned = append(assigned, n)
		} else if s.AssignedLine[n] == -1 {
			unassigned = append(unassigned, n)
		}
	}
	sort.Slice(assigned, func(i, j int) bool { return s.SOC[assigned[i]][t] > s.SOC[assigned[j]][t] })
	sort.Slice(unassigned, func(i, j int) bool { return s.SOC[unassigned[i]][t] > s.SOC[unassigned[j]][t] })
	cands := append(assigned, unassigned...)

	for _, n := range cands {
		if !s.freeForTrip(inst, n, l, t) {
			continue
		}
		need := inst.Lines[l].Energy + inst.Vehicles[n].Emin
		if s.SOC[n][t] >= need-1e-9 {
			s.insertTrip(inst, n, l, t)
			if s.AssignedLine[n] == -1 {
				s.AssignedLine[n] = l
			}
			return true
		}
		tc, ok := s.findChargeStartBefore(inst, n, t, opt.ChargeSearchBackSteps)
		if !ok {
			continue
		}
		s.insertCharge(inst, n, tc)
		if s.SOC[n][t] < need-1e-9 {
			continue
		}
		s.insertTrip(inst, n, l, t)
		if s.AssignedLine[n] == -1 {
			s.AssignedLine[n] = l
		}
		return true
	}
	return false
}

func buildLinePools(inst *Instance, buffer int) [][]bool {
	L, N := len(inst.Lines), len(inst.Vehicles)
	if buffer < 0 {
		buffer = 0
	}
	target := make([]int, L)
	total := 0
	for l := 0; l < L; l++ {
		base := int(math.Ceil(float64(inst.Lines[l].TripStep) / float64(maxInt(inst.HeadwayMax, 1))))
		target[l] = maxInt(1, base+buffer)
		total += target[l]
	}
	if total > N {
		scale := float64(N) / float64(total)
		total = 0
		for l := 0; l < L; l++ {
			target[l] = maxInt(1, int(math.Floor(float64(target[l])*scale)))
			total += target[l]
		}
		for total > N {
			li := argMax(target)
			if target[li] > 1 {
				target[li]--
				total--
			} else {
				break
			}
		}
	}
	type vi struct {
		idx  int
		emax float64
	}
	vs := make([]vi, N)
	for i := 0; i < N; i++ {
		vs[i] = vi{idx: i, emax: inst.Vehicles[i].Emax}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].emax > vs[j].emax })
	inPool := make([][]bool, L)
	for l := 0; l < L; l++ {
		inPool[l] = make([]bool, N)
	}
	ptr := 0
	remaining := append([]int(nil), target...)
	for {
		done := true
		for l := 0; l < L; l++ {
			if remaining[l] > 0 {
				done = false
				if ptr >= N {
					break
				}
				inPool[l][vs[ptr].idx] = true
				remaining[l]--
				ptr++
			}
		}
		if done || ptr >= N {
			break
		}
	}
	for ptr < N {
		inPool[ptr%L][vs[ptr].idx] = true
		ptr++
	}
	return inPool
}

func (s *Solution) insertTrip(inst *Instance, n, l, t int) {
	tripLen := inst.Lines[l].TripStep
	end := t + tripLen
	if end > inst.T {
		end = inst.T
	}
	s.Trips[n] = append(s.Trips[n], Trip{LineID: l, Start: t})
	for tt := t; tt < end; tt++ {
		s.Occ[n][tt] = true
	}
	need := inst.Lines[l].Energy
	s.applyEnergyDrop(n, t, need)
}

func (s *Solution) insertCharge(inst *Instance, n, tc int) {
	tau := inst.ChargeLen
	end := tc + tau
	if end > inst.T {
		end = inst.T
	}
	s.Charges[n] = append(s.Charges[n], tc)
	for tt := tc; tt < end; tt++ {
		s.Occ[n][tt] = true
		s.ChargerUse[tt]++
	}
	for tt := tc; tt <= inst.T; tt++ {
		s.SOC[n][tt] = inst.Vehicles[n].Emax
	}
}

func (s *Solution) applyEnergyDrop(n, t int, need float64) {
	for tt := t + 1; tt < len(s.SOC[n]); tt++ {
		s.SOC[n][tt] -= need
	}
}

func (s *Solution) freeForTrip(inst *Instance, n, l, t int) bool {
	if t < 0 || t >= inst.T {
		return false
	}
	end := t + inst.Lines[l].TripStep
	if end > inst.T {
		end = inst.T
	}
	for tt := t; tt < end; tt++ {
		if s.Occ[n][tt] {
			return false
		}
	}
	return true
}

func (s *Solution) findChargeStartBefore(inst *Instance, n, t, backSteps int) (int, bool) {
	if backSteps <= 0 {
		backSteps = inst.T
	}
	latest := t - inst.ChargeLen
	if latest < 0 {
		return -1, false
	}
	start := t - backSteps
	if start < 0 {
		start = 0
	}
	for tc := latest; tc >= start; tc-- {
		end := tc + inst.ChargeLen
		if end > inst.T {
			continue
		}
		ok := true
		for tt := tc; tt < end; tt++ {
			if s.Occ[n][tt] || s.ChargerUse[tt] >= inst.Chargers {
				ok = false
				break
			}
		}
		if ok {
			return tc, true
		}
	}
	return -1, false
}

func (s *Solution) ValidateFleet(inst *Instance) error {
	N, T := len(inst.Vehicles), inst.T
	for n := 0; n < N; n++ {
		for t := 0; t <= T; t++ {
			if s.SOC[n][t] < inst.Vehicles[n].Emin-1e-9 {
				return fmt.Errorf("vehicle %d soc below Emin at t=%d", n, t)
			}
			if s.SOC[n][t] > inst.Vehicles[n].Emax+1e-9 {
				return fmt.Errorf("vehicle %d soc above Emax at t=%d", n, t)
			}
		}
	}
	for t := 0; t < T; t++ {
		if s.ChargerUse[t] > inst.Chargers {
			return fmt.Errorf("charger overflow at t=%d", t)
		}
	}
	return nil
}

func uniqueTimes(a []int) []int {
	if len(a) == 0 {
		return a
	}
	out := a[:1]
	for i := 1; i < len(a); i++ {
		if a[i] != a[i-1] {
			out = append(out, a[i])
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}

func argMax(a []int) int {
	bestI, bestV := 0, a[0]
	for i := 1; i < len(a); i++ {
		if a[i] > bestV {
			bestI, bestV = i, a[i]
		}
	}
	return bestI
}
