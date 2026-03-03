package operators

import (
	"fmt"
	"math/rand"
	"sort"

	"alns_go/internal/instance"
	"alns_go/internal/solution"
)

type TimetableRepairOptions struct {
	// Search radius for shifting when fixing min-headway conflicts.
	ShiftRadius int

	// Fill strategy: choose among top-K candidates (by FT) randomly to avoid being too greedy.
	FillTopK int

	// Hard cap to prevent infinite loops in pathological cases.
	MaxItersPerLine int
}

// RepairTimetable enforces timetable-only feasibility:
// - single-departure: Dep[l] is unique
// - headway min: adjacent gap >= δ1
// - headway max: adjacent gap <= δ2
// - boundary max-headway windows: at least one dep in [0,δ2-1] and [T-δ2,T-1]
//
// It does NOT consider fleet feasibility (that is FleetRepairGreedy's job).
func RepairTimetable(inst *instance.Instance, sol *solution.Solution, rng *rand.Rand, opt TimetableRepairOptions) error {
	if opt.ShiftRadius <= 0 {
		opt.ShiftRadius = 6
	}
	if opt.FillTopK <= 0 {
		opt.FillTopK = 10
	}
	if opt.MaxItersPerLine <= 0 {
		opt.MaxItersPerLine = 2000
	}

	L := len(inst.Lines)
	T := inst.T

	for l := 0; l < L; l++ {
		sol.Dep[l] = normalizeDep(sol.Dep[l], T)

		if err := fixBoundaryMaxHeadway(inst, sol, rng, l, opt); err != nil {
			return err
		}
		if err := fixMinHeadway(inst, sol, rng, l, opt); err != nil {
			return err
		}
		if err := fillMaxHeadway(inst, sol, rng, l, opt); err != nil {
			return err
		}

		sol.Dep[l] = normalizeDep(sol.Dep[l], T)
	}

	return nil
}

// -----------------------------
// Boundary repair
// -----------------------------

func fixBoundaryMaxHeadway(inst *instance.Instance, sol *solution.Solution, rng *rand.Rand, l int, opt TimetableRepairOptions) error {
	T := inst.T
	d1 := inst.HeadwayMin
	d2 := inst.HeadwayMax

	dep := sol.Dep[l]
	if len(dep) == 0 {
		// If empty, insert a seed departure in [0, d2-1] at best FT
		hi := d2 - 1
		if hi >= T {
			hi = T - 1
		}
		t, ok := bestFTInRange(inst, l, 0, hi, nil)
		if !ok {
			return fmt.Errorf("boundary: cannot seed departure for line %d", l)
		}
		sol.Dep[l] = []int{t}
		return nil
	}

	// Ensure at least one departure in [0, d2-1]
	first := dep[0]
	if first >= d2 {
		hi := d2 - 1
		if hi >= T {
			hi = T - 1
		}
		// Candidate range [0, min(hi, first-d1)] to preserve min-headway with first
		cHi := hi
		if first-d1 < cHi {
			cHi = first - d1
		}
		if cHi >= 0 {
			t, ok := bestFTInRange(inst, l, 0, cHi, toSet(dep))
			if ok {
				dep = append(dep, t)
				dep = normalizeDep(dep, T)
				sol.Dep[l] = dep
			} else {
				// If we cannot insert without violating min-headway, do nothing here;
				// fixMinHeadway may later shift/delete to resolve.
			}
		}
	}

	// Ensure at least one departure in [T-d2, T-1]
	dep = sol.Dep[l]
	last := dep[len(dep)-1]
	if last <= T-d2-1 {
		lo := T - d2
		if lo < 0 {
			lo = 0
		}
		// Candidate range [max(lo, last+d1), T-1]
		cLo := lo
		if last+d1 > cLo {
			cLo = last + d1
		}
		if cLo <= T-1 {
			t, ok := bestFTInRange(inst, l, cLo, T-1, toSet(dep))
			if ok {
				dep = append(dep, t)
				dep = normalizeDep(dep, T)
				sol.Dep[l] = dep
			} else {
				// Might be impossible with strict min-headway; leave to fixMinHeadway.
			}
		}
	}

	return nil
}

// -----------------------------
// Min-headway repair
// -----------------------------

func fixMinHeadway(inst *instance.Instance, sol *solution.Solution, rng *rand.Rand, l int, opt TimetableRepairOptions) error {
	T := inst.T
	d1 := inst.HeadwayMin
	dep := sol.Dep[l]
	if len(dep) <= 1 {
		return nil
	}

	// We repeatedly scan and resolve conflicts.
	iters := 0
	for {
		iters++
		if iters > opt.MaxItersPerLine {
			return fmt.Errorf("fixMinHeadway: exceeded max iterations on line %d", l)
		}

		changed := false
		dep = sol.Dep[l]
		if len(dep) <= 1 {
			return nil
		}

		for i := 0; i < len(dep)-1; i++ {
			t1 := dep[i]
			t2 := dep[i+1]
			if t2-t1 >= d1 {
				continue
			}
			// conflict between t1 and t2
			changed = true

			// decide which one is "worse" (lower FT)
			ft1 := inst.FT[l][t1]
			ft2 := inst.FT[l][t2]
			worseIdx := i
			betterIdx := i + 1
			if ft2 < ft1 {
				worseIdx = i + 1
				betterIdx = i
			}

			worseT := dep[worseIdx]
			betterT := dep[betterIdx]

			// try shift worseT to a feasible nearby time
			set := toSet(dep)
			delete(set, worseT) // allow moving

			newT, ok := bestFeasibleShift(inst, l, worseT, set, d1, opt.ShiftRadius)
			if ok {
				dep[worseIdx] = newT
				dep = normalizeDep(dep, T)
				sol.Dep[l] = dep
				break // restart scan
			}

			// otherwise delete worseT
			dep = deleteTime(dep, worseT)
			dep = normalizeDep(dep, T)
			sol.Dep[l] = dep

			// Keep at least one departure if possible
			if len(dep) == 0 {
				hi := inst.HeadwayMax - 1
				if hi >= T {
					hi = T - 1
				}
				t, ok2 := bestFTInRange(inst, l, 0, hi, nil)
				if !ok2 {
					// fallback: just put 0
					t = 0
				}
				dep = []int{t}
				dep = normalizeDep(dep, T)
				sol.Dep[l] = dep
			}

			_ = betterT // kept
			break // restart scan
		}

		if !changed {
			return nil
		}
	}
}

func bestFeasibleShift(inst *instance.Instance, l int, center int, existing map[int]bool, d1 int, radius int) (int, bool) {
	T := inst.T
	bestT := -1
	bestFT := -1e100

	lo := center - radius
	hi := center + radius
	if lo < 0 {
		lo = 0
	}
	if hi > T-1 {
		hi = T - 1
	}
	for t := lo; t <= hi; t++ {
		if existing[t] {
			continue
		}
		if !minGapOK(existing, t, d1) {
			continue
		}
		ft := inst.FT[l][t]
		if ft > bestFT {
			bestFT = ft
			bestT = t
		}
	}
	return bestT, bestT != -1
}

// minGapOK checks if inserting time t keeps distance >= d1 to all existing times.
// Since existing is a set (not ordered), this is O(#dep). But #dep is small in your scale.
func minGapOK(existing map[int]bool, t int, d1 int) bool {
	for tt := range existing {
		if abs(tt-t) < d1 {
			return false
		}
	}
	return true
}

// -----------------------------
// Max-headway repair (fill holes)
// -----------------------------

func fillMaxHeadway(inst *instance.Instance, sol *solution.Solution, rng *rand.Rand, l int, opt TimetableRepairOptions) error {
	T := inst.T
	d1 := inst.HeadwayMin
	d2 := inst.HeadwayMax

	dep := sol.Dep[l]
	if len(dep) == 0 {
		return fmt.Errorf("fillMaxHeadway: empty dep after prior steps, line=%d", l)
	}

	iters := 0
	for {
		iters++
		if iters > opt.MaxItersPerLine {
			return fmt.Errorf("fillMaxHeadway: exceeded max iterations on line %d", l)
		}

		dep = sol.Dep[l]
		sort.Ints(dep)
		if len(dep) == 1 {
			// Need to ensure boundary windows; handled elsewhere. Still attempt to fill.
			// If a single dep, we will fill forward until last reaches tail.
		}

		changed := false

		// Ensure gaps between consecutive departures <= d2
		for i := 0; i < len(dep)-1; i++ {
			left := dep[i]
			right := dep[i+1]
			if right-left <= d2 {
				continue
			}
			changed = true

			// candidate range must keep min-headway to both sides:
			// [left + d1, right - d1]
			cLo := left + d1
			cHi := right - d1
			if cLo > cHi {
				// cannot insert; this indicates earlier min-headway fixes may have caused issues
				// fallback: do nothing here
				break
			}

			existing := toSet(dep)
			tIns, ok := pickInsertTimeByTopK(inst, rng, l, cLo, cHi, existing, d1, opt.FillTopK)
			if !ok {
				// If cannot insert (due to min constraints), break
				break
			}

			dep = append(dep, tIns)
			dep = normalizeDep(dep, T)
			sol.Dep[l] = dep
			break // restart scanning
		}

		// Also ensure start/tail max-headway windows (in case they were not satisfiable before min-fix)
		if !changed {
			// start window: need dep in [0, d2-1]
			dep = sol.Dep[l]
			if len(dep) > 0 && dep[0] >= d2 {
				hi := d2 - 1
				if hi >= T {
					hi = T - 1
				}
				cHi := hi
				if dep[0]-d1 < cHi {
					cHi = dep[0] - d1
				}
				if cHi >= 0 {
					existing := toSet(dep)
					tIns, ok := pickInsertTimeByTopK(inst, rng, l, 0, cHi, existing, d1, opt.FillTopK)
					if ok {
						dep = append(dep, tIns)
						dep = normalizeDep(dep, T)
						sol.Dep[l] = dep
						changed = true
					}
				}
			}
		}

		if !changed {
			dep = sol.Dep[l]
			if len(dep) > 0 && dep[len(dep)-1] <= T-d2-1 {
				lo := T - d2
				if lo < 0 {
					lo = 0
				}
				cLo := lo
				if dep[len(dep)-1]+d1 > cLo {
					cLo = dep[len(dep)-1] + d1
				}
				if cLo <= T-1 {
					existing := toSet(dep)
					tIns, ok := pickInsertTimeByTopK(inst, rng, l, cLo, T-1, existing, d1, opt.FillTopK)
					if ok {
						dep = append(dep, tIns)
						dep = normalizeDep(dep, T)
						sol.Dep[l] = dep
						changed = true
					}
				}
			}
		}

		if !changed {
			return nil
		}
	}
}

func pickInsertTimeByTopK(inst *instance.Instance, rng *rand.Rand, l int, lo, hi int, existing map[int]bool, d1 int, topK int) (int, bool) {
	if lo > hi {
		return -1, false
	}
	if lo < 0 {
		lo = 0
	}
	if hi > inst.T-1 {
		hi = inst.T - 1
	}

	type cand struct {
		t  int
		ft float64
	}
	cs := make([]cand, 0, 64)

	for t := lo; t <= hi; t++ {
		if existing[t] {
			continue
		}
		if !minGapOK(existing, t, d1) {
			continue
		}
		cs = append(cs, cand{t: t, ft: inst.FT[l][t]})
	}
	if len(cs) == 0 {
		return -1, false
	}

	sort.Slice(cs, func(i, j int) bool {
		if cs[i].ft == cs[j].ft {
			return cs[i].t < cs[j].t
		}
		return cs[i].ft > cs[j].ft
	})

	k := topK
	if k <= 0 {
		k = 1
	}
	if k > len(cs) {
		k = len(cs)
	}

	choice := cs[rng.Intn(k)]
	return choice.t, true
}

func bestFTInRange(inst *instance.Instance, l int, lo, hi int, existing map[int]bool) (int, bool) {
	if lo > hi {
		return -1, false
	}
	if lo < 0 {
		lo = 0
	}
	if hi > inst.T-1 {
		hi = inst.T - 1
	}
	bestT := -1
	bestFT := -1e100
	for t := lo; t <= hi; t++ {
		if existing != nil && existing[t] {
			continue
		}
		ft := inst.FT[l][t]
		if ft > bestFT {
			bestFT = ft
			bestT = t
		}
	}
	return bestT, bestT != -1
}

// -----------------------------
// Small utilities
// -----------------------------

func toSet(dep []int) map[int]bool {
	m := make(map[int]bool, len(dep))
	for _, t := range dep {
		m[t] = true
	}
	return m
}

func deleteTime(dep []int, t int) []int {
	out := make([]int, 0, len(dep))
	for _, x := range dep {
		if x == t {
			continue
		}
		out = append(out, x)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
