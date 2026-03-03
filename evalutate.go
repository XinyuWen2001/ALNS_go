package solution

import (
	"fmt"

	"alns_go/internal/instance"
)

type EvalBreakdown struct {
	Revenue       float64
	ChargeCost    float64
	EndTopUpCost  float64
	TotalCharges  int
	TotalDepart   int
	Objective     float64
}

func (e EvalBreakdown) String() string {
	return fmt.Sprintf(
		"Eval: obj=%.2f | revenue=%.2f, chargeCost=%.2f (#charges=%d), endTopUp=%.2f | departures=%d",
		e.Objective, e.Revenue, e.ChargeCost, e.TotalCharges, e.EndTopUpCost, e.TotalDepart,
	)
}

// Evaluate computes objective components for the current solution.
// Assumptions for this first version (baseline):
// - Revenue: sum FT[l][t] over departures in Dep[l].
// - ChargeCost: FullChargeCost * (#charge starts).
// - EndTopUpCost: 0 by default (you can enable when gamma is added).
//
// IMPORTANT: call sol.RebuildAll(inst) before Evaluate if you rely on SOC/derived fields.
func (s *Solution) Evaluate(inst *instance.Instance) (EvalBreakdown, error) {
	L := len(inst.Lines)
	T := inst.T

	var rev float64
	var depCnt int

	// Revenue from timetable (Dep)
	for l := 0; l < L; l++ {
		for _, t := range s.Dep[l] {
			if t < 0 || t >= T {
				return EvalBreakdown{}, fmt.Errorf("evaluate: dep out of range: line=%d t=%d", l, t)
			}
			rev += inst.FT[l][t]
			depCnt++
		}
	}

	// Charge cost (count charge starts)
	totalCharges := 0
	for n := 0; n < len(inst.Vehicles); n++ {
		totalCharges += len(s.Charges[n])
	}
	chargeCost := inst.FullChargeCost * float64(totalCharges)

	// End-of-day top-up cost (disabled in baseline)
	endTopUp := 0.0

	obj := rev - chargeCost - endTopUp

	return EvalBreakdown{
		Revenue:      rev,
		ChargeCost:   chargeCost,
		EndTopUpCost: endTopUp,
		TotalCharges: totalCharges,
		TotalDepart:  depCnt,
		Objective:    obj,
	}, nil
}
