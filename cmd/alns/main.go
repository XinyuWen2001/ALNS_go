package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"alns_go/internal/alns"
	"alns_go/internal/data"
	"alns_go/internal/model"
)

func main() {
	instancePath := flag.String("instance", "data/instance.json", "path to instance.json")
	ftDir := flag.String("ft_dir", "data_cd", "directory containing ft csv files")
	ftFilesStr := flag.String("ft_files", "", "comma separated ft files (optional)")
	seed := flag.Int64("seed", 42, "single run seed")
	multiSeeds := flag.String("seeds", "", "comma separated seeds for multi-run, e.g. 1,2,3")
	iters := flag.Int("iters", 50000, "alns iterations")
	outDir := flag.String("out", "logs", "output directory")
	flag.Parse()

	var ftFiles []string
	if strings.TrimSpace(*ftFilesStr) != "" {
		for _, x := range strings.Split(*ftFilesStr, ",") {
			x = strings.TrimSpace(x)
			if x != "" {
				ftFiles = append(ftFiles, x)
			}
		}
	}

	inst, err := data.LoadInstance(*instancePath, *ftDir, ftFiles)
	if err != nil {
		panic(err)
	}
	cfg := defaultConfig(*iters)

	ts := time.Now().Format("20060102_150405")
	runDir := filepath.Join(*outDir, "alns_"+ts)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		panic(err)
	}

	if strings.TrimSpace(*multiSeeds) == "" {
		_, ev, stats := runOne(inst, cfg, *seed)
		fmt.Printf("Instance=%s T=%d Lines=%d Veh=%d\n", inst.Name, inst.T, len(inst.Lines), len(inst.Vehicles))
		fmt.Printf("Seed=%d Best objective=%.2f depart=%d\n", *seed, ev.Objective, ev.TotalDepart)
		fmt.Print(alns.Report(stats))
		if err := writeSummary(filepath.Join(runDir, "single_run_summary.csv"), []result{{Seed: *seed, Objective: ev.Objective, Depart: ev.TotalDepart}}); err != nil {
			panic(err)
		}
		return
	}

	seeds, err := parseSeeds(*multiSeeds)
	if err != nil {
		panic(err)
	}
	results := make([]result, 0, len(seeds))
	for _, sd := range seeds {
		_, ev, _ := runOne(inst, cfg, sd)
		results = append(results, result{Seed: sd, Objective: ev.Objective, Depart: ev.TotalDepart})
		fmt.Printf("seed=%d obj=%.2f dep=%d\n", sd, ev.Objective, ev.TotalDepart)
	}

	if err := writeSummary(filepath.Join(runDir, "multi_seed_summary.csv"), results); err != nil {
		panic(err)
	}
	mean, std, best, worst := calcStats(results)
	fmt.Printf("Multi-seed summary: n=%d mean=%.2f std=%.2f best=%.2f worst=%.2f gap=%.2f%%\n",
		len(results), mean, std, best, worst, 100*(best-worst)/math.Max(math.Abs(best), 1e-9))
}

type result struct {
	Seed      int64
	Objective float64
	Depart    int
}

func defaultConfig(iters int) alns.Config {
	return alns.Config{
		Iters:       iters,
		Temp0:       80,
		Cooling:     0.9999,
		TempMin:     0.5,
		SegLen:      100,
		Reaction:    0.2,
		SigmaBest:   30,
		SigmaBetter: 20,
		SigmaAccept: 10,
	}
}

func runOne(inst *model.Instance, cfg alns.Config, seed int64) (*model.Solution, model.Eval, []alns.OperatorStat) {
	cfg.Seed = seed
	init := alns.BuildInitial(inst)
	solver := alns.New(cfg,
		[]alns.DestroyOperator{alns.DestroyRandomBlock, alns.DestroyWorstFT},
		[]string{"RandomBlock", "WorstFT"},
		alns.RepairGreedy,
	)
	return solver.Solve(inst, init)
}

func parseSeeds(s string) ([]int64, error) {
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid seeds")
	}
	return out, nil
}

func writeSummary(path string, rows []result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"seed", "objective", "depart"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{strconv.FormatInt(r.Seed, 10), fmt.Sprintf("%.6f", r.Objective), strconv.Itoa(r.Depart)}); err != nil {
			return err
		}
	}
	return nil
}

func calcStats(rows []result) (mean, std, best, worst float64) {
	if len(rows) == 0 {
		return
	}
	best, worst = rows[0].Objective, rows[0].Objective
	for _, r := range rows {
		mean += r.Objective
		if r.Objective > best {
			best = r.Objective
		}
		if r.Objective < worst {
			worst = r.Objective
		}
	}
	mean /= float64(len(rows))
	for _, r := range rows {
		d := r.Objective - mean
		std += d * d
	}
	std = math.Sqrt(std / float64(len(rows)))
	return
}
