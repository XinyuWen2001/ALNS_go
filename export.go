package export

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"alns_go/internal/instance"
	"alns_go/internal/solution"
)

// ExportResults exports core results to CSV files under outDir.
//
// Files:
// - departures.csv: (line, t_step, t_min, ft)
// - charges.csv:    (vehicle, charge_start_step, charge_start_min)
// - soc.csv:        (vehicle, t_step, t_min, soc)
// - charger_use.csv:(t_step, t_min, charger_use)
func ExportResults(inst *instance.Instance, sol *solution.Solution, outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}

	// Ensure derived fields exist (SOC/Occ/ChargerUse) for export.
	// If you already guarantee this earlier, this is still safe.
	if err := sol.RebuildAll(inst); err != nil {
		return fmt.Errorf("rebuild before export: %w", err)
	}

	if err := exportDepartures(inst, sol, outDir); err != nil {
		return err
	}
	if err := exportCharges(inst, sol, outDir); err != nil {
		return err
	}
	if err := exportSOC(inst, sol, outDir); err != nil {
		return err
	}
	if err := exportChargerUse(inst, sol, outDir); err != nil {
		return err
	}
	return nil
}

func exportDepartures(inst *instance.Instance, sol *solution.Solution, outDir string) error {
	fp := filepath.Join(outDir, "departures.csv")
	f, err := os.Create(fp)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	_ = w.Write([]string{"line", "t_step", "t_min", "ft"})
	for l, dep := range sol.Dep {
		for _, t := range dep {
			if t < 0 || t >= inst.T {
				continue
			}
			_ = w.Write([]string{
				strconv.Itoa(l),
				strconv.Itoa(t),
				strconv.Itoa(t * inst.StepMinutes),
				fmt.Sprintf("%.6f", inst.FT[l][t]),
			})
		}
	}
	return w.Error()
}

func exportCharges(inst *instance.Instance, sol *solution.Solution, outDir string) error {
	fp := filepath.Join(outDir, "charges.csv")
	f, err := os.Create(fp)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	_ = w.Write([]string{"vehicle", "charge_start_step", "charge_start_min"})
	for n, charges := range sol.Charges {
		for _, t := range charges {
			if t < 0 || t >= inst.T {
				continue
			}
			_ = w.Write([]string{
				strconv.Itoa(n),
				strconv.Itoa(t),
				strconv.Itoa(t * inst.StepMinutes),
			})
		}
	}
	return w.Error()
}

func exportSOC(inst *instance.Instance, sol *solution.Solution, outDir string) error {
	fp := filepath.Join(outDir, "soc.csv")
	f, err := os.Create(fp)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	_ = w.Write([]string{"vehicle", "t_step", "t_min", "soc"})
	for n := range sol.SOC {
		for t, soc := range sol.SOC[n] {
			// SOC has length T+1, so t can be == T
			if t < 0 || t > inst.T {
				continue
			}
			_ = w.Write([]string{
				strconv.Itoa(n),
				strconv.Itoa(t),
				strconv.Itoa(t * inst.StepMinutes),
				fmt.Sprintf("%.6f", soc),
			})
		}
	}
	return w.Error()
}

func exportChargerUse(inst *instance.Instance, sol *solution.Solution, outDir string) error {
	fp := filepath.Join(outDir, "charger_use.csv")
	f, err := os.Create(fp)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	_ = w.Write([]string{"t_step", "t_min", "charger_use"})
	for t, use := range sol.ChargerUse {
		if t < 0 || t >= inst.T {
			continue
		}
		_ = w.Write([]string{
			strconv.Itoa(t),
			strconv.Itoa(t * inst.StepMinutes),
			strconv.Itoa(use),
		})
	}
	return w.Error()
}
