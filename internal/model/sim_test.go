package model

import "testing"

func TestRebuildFleetBasic(t *testing.T) {
	inst := &Instance{
		T:          20,
		HeadwayMin: 2,
		HeadwayMax: 6,
		ChargeLen:  3,
		Chargers:   1,
		Alpha:      1,
		Beta:       1,
		Gamma:      1,
		Vehicles: []Vehicle{
			{ID: 0, Emin: 0, Emax: 30},
			{ID: 1, Emin: 0, Emax: 30},
		},
		Lines: []Line{{ID: 0, TripStep: 4, Energy: 8, FT: make([]float64, 20), OpCost: make([]float64, 20)}},
	}
	s := NewEmptySolution(inst)
	s.Dep[0] = []int{0, 5, 10, 15}
	if err := s.RebuildFleet(inst); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if err := s.ValidateFleet(inst); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

func TestRebuildFleetWithLinePools(t *testing.T) {
	inst := &Instance{
		T:          30,
		HeadwayMin: 2,
		HeadwayMax: 5,
		ChargeLen:  2,
		Chargers:   2,
		Vehicles: []Vehicle{
			{ID: 0, Emin: 0, Emax: 60}, {ID: 1, Emin: 0, Emax: 55}, {ID: 2, Emin: 0, Emax: 50}, {ID: 3, Emin: 0, Emax: 45},
		},
		Lines: []Line{
			{ID: 0, TripStep: 6, Energy: 10, FT: make([]float64, 30), OpCost: make([]float64, 30)},
			{ID: 1, TripStep: 5, Energy: 8, FT: make([]float64, 30), OpCost: make([]float64, 30)},
		},
	}
	s := NewEmptySolution(inst)
	s.Dep[0] = []int{0, 7, 14, 21}
	s.Dep[1] = []int{1, 8, 15, 22}
	opt := FleetRepairOptions{ChargeSearchBackSteps: 20, UseLinePools: true, PoolBuffer: 1}
	if err := s.RebuildFleetWithOptions(inst, opt); err != nil {
		t.Fatalf("rebuild with pools failed: %v", err)
	}
	if err := s.ValidateFleet(inst); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}
