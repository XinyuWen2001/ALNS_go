package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"alns_go/internal/export"
	"alns_go/internal/instance"
	"alns_go/internal/operators"
	"alns_go/internal/solution"
)

type OpStat struct {
	Name     string
	Calls    int
	Feasible int
	Accepted int
	BestHits int

	SumDelta float64
	AvgDelta float64

	FailDestroy int
	FailTTRep   int
	FailFleet   int
	FailCheck   int
}

func main() {
	// ----------------------------
	// Config
	// ----------------------------
	iters := 50000

	// 12 seeds: 你也可以换成你喜欢的任何 12 个 seed
	seeds := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

	// 0=串行（推荐晚上跑更稳）；>0=并行 worker 数（例如 2/3/4）
	parallelWorkers := 3

	// ----------------------------
	// 1) Root log dir setup
	// ----------------------------
	ts := time.Now().Format("20060102_150405")
	rootLogDir := filepath.Join("logs", "multiSeed_"+ts)
	if err := os.MkdirAll(rootLogDir, 0755); err != nil {
		log.Fatal(err)
	}

	// ----------------------------
	// 2) Load instance (once)
	// ----------------------------
	inst, ftFiles, err := instance.LoadJSON("data/instance.json")
	if err != nil {
		log.Fatal(err)
	}
	if len(ftFiles) == 0 {
		ftFiles = []string{"ft_1.csv", "ft_2.csv", "ft_3.csv", "ft_4.csv", "ft_5.csv"}
	}
	if err := inst.LoadFTFromDir("data_cd", ftFiles); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Loaded instance: %s | T=%d | Gamma=%.3f\n", inst.Name, inst.T, inst.Gamma)
	fmt.Printf("Multi-seed run: seeds=%d, iters/seed=%d, parallelWorkers=%d\n\n", len(seeds), iters, parallelWorkers)

	// ----------------------------
	// 3) Run multi-seed
	// ----------------------------
	if parallelWorkers <= 0 {
		// 串行（推荐）
		for i, sd := range seeds {
			seedDir := filepath.Join(rootLogDir, fmt.Sprintf("seed_%02d_%d", i+1, sd))
			_ = os.MkdirAll(seedDir, 0755)
			fmt.Printf("=== Running seed %d (%d/%d) | logDir=%s ===\n", sd, i+1, len(seeds), seedDir)
			solve(inst, sd, iters, seedDir)
			fmt.Println()
		}
		fmt.Printf("All seeds done. Root log dir: %s\n", rootLogDir)
		return
	}

	// 并行（可选）
	if parallelWorkers > len(seeds) {
		parallelWorkers = len(seeds)
	}
	runtime.GOMAXPROCS(parallelWorkers)

	type job struct {
		idx  int
		seed int64
	}
	jobs := make(chan job, len(seeds))
	var wg sync.WaitGroup

	for w := 0; w < parallelWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for jb := range jobs {
				seedDir := filepath.Join(rootLogDir, fmt.Sprintf("seed_%02d_%d", jb.idx+1, jb.seed))
				_ = os.MkdirAll(seedDir, 0755)
				fmt.Printf("[Worker %d] START seed=%d (%d/%d)\n", workerID, jb.seed, jb.idx+1, len(seeds))
				solve(inst, jb.seed, iters, seedDir)
				fmt.Printf("[Worker %d] DONE  seed=%d\n", workerID, jb.seed)
			}
		}(w + 1)
	}

	for i, sd := range seeds {
		jobs <- job{idx: i, seed: sd}
	}
	close(jobs)
	wg.Wait()

	fmt.Printf("\nAll seeds done (parallel). Root log dir: %s\n", rootLogDir)
}

// ----------------------------------------------------------------------
// SOLVE FUNCTION (Encapsulated ALNS Logic)
// ----------------------------------------------------------------------
func solve(inst *instance.Instance, seed int64, iters int, logDir string) {
	startRun := time.Now()
	rng := rand.New(rand.NewSource(seed))

	// SA Params
	T0 := 80.0
	alpha := 0.9999
	minTemp := 0.5

	// ALNS Params
	sigma1, sigma2, sigma3 := 30.0, 20.0, 10.0
	reaction := 0.2
	segment := 100

	// Restart Params
	maxIdleIters := 1000
	lastBestIter := 1
	restartCount := 0

	// =========================================================
	// Operators Options
	// =========================================================
	op0Opt := operators.DestroyLineIntervalOptions{MinLen: inst.HeadwayMax, MaxLen: 60}
	op1Opt := operators.DestroyWorstFTBlockOptions{Q: 3, BlockSize: 4, WithinOneLine: true}
	op2Opt := operators.DestroyBlockRandomOptions{Q: 3, BlockSize: 4, WithinOneLine: false}

	ttOpt := operators.TimetableRepairOptions{ShiftRadius: 6, FillTopK: 10, MaxItersPerLine: 2000}

	// Fleet Repair
	backSteps := 240
	frOptStrong := operators.FleetRepairOptions{ChargeSearchBackSteps: backSteps, UseLinePools: true, PoolBuffer: 1}
	frOptWeak := operators.FleetRepairOptions{ChargeSearchBackSteps: backSteps, UseLinePools: false, PoolBuffer: 1}

	// 1. Initial Solution
	cur := solution.NewEmpty(inst)
	for l := 0; l < len(inst.Lines); l++ {
		for t := 0; t < inst.T; t += inst.HeadwayMax {
			cur.Dep[l] = append(cur.Dep[l], t)
		}
	}
	_ = operators.RepairTimetable(inst, cur, rng, ttOpt)
	ClearFleet(inst, cur)
	if err := operators.FleetRepairGreedy(inst, cur, frOptStrong); err != nil {
		ClearFleet(inst, cur)
		_ = operators.FleetRepairGreedy(inst, cur, frOptWeak)
	}

	eb0, _ := cur.Evaluate(inst)
	best := DeepCopySolution(inst, cur)
	bestEB := eb0

	// ==========================================
	// Logging
	// ==========================================
	logCSVPath := filepath.Join(logDir, "run_log.csv")
	logF, err := os.Create(logCSVPath)
	if err != nil {
		log.Fatalf("❌ 无法创建日志文件 %s: %v", logCSVPath, err)
	}
	defer logF.Close()

	logW := csv.NewWriter(logF)
	defer logW.Flush()

	_ = logW.Write([]string{
		"iter", "temp", "op", "weight", "accepted", "cur_obj", "best_obj",
		"revenue", "charge_cost", "end_topup_cost", "total_charges", "total_deps", "restart",
	})
	logW.Flush()
	// ==========================================

	// Stats
	opNames := []string{"LineInterval", "WorstFT", "BlockRandom"}
	numOps := len(opNames)
	stats := make([]OpStat, numOps)
	for i := range stats {
		stats[i].Name = opNames[i]
	}
	weights := make([]float64, numOps)
	scores := make([]float64, numOps)
	counts := make([]int, numOps)
	for i := 0; i < numOps; i++ {
		weights[i] = 1.0
	}

	// 2. Main Loop
	for it := 1; it <= iters; it++ {
		var temp float64
		isRestart := 0

		if it-lastBestIter > maxIdleIters {
			cur = DeepCopySolution(inst, best)
			eb0 = bestEB
			temp = 40.0
			lastBestIter = it
			restartCount++
			isRestart = 1
		} else {
			temp = T0 * math.Pow(alpha, float64(it))
			if temp < minTemp {
				temp = minTemp
			}
		}

		op := RouletteSelect(weights, rng)
		stats[op].Calls++
		counts[op]++

		cand := DeepCopySolution(inst, cur)

		var derr error
		switch op {
		case 0:
			_, derr = operators.Destroy_LineIntervalRemoval(inst, cand, rng, op0Opt)
		case 1:
			_, derr = operators.Destroy_WorstFT_Block(inst, cand, rng, op1Opt)
		case 2:
			_, derr = operators.Destroy_Block_Random(inst, cand, rng, op2Opt)
		}
		if derr != nil {
			stats[op].FailDestroy++
			continue
		}

		if err := operators.RepairTimetable(inst, cand, rng, ttOpt); err != nil {
			stats[op].FailTTRep++
			continue
		}

		ClearFleet(inst, cand)
		if err := operators.FleetRepairGreedy(inst, cand, frOptStrong); err != nil {
			ClearFleet(inst, cand)
			if err2 := operators.FleetRepairGreedy(inst, cand, frOptWeak); err2 != nil {
				stats[op].FailFleet++
				continue
			}
		}

		if vio := cand.ValidateSolution(inst); len(vio) > 0 {
			stats[op].FailCheck++
			continue
		}

		stats[op].Feasible++
		cEB, _ := cand.Evaluate(inst)
		delta := cEB.Objective - eb0.Objective
		stats[op].SumDelta += delta

		opScore := 0.0
		isAccepted := false

		if SAaccept(delta, temp, rng) {
			isAccepted = true
			stats[op].Accepted++

			if cEB.Objective > bestEB.Objective {
				bestDelta := cEB.Objective - bestEB.Objective
				fmt.Printf("Seed %-6d Iter %-5d BEST | op=%-13s (w=%.2f) | obj=%.2f (Δ=%.2f)\n",
					seed, it, opNames[op], weights[op], cEB.Objective, bestDelta)

				best = DeepCopySolution(inst, cand)
				bestEB = cEB
				stats[op].BestHits++
				opScore = sigma1
				lastBestIter = it
			} else if delta > 0 {
				opScore = sigma2
			} else {
				opScore = sigma3
			}
			cur = cand
			eb0 = cEB
		}

		scores[op] += opScore

		// Adaptive Weights Update
		if it%segment == 0 {
			for i := 0; i < numOps; i++ {
				if counts[i] > 0 {
					avgScore := scores[i] / float64(counts[i])
					weights[i] = (1.0-reaction)*weights[i] + reaction*avgScore
				}
				if weights[i] < 0.1 {
					weights[i] = 0.1
				}
				scores[i] = 0
				counts[i] = 0
			}
		}

		// Log to CSV
		if it%50 == 0 || isAccepted {
			_ = logW.Write([]string{
				strconv.Itoa(it), fmt.Sprintf("%.4f", temp), strconv.Itoa(op),
				fmt.Sprintf("%.2f", weights[op]), bool01(isAccepted),
				fmt.Sprintf("%.2f", eb0.Objective), fmt.Sprintf("%.2f", bestEB.Objective),
				fmt.Sprintf("%.2f", eb0.Revenue), fmt.Sprintf("%.2f", eb0.ChargeCost),
				fmt.Sprintf("%.2f", eb0.EndTopUpCost), strconv.Itoa(eb0.TotalCharges), strconv.Itoa(eb0.TotalDepart),
				strconv.Itoa(isRestart),
			})
			logW.Flush()
		}
	}

	runDuration := time.Since(startRun)
	fmt.Printf("Seed %-6d DONE in %v | Best Objective: %.2f | Restarts: %d | logDir=%s\n",
		seed, runDuration, bestEB.Objective, restartCount, logDir)

	// Export
	outRun := filepath.Join(logDir, "best")
	_ = os.MkdirAll(outRun, 0755)
	_ = export.ExportResults(inst, best, outRun)

	// finalize AvgDelta
	for i := range stats {
		if stats[i].Feasible > 0 {
			stats[i].AvgDelta = stats[i].SumDelta / float64(stats[i].Feasible)
		} else {
			stats[i].AvgDelta = 0
		}
	}
	_ = writeOpStatsCSV(filepath.Join(logDir, "op_stats.csv"), stats)
}

// ----------------------------
// Helpers (Standard)
// ----------------------------

func RouletteSelect(weights []float64, rng *rand.Rand) int {
	sum := 0.0
	for _, w := range weights {
		sum += w
	}
	if sum == 0 {
		return rng.Intn(len(weights))
	}
	r := rng.Float64() * sum
	acc := 0.0
	for i, w := range weights {
		acc += w
		if r <= acc {
			return i
		}
	}
	return len(weights) - 1
}

func SAaccept(delta float64, temp float64, rng *rand.Rand) bool {
	if delta >= 0 {
		return true
	}
	return rng.Float64() < math.Exp(delta/temp)
}

func bool01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func ClearFleet(inst *instance.Instance, s *solution.Solution) {
	N, T := len(inst.Vehicles), inst.T
	if len(s.AssignedLine) != N {
		s.AssignedLine = make([]int, N)
	}
	if len(s.Trips) != N {
		s.Trips = make([][]solution.Trip, N)
	}
	if len(s.Charges) != N {
		s.Charges = make([][]int, N)
	}
	for n := 0; n < N; n++ {
		s.AssignedLine[n] = -1
		s.Trips[n] = s.Trips[n][:0]
		s.Charges[n] = s.Charges[n][:0]
	}
	if len(s.Occ) != N {
		s.Occ = make([][]bool, N)
	}
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
	if len(s.SOC) != N {
		s.SOC = make([][]float64, N)
	}
	for n := 0; n < N; n++ {
		if len(s.SOC[n]) != T+1 {
			s.SOC[n] = make([]float64, T+1)
		} else {
			for t := 0; t < T+1; t++ {
				s.SOC[n][t] = 0
			}
		}
	}
}

func DeepCopySolution(inst *instance.Instance, s *solution.Solution) *solution.Solution {
	L, N, T := len(inst.Lines), len(inst.Vehicles), inst.T
	cp := solution.NewEmpty(inst)
	cp.Dep = make([][]int, L)
	for l := 0; l < L; l++ {
		if len(s.Dep[l]) > 0 {
			cp.Dep[l] = append(cp.Dep[l], s.Dep[l]...)
		}
	}
	cp.AssignedLine = make([]int, N)
	copy(cp.AssignedLine, s.AssignedLine)
	cp.Trips = make([][]solution.Trip, N)
	cp.Charges = make([][]int, N)
	for n := 0; n < N; n++ {
		if len(s.Trips[n]) > 0 {
			cp.Trips[n] = append(cp.Trips[n], s.Trips[n]...)
		}
		if len(s.Charges[n]) > 0 {
			cp.Charges[n] = append(cp.Charges[n], s.Charges[n]...)
		}
	}
	cp.Occ = make([][]bool, N)
	for n := 0; n < N; n++ {
		cp.Occ[n] = make([]bool, T)
		if n < len(s.Occ) && len(s.Occ[n]) == T {
			copy(cp.Occ[n], s.Occ[n])
		}
	}
	cp.ChargerUse = make([]int, T)
	if len(s.ChargerUse) == T {
		copy(cp.ChargerUse, s.ChargerUse)
	}
	cp.SOC = make([][]float64, N)
	for n := 0; n < N; n++ {
		cp.SOC[n] = make([]float64, T+1)
		if n < len(s.SOC) && len(s.SOC[n]) == T+1 {
			copy(cp.SOC[n], s.SOC[n])
		}
	}
	return cp
}

func writeOpStatsCSV(path string, st []OpStat) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	_ = w.Write([]string{"op", "name", "calls", "feasible", "accepted", "bestHits", "avgDelta", "failDestroy", "failTTRepair", "failFleetRepair", "failCheck"})
	for i, s := range st {
		_ = w.Write([]string{
			strconv.Itoa(i),
			s.Name,
			strconv.Itoa(s.Calls),
			strconv.Itoa(s.Feasible),
			strconv.Itoa(s.Accepted),
			strconv.Itoa(s.BestHits),
			fmt.Sprintf("%.6f", s.AvgDelta),
			strconv.Itoa(s.FailDestroy),
			strconv.Itoa(s.FailTTRep),
			strconv.Itoa(s.FailFleet),
			strconv.Itoa(s.FailCheck),
		})
	}
	return nil
}