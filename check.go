package solution

import (
	"fmt"
	"sort"

	"alns_go/internal/instance"
)

type Violation struct {
	Code string
	Msg  string
	N    int
	L    int
	T    int
	T2   int
}

func (v Violation) String() string {
	// Keep it compact but actionable
	return fmt.Sprintf("[%s] %s (n=%d l=%d t=%d t2=%d)", v.Code, v.Msg, v.N, v.L, v.T, v.T2)
}

func (s *Solution) ValidateSolution(inst *instance.Instance) []Violation {
	// Always rebuild derived state before checks (recommended)
	if err := s.RebuildAll(inst); err != nil {
		return []Violation{{Code: "REBUILD", Msg: err.Error(), N: -1, L: -1, T: -1, T2: -1}}
	}

	var vio []Violation
	vio = append(vio, s.CheckTimetable(inst)...)
	vio = append(vio, s.CheckFleet(inst)...)
	return vio
}

// ----------------------------
// Timetable checks
// ----------------------------

// CheckTimetable checks constraints related to Dep[l]:
// (4-4) unique departure per line per time
// (4-5) min headway
// (4-6) max headway (with boundary anchors)
func (s *Solution) CheckTimetable(inst *instance.Instance) []Violation {
	var vio []Violation

	L := len(inst.Lines)
	T := inst.T
	d1 := inst.HeadwayMin
	d2 := inst.HeadwayMax

	for l := 0; l < L; l++ {
		dep := s.Dep[l]
		if len(dep) == 0 {
			// violates max headway in almost all practical cases
			vio = append(vio, Violation{Code: "4-6", Msg: "Dep[l] is empty -> max headway violated", L: l, N: -1, T: 0, T2: T})
			continue
		}

		// enforce sorted for checking
		if !sort.IntsAreSorted(dep) {
			tmp := append([]int(nil), dep...)
			sort.Ints(tmp)
			dep = tmp
		}

		// (4-4) uniqueness check
		for i := 1; i < len(dep); i++ {
			if dep[i] == dep[i-1] {
				vio = append(vio, Violation{Code: "4-4", Msg: "duplicate departure time on same line", L: l, T: dep[i], T2: dep[i-1], N: -1})
			}
		}

		// (4-5) min headway between consecutive departures
		for i := 0; i+1 < len(dep); i++ {
			if dep[i+1]-dep[i] < d1 {
				vio = append(vio, Violation{Code: "4-5", Msg: "min headway violated", L: l, T: dep[i], T2: dep[i+1], N: -1})
			}
		}

		// (4-6) max headway with boundaries [0, T]
		// Check first gap: dep[0] - 0 <= d2
		if dep[0]-0 > d2 {
			vio = append(vio, Violation{Code: "4-6", Msg: "max headway violated at start boundary", L: l, T: 0, T2: dep[0], N: -1})
		}
		// middle gaps
		for i := 0; i+1 < len(dep); i++ {
			if dep[i+1]-dep[i] > d2 {
				vio = append(vio, Violation{Code: "4-6", Msg: "max headway violated between departures", L: l, T: dep[i], T2: dep[i+1], N: -1})
			}
		}
		// last gap: T - dep[last] <= d2
		if T-dep[len(dep)-1] > d2 {
			vio = append(vio, Violation{Code: "4-6", Msg: "max headway violated at end boundary", L: l, T: dep[len(dep)-1], T2: T, N: -1})
		}
	}

	return vio
}

// ----------------------------
// Fleet checks
// ----------------------------

func (s *Solution) CheckFleet(inst *instance.Instance) []Violation {
	var vio []Violation

	N := len(inst.Vehicles)
	T := inst.T

	// (4-2)(4-3) single line per vehicle + assignment consistency
	for n := 0; n < N; n++ {
		if len(s.Trips[n]) == 0 {
			if s.AssignedLine[n] != -1 {
				vio = append(vio, Violation{Code: "4-2", Msg: "vehicle has AssignedLine but no trips", N: n, L: s.AssignedLine[n]})
			}
			continue
		}
		firstL := s.Trips[n][0].L
		if s.AssignedLine[n] != firstL {
			vio = append(vio, Violation{Code: "4-3", Msg: "AssignedLine mismatch with Trips", N: n, L: firstL})
		}
		for _, tr := range s.Trips[n] {
			if tr.L != firstL {
				vio = append(vio, Violation{Code: "4-2", Msg: "vehicle serves multiple lines in one day", N: n, L: tr.L, T: tr.T})
			}
		}
	}

	// (4-7)(4-8)(4-9) no overlaps are already caught during rebuild (Occ overlap errors),
	// but we also do a direct check using Trips/Charges intervals for robustness.
	for n := 0; n < N; n++ {
		// check trip-trip overlaps using sorted trips
		trips := s.Trips[n]
		for i := 0; i+1 < len(trips); i++ {
			l1 := trips[i].L
			t1 := trips[i].T
			end1 := t1 + inst.Lines[l1].TripSteps

			l2 := trips[i+1].L
			t2 := trips[i+1].T
			_ = l2

			if t2 < end1 {
				vio = append(vio, Violation{Code: "4-7", Msg: "trip-trip overlap", N: n, L: l1, T: t1, T2: t2})
			}
		}

		// check trip-charge overlaps
		for _, tr := range trips {
			t1 := tr.T
			end1 := t1 + inst.Lines[tr.L].TripSteps
			for _, tc := range s.Charges[n] {
				endc := tc + inst.ChargeLen
				// overlap if intervals [t1,end1) and [tc,endc) intersect
				if tc < end1 && t1 < endc {
					vio = append(vio, Violation{Code: "4-8", Msg: "trip-charge overlap", N: n, L: tr.L, T: tr.T, T2: tc})
				}
			}
		}
	}

	// (4-10) charger capacity
	for t := 0; t < T; t++ {
		if s.ChargerUse[t] > inst.Chargers {
			vio = append(vio, Violation{Code: "4-10", Msg: "charger capacity exceeded", N: -1, L: -1, T: t, T2: s.ChargerUse[t]})
		}
	}

	// (4-17) bounds + (4-19) before trip energy check
	for n := 0; n < N; n++ {
		emin := inst.Vehicles[n].Emin
		emax := inst.Vehicles[n].Emax

		// bounds for t=0..T
		for t := 0; t <= T; t++ {
			e := s.SOC[n][t]
			if e < emin-1e-9 || e > emax+1e-9 {
				vio = append(vio, Violation{Code: "4-17", Msg: "SOC out of bounds", N: n, T: t, T2: int(e)})
				break
			}
		}

		// before-trip check: at each trip start time t, SOC[t] >= Eta + Emin
		for _, tr := range s.Trips[n] {
			if tr.T < 0 || tr.T >= T {
				continue
			}
			need := inst.Lines[tr.L].EnergyKWh + emin
			if s.SOC[n][tr.T] + 1e-9 < need {
				vio = append(vio, Violation{Code: "4-19", Msg: "insufficient SOC before trip", N: n, L: tr.L, T: tr.T})
			}
		}
	}

	return vio
}
