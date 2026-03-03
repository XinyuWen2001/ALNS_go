package solution

import (
	"fmt"
	"sort"

	"alns_go/internal/instance"
)

// RebuildAll rebuilds derived fields from Dep/Trips/Charges.
// Recommended usage: call this before checks or exporting.
func (s *Solution) RebuildAll(inst *instance.Instance) error {
	if err := s.RebuildOccAndChargerUse(inst); err != nil {
		return err
	}
	if err := s.RebuildSOC(inst); err != nil {
		return err
	}
	return nil
}

// RebuildOccAndChargerUse rebuilds Occ[n][t] and ChargerUse[t] from Trips and Charges.
func (s *Solution) RebuildOccAndChargerUse(inst *instance.Instance) error {
	N := len(inst.Vehicles)
	T := inst.T

	// clear
	for n := 0; n < N; n++ {
		if len(s.Occ[n]) != T {
			s.Occ[n] = make([]bool, T)
		} else {
			for t := 0; t < T; t++ {
				s.Occ[n][t] = false
			}
		}
	}
	if len(s.ChargerUse) != T {
		s.ChargerUse = make([]int, T)
	} else {
		for t := 0; t < T; t++ {
			s.ChargerUse[t] = 0
		}
	}

	// ensure Trips sorted by time for each vehicle (safe for later checks)
	for n := 0; n < N; n++ {
		sort.Slice(s.Trips[n], func(i, j int) bool { return s.Trips[n][i].T < s.Trips[n][j].T })
		sort.Ints(s.Charges[n])
	}

	// mark trip occupancy
	for n := 0; n < N; n++ {
		for _, tr := range s.Trips[n] {
			if tr.L < 0 || tr.L >= len(inst.Lines) {
				return fmt.Errorf("rebuild occ: vehicle %d has invalid line index %d", n, tr.L)
			}
			if tr.T < 0 || tr.T >= T {
				return fmt.Errorf("rebuild occ: vehicle %d has trip at invalid time %d", n, tr.T)
			}
			lenTrip := inst.Lines[tr.L].TripSteps
			end := tr.T + lenTrip // [T, end)
			if end > T {
				end = T
			}
			for tt := tr.T; tt < end; tt++ {
				if s.Occ[n][tt] {
					// overlap already occupied (trip/charge conflict)
					return fmt.Errorf("rebuild occ: overlap at vehicle %d time %d (trip)", n, tt)
				}
				s.Occ[n][tt] = true
			}
		}
	}

	// mark charge occupancy and charger usage
	for n := 0; n < N; n++ {
		for _, tc := range s.Charges[n] {
			if tc < 0 || tc >= T {
				return fmt.Errorf("rebuild occ: vehicle %d has charge at invalid time %d", n, tc)
			}
			end := tc + inst.ChargeLen // [tc, end)
			if end > T {
				end = T
			}
			for tt := tc; tt < end; tt++ {
				if s.Occ[n][tt] {
					return fmt.Errorf("rebuild occ: overlap at vehicle %d time %d (charge)", n, tt)
				}
				s.Occ[n][tt] = true
				s.ChargerUse[tt]++
			}
		}
	}

	return nil
}

// RebuildSOC rebuilds SOC[n][t] for t=0..T based on charge-start and trip-start events.
// Semantics (consistent with e^{t+1} = e^t - eta * x^t):
// - SOC[n][t] is the battery energy at the BEGINNING of step t (pre-state).
// - If charge starts at t: SOC[n][t] is set to Emax immediately.
// - If trip starts at t on line l: the energy deduction applies to SOC[n][t+1] = SOC[n][t] - Eta[l].
func (s *Solution) RebuildSOC(inst *instance.Instance) error {
	N := len(inst.Vehicles)
	T := inst.T

	for n := 0; n < N; n++ {
		if len(s.SOC[n]) != T+1 {
			s.SOC[n] = make([]float64, T+1)
		}
		emax := inst.Vehicles[n].Emax
		s.SOC[n][0] = emax

		// fast lookup: charge start set
		isChargeStart := make([]bool, T)
		for _, tc := range s.Charges[n] {
			if tc >= 0 && tc < T {
				isChargeStart[tc] = true
			}
		}
		// fast lookup: trip start at time t -> line index (assume at most one trip start per t for a vehicle)
		tripLineAt := make([]int, T)
		for t := 0; t < T; t++ {
			tripLineAt[t] = -1
		}
		for _, tr := range s.Trips[n] {
			if tr.T >= 0 && tr.T < T {
				if tripLineAt[tr.T] != -1 {
					return fmt.Errorf("rebuild soc: vehicle %d has 2 trips starting at same time %d", n, tr.T)
				}
				tripLineAt[tr.T] = tr.L
			}
		}

		for t := 0; t < T; t++ {
			cur := s.SOC[n][t]

			// charge at t sets SOC to full at step start
			if isChargeStart[t] {
				cur = emax
				s.SOC[n][t] = cur
			}

			// default carry
			next := cur

			// trip consumes energy into next step
			if l := tripLineAt[t]; l != -1 {
				if l < 0 || l >= len(inst.Lines) {
					return fmt.Errorf("rebuild soc: vehicle %d invalid line %d at time %d", n, l, t)
				}
				next = cur - inst.Lines[l].EnergyKWh
			}

			s.SOC[n][t+1] = next
		}
	}

	return nil
}
