package alns

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"alns_go/internal/model"
)

type DestroyOperator func(inst *model.Instance, s *model.Solution, rng *rand.Rand) bool

type RepairOperator func(inst *model.Instance, s *model.Solution, rng *rand.Rand) bool

type Config struct {
	Seed        int64
	Iters       int
	Temp0       float64
	Cooling     float64
	TempMin     float64
	SegLen      int
	Reaction    float64
	SigmaBest   float64
	SigmaBetter float64
	SigmaAccept float64
}

type OperatorStat struct {
	Name     string
	Calls    int
	Accepted int
	BestHits int
}

type Solver struct {
	cfg     Config
	destroy []DestroyOperator
	repair  RepairOperator
	opNames []string
	weights []float64
	scores  []float64
	counts  []int
	stats   []OperatorStat
}

func New(cfg Config, destroy []DestroyOperator, opNames []string, repair RepairOperator) *Solver {
	m := len(destroy)
	s := &Solver{
		cfg:     cfg,
		destroy: destroy,
		repair:  repair,
		opNames: opNames,
		weights: make([]float64, m),
		scores:  make([]float64, m),
		counts:  make([]int, m),
		stats:   make([]OperatorStat, m),
	}
	for i := 0; i < m; i++ {
		s.weights[i] = 1.0
		s.stats[i] = OperatorStat{Name: opNames[i]}
	}
	return s
}

func (s *Solver) Solve(inst *model.Instance, init *model.Solution) (*model.Solution, model.Eval, []OperatorStat) {
	rng := rand.New(rand.NewSource(s.cfg.Seed))
	cur := model.DeepCopy(init)
	best := model.DeepCopy(init)
	curEval := cur.Evaluate(inst)
	bestEval := curEval

	for it := 1; it <= s.cfg.Iters; it++ {
		temp := math.Max(s.cfg.TempMin, s.cfg.Temp0*math.Pow(s.cfg.Cooling, float64(it)))
		op := roulette(s.weights, rng)
		s.stats[op].Calls++
		s.counts[op]++

		cand := model.DeepCopy(cur)
		if !s.destroy[op](inst, cand, rng) {
			continue
		}
		if !s.repair(inst, cand, rng) {
			continue
		}
		candEval := cand.Evaluate(inst)
		delta := candEval.Objective - curEval.Objective
		score := 0.0

		if acceptSA(delta, temp, rng) {
			cur = cand
			curEval = candEval
			s.stats[op].Accepted++
			score = s.cfg.SigmaAccept
			if delta > 0 {
				score = s.cfg.SigmaBetter
			}
			if candEval.Objective > bestEval.Objective {
				best = model.DeepCopy(cand)
				bestEval = candEval
				s.stats[op].BestHits++
				score = s.cfg.SigmaBest
			}
		}
		s.scores[op] += score

		if it%s.cfg.SegLen == 0 {
			for i := range s.weights {
				if s.counts[i] > 0 {
					avg := s.scores[i] / float64(s.counts[i])
					s.weights[i] = (1-s.cfg.Reaction)*s.weights[i] + s.cfg.Reaction*avg
				}
				s.weights[i] = math.Max(0.1, s.weights[i])
				s.scores[i], s.counts[i] = 0, 0
			}
		}
	}
	return best, bestEval, s.stats
}

func BuildInitial(inst *model.Instance) *model.Solution {
	s := model.NewEmptySolution(inst)
	for l := range inst.Lines {
		for t := 0; t < inst.T; t += inst.HeadwayMax {
			s.Dep[l] = append(s.Dep[l], t)
		}
	}
	return s
}

func RepairGreedy(inst *model.Instance, s *model.Solution, rng *rand.Rand) bool {
	for l := range inst.Lines {
		sort.Ints(s.Dep[l])
		s.Dep[l] = unique(s.Dep[l])
		// max headway fill
		for {
			changed := false
			for i := 0; i < len(s.Dep[l])-1; i++ {
				if s.Dep[l][i+1]-s.Dep[l][i] > inst.HeadwayMax {
					mid := (s.Dep[l][i+1] + s.Dep[l][i]) / 2
					if feasibleGap(s.Dep[l], mid, inst.HeadwayMin) {
						s.Dep[l] = append(s.Dep[l], mid)
						sort.Ints(s.Dep[l])
						changed = true
						break
					}
				}
			}
			if !changed {
				break
			}
		}
		// min headway delete by lower FT
		for i := 0; i < len(s.Dep[l])-1; {
			a, b := s.Dep[l][i], s.Dep[l][i+1]
			if b-a >= inst.HeadwayMin {
				i++
				continue
			}
			if inst.Lines[l].FT[a] < inst.Lines[l].FT[b] {
				s.Dep[l] = append(s.Dep[l][:i], s.Dep[l][i+1:]...)
			} else {
				s.Dep[l] = append(s.Dep[l][:i+1], s.Dep[l][i+2:]...)
			}
		}
	}
	if err := s.RebuildFleet(inst); err != nil {
		return false
	}
	return true
}

func DestroyRandomBlock(inst *model.Instance, s *model.Solution, rng *rand.Rand) bool {
	if len(inst.Lines) == 0 {
		return false
	}
	l := rng.Intn(len(inst.Lines))
	if len(s.Dep[l]) == 0 {
		return false
	}
	start := rng.Intn(len(s.Dep[l]))
	end := start + 3
	if end > len(s.Dep[l]) {
		end = len(s.Dep[l])
	}
	s.Dep[l] = append(s.Dep[l][:start], s.Dep[l][end:]...)
	return true
}

func DestroyWorstFT(inst *model.Instance, s *model.Solution, rng *rand.Rand) bool {
	bestL, bestIdx := -1, -1
	worst := math.MaxFloat64
	for l := range s.Dep {
		for i, t := range s.Dep[l] {
			if inst.Lines[l].FT[t] < worst {
				worst = inst.Lines[l].FT[t]
				bestL, bestIdx = l, i
			}
		}
	}
	if bestL == -1 {
		return false
	}
	s.Dep[bestL] = append(s.Dep[bestL][:bestIdx], s.Dep[bestL][bestIdx+1:]...)
	return true
}

func Report(stats []OperatorStat) string {
	msg := "operator stats:\n"
	for _, st := range stats {
		msg += fmt.Sprintf("- %s calls=%d accepted=%d bestHits=%d\n", st.Name, st.Calls, st.Accepted, st.BestHits)
	}
	return msg
}

func unique(a []int) []int {
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

func feasibleGap(dep []int, t, d1 int) bool {
	for _, x := range dep {
		if abs(x-t) < d1 {
			return false
		}
	}
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
