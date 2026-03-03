package operators

import (
	"fmt"
	"math"
	"sort"

	"alns_go/internal/instance"
	"alns_go/internal/solution"
)

type FleetRepairOptions struct {
	ChargeSearchBackSteps int

	// If true: build a per-line vehicle pool upfront to prevent line starvation.
	UseLinePools bool

	// Buffer vehicles per line (in addition to ceil(trip/headway)).
	// Suggest 1 for your full-charge + charging time setting.
	PoolBuffer int
}

type depEvent struct {
	L int
	T int
}

type vehInfo struct {
	Idx  int
	Emax float64
}

// FleetRepairGreedy assigns vehicles and inserts charges to serve all departures in Dep.
// It processes departures in GLOBAL time order.
// NEW: can use line pools to avoid starvation under single-line constraint.
func FleetRepairGreedy(inst *instance.Instance, sol *solution.Solution, opt FleetRepairOptions) error {
	L := len(inst.Lines)
	N := len(inst.Vehicles)

	// reset fleet layer
	for n := 0; n < N; n++ {
		sol.AssignedLine[n] = -1
		sol.Trips[n] = sol.Trips[n][:0]
		sol.Charges[n] = sol.Charges[n][:0]
	}

	// normalize Dep and build global events
	events := make([]depEvent, 0, 4096)
	for l := 0; l < L; l++ {
		dep := append([]int(nil), sol.Dep[l]...)
		sort.Ints(dep)
		// optional: unique
		dep = uniqueSorted(dep)
		sol.Dep[l] = dep
		for _, t := range dep {
			events = append(events, depEvent{L: l, T: t})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].T != events[j].T {
			return events[i].T < events[j].T
		}
		return events[i].L < events[j].L
	})

	// build line pools if enabled
	var linePools [][]int
	var inPool [][]bool // inPool[l][n] = true if vehicle n belongs to line l pool
	if opt.UseLinePools {
		linePools = buildLinePools(inst, opt.PoolBuffer)
		inPool = make([][]bool, L)
		for l := 0; l < L; l++ {
			inPool[l] = make([]bool, N)
			for _, n := range linePools[l] {
				inPool[l][n] = true
			}
		}
	}

	// initial rebuild
	if err := sol.RebuildAll(inst); err != nil {
		return err
	}

	// assign in global time order
	for _, ev := range events {
		ok, err := assignOneDeparture(inst, sol, ev.L, ev.T, opt, inPool)
		if err != nil {
			return fmt.Errorf("fleet repair failed at line=%d dep_t=%d: %w", ev.L, ev.T, err)
		}
		if !ok {
			// return detailed diagnosis
			diag := diagnoseFailure(inst, sol, ev.L, ev.T, opt, inPool)
			return fmt.Errorf("fleet repair failed at line=%d dep_t=%d: no feasible vehicle. %s", ev.L, ev.T, diag)
		}
	}

	return sol.RebuildAll(inst)
}

func assignOneDeparture(inst *instance.Instance, sol *solution.Solution, l, t int, opt FleetRepairOptions, inPool [][]bool) (bool, error) {
	N := len(inst.Vehicles)

	// candidate order:
	// 1) vehicles already assigned to line l
	// 2) unassigned vehicles
	// [OPTIMIZATION]: Sort by SOC descending to prefer vehicles that don't need charging!
	
	assignedCandidates := make([]int, 0, N)
	unassignedCandidates := make([]int, 0, N)

	for n := 0; n < N; n++ {
		// Filter by pool constraints first
		if inPool != nil && !inPool[l][n] {
			continue
		}

		if sol.AssignedLine[n] == l {
			assignedCandidates = append(assignedCandidates, n)
		} else if sol.AssignedLine[n] == -1 {
			unassignedCandidates = append(unassignedCandidates, n)
		}
	}

	// Helper to sort by SOC descending at time t
	sortBySOC := func(cands []int) {
		sort.Slice(cands, func(i, j int) bool {
			n1, n2 := cands[i], cands[j]
			// Higher SOC first
			return sol.SOC[n1][t] > sol.SOC[n2][t]
		})
	}

	sortBySOC(assignedCandidates)
	sortBySOC(unassignedCandidates)

	// Merge lists: prefer already assigned vehicles (continuity) then new ones (expansion)
	candidates := append(assignedCandidates, unassignedCandidates...)

	for _, n := range candidates {
		// single-line restriction
		if sol.AssignedLine[n] != -1 && sol.AssignedLine[n] != l {
			continue
		}

		// trip occupancy feasibility
		if !isVehicleFreeForTrip(inst, sol, n, l, t) {
			continue
		}

		eminN := inst.Vehicles[n].Emin
		need := inst.Lines[l].EnergyKWh + eminN

		// enough SOC -> direct depart
		if sol.SOC[n][t] >= need-1e-9 {
			commit := func() error {
				insertTrip(sol, n, l, t)
				if sol.AssignedLine[n] == -1 {
					sol.AssignedLine[n] = l
				}
				return sol.RebuildAll(inst)
			}
			if err := commitWithRollback(inst, sol, n, commit); err == nil {
				return true, nil
			}
			continue
		}

		// try one full charge before t
		tc, found := findChargeStartBefore(inst, sol, n, t, opt.ChargeSearchBackSteps)
		if !found {
			continue
		}

		commit := func() error {
			insertCharge(sol, n, tc)
			if err := sol.RebuildAll(inst); err != nil {
				return err
			}
			// Re-check SOC after charge
			if sol.SOC[n][t] < need-1e-9 {
				return fmt.Errorf("charge inserted but SOC still insufficient at t=%d (soc=%.3f need=%.3f)", t, sol.SOC[n][t], need)
			}
			insertTrip(sol, n, l, t)
			if sol.AssignedLine[n] == -1 {
				sol.AssignedLine[n] = l
			}
			return sol.RebuildAll(inst)
		}

		if err := commitWithRollback(inst, sol, n, commit); err == nil {
			return true, nil
		}
	}

	return false, nil
}

func diagnoseFailure(inst *instance.Instance, sol *solution.Solution, l, t int, opt FleetRepairOptions, inPool [][]bool) string {
	N := len(inst.Vehicles)

	// counts
	totalCand := 0
	freeTrip := 0
	socEnough := 0
	chargeFeasible := 0

	// pool filtered candidates (assigned to l or unassigned)
	for n := 0; n < N; n++ {
		if sol.AssignedLine[n] != -1 && sol.AssignedLine[n] != l {
			continue
		}
		if inPool != nil && !inPool[l][n] {
			continue
		}
		// only consider (assigned to l) or (unassigned)
		if sol.AssignedLine[n] == l || sol.AssignedLine[n] == -1 {
			totalCand++

			if isVehicleFreeForTrip(inst, sol, n, l, t) {
				freeTrip++
				need := inst.Lines[l].EnergyKWh + inst.Vehicles[n].Emin
				if sol.SOC[n][t] >= need-1e-9 {
					socEnough++
				} else {
					_, ok := findChargeStartBefore(inst, sol, n, t, opt.ChargeSearchBackSteps)
					if ok {
						chargeFeasible++
					}
				}
			}
		}
	}

	return fmt.Sprintf("diagnose: candidates=%d, freeForTrip=%d, socEnough=%d, chargeWindowFound=%d (backSteps=%d)",
		totalCand, freeTrip, socEnough, chargeFeasible, opt.ChargeSearchBackSteps)
}

// -------------------------
// Line pool construction
// -------------------------

func buildLinePools(inst *instance.Instance, buffer int) [][]int {
	L := len(inst.Lines)
	N := len(inst.Vehicles)
	if buffer < 0 {
		buffer = 0
	}

	// target vehicles per line:
	// base = ceil(tripSteps / headwayMax) + buffer
	target := make([]int, L)
	sumTarget := 0
	for l := 0; l < L; l++ {
		base := int(math.Ceil(float64(inst.Lines[l].TripSteps) / float64(inst.HeadwayMax)))
		target[l] = base + buffer
		sumTarget += target[l]
	}

	// if sumTarget > N, scale down proportionally but keep >=1
	if sumTarget > N {
		scale := float64(N) / float64(sumTarget)
		sumTarget = 0
		for l := 0; l < L; l++ {
			target[l] = maxInt(1, int(math.Floor(float64(target[l])*scale)))
			sumTarget += target[l]
		}
		// if still > N, reduce from largest target
		for sumTarget > N {
			li := argMax(target)
			if target[li] > 1 {
				target[li]--
				sumTarget--
			} else {
				break
			}
		}
	}

	// vehicles sorted by Emax descending (give stronger batteries first)
	veh := make([]vehInfo, 0, N)
	for n := 0; n < N; n++ {
		veh = append(veh, vehInfo{Idx: n, Emax: inst.Vehicles[n].Emax})
	}
	sort.Slice(veh, func(i, j int) bool { return veh[i].Emax > veh[j].Emax })

	pools := make([][]int, L)
	assigned := make([]bool, N)

	// allocate targets using round-robin over lines with remaining demand
	ptr := 0
	remaining := append([]int(nil), target...)
	for {
		done := true
		for l := 0; l < L; l++ {
			if remaining[l] > 0 {
				done = false
				// find next unassigned vehicle
				for ptr < N && assigned[veh[ptr].Idx] {
					ptr++
				}
				if ptr >= N {
					break
				}
				n := veh[ptr].Idx
				assigned[n] = true
				pools[l] = append(pools[l], n)
				remaining[l]--
				ptr++
			}
		}
		if done || ptr >= N {
			break
		}
	}

	// any leftover vehicles (if sumTarget < N): distribute to lines with largest tripSteps (or simply round-robin)
	left := make([]int, 0)
	for _, v := range veh {
		if !assigned[v.Idx] {
			left = append(left, v.Idx)
		}
	}
	// distribute leftovers to lines with largest TripSteps first
	order := make([]int, L)
	for l := 0; l < L; l++ {
		order[l] = l
	}
	sort.Slice(order, func(i, j int) bool { return inst.Lines[order[i]].TripSteps > inst.Lines[order[j]].TripSteps })

	for i, n := range left {
		l := order[i%L]
		pools[l] = append(pools[l], n)
	}

	return pools
}

// -------------------------
// Helpers
// -------------------------

func commitWithRollback(inst *instance.Instance, sol *solution.Solution, n int, commit func() error) error {
	origAssigned := sol.AssignedLine[n]
	origTrips := append([]solution.Trip(nil), sol.Trips[n]...)
	origCharges := append([]int(nil), sol.Charges[n]...)

	if err := commit(); err != nil {
		sol.AssignedLine[n] = origAssigned
		sol.Trips[n] = origTrips
		sol.Charges[n] = origCharges
		_ = sol.RebuildAll(inst)
		return err
	}
	return nil
}

func insertTrip(sol *solution.Solution, n, l, t int) {
	sol.Trips[n] = append(sol.Trips[n], solution.Trip{L: l, T: t})
	sort.Slice(sol.Trips[n], func(i, j int) bool { return sol.Trips[n][i].T < sol.Trips[n][j].T })
}

func insertCharge(sol *solution.Solution, n, tc int) {
	sol.Charges[n] = append(sol.Charges[n], tc)
	sort.Ints(sol.Charges[n])
}

func isVehicleFreeForTrip(inst *instance.Instance, sol *solution.Solution, n, l, t int) bool {
	T := inst.T
	if t < 0 || t >= T {
		return false
	}
	end := t + inst.Lines[l].TripSteps
	if end > T {
		end = T
	}
	for tt := t; tt < end; tt++ {
		if sol.Occ[n][tt] {
			return false
		}
	}
	return true
}

func findChargeStartBefore(inst *instance.Instance, sol *solution.Solution, n, t int, backSteps int) (int, bool) {
	T := inst.T
	tau := inst.ChargeLen

	if backSteps <= 0 {
		backSteps = 480
	}

	// charge interval is [tc, tc+tau)
	// to avoid overlap with trip starting at t: require tc+tau <= t  => tc <= t - tau
	latest := t - tau
	if latest < 0 {
		return -1, false
	}

	startMin := t - backSteps
	if startMin < 0 {
		startMin = 0
	}
	if startMin > latest {
		return -1, false
	}

	// Prefer latest feasible tc
	for tc := latest; tc >= startMin; tc-- {
		end := tc + tau
		if end > T {
			continue
		}
		ok := true
		for tt := tc; tt < end; tt++ {
			if sol.Occ[n][tt] {
				ok = false
				break
			}
			if sol.ChargerUse[tt] >= inst.Chargers {
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

func uniqueSorted(a []int) []int {
	if len(a) <= 1 {
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
	bestI := 0
	bestV := a[0]
	for i := 1; i < len(a); i++ {
		if a[i] > bestV {
			bestV = a[i]
			bestI = i
		}
	}
	return bestI
}