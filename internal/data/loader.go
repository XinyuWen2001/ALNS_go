package data

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"alns_go/internal/model"
)

type rawInstance struct {
	Meta struct {
		Name string `json:"name"`
	} `json:"meta"`
	Time struct {
		OperatingSteps int `json:"operating_steps"`
	} `json:"time"`
	Economics struct {
		TicketPrice    float64 `json:"ticket_price"`
		FullChargeCost float64 `json:"full_charge_cost"`
		GammaUnitCost  float64 `json:"gamma_unit_cost"`
	} `json:"economics"`
	Headway struct {
		MinStep int `json:"min_step"`
		MaxStep int `json:"max_step"`
	} `json:"headway"`
	Charging struct {
		Chargers          int `json:"chargers"`
		FullChargeLenStep int `json:"full_charge_len_step"`
	} `json:"charging"`
	Lines []struct {
		TripSteps int     `json:"trip_steps"`
		EnergyKWh float64 `json:"energy_kwh"`
	} `json:"lines"`
	Fleet struct {
		SocMinKWh float64   `json:"soc_min_kwh"`
		SocMaxKWh []float64 `json:"soc_max_kwh"`
	} `json:"fleet"`
}

func LoadInstance(jsonPath string, ftDir string, ftFiles []string) (*model.Instance, error) {
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	var raw rawInstance
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	if len(raw.Lines) == 0 {
		return nil, fmt.Errorf("instance has no lines")
	}
	if len(raw.Fleet.SocMaxKWh) == 0 {
		return nil, fmt.Errorf("instance has no vehicles")
	}

	if len(ftFiles) == 0 {
		ftFiles = make([]string, len(raw.Lines))
		for i := range raw.Lines {
			ftFiles[i] = fmt.Sprintf("ft_%d.csv", i+1)
		}
	}
	if len(ftFiles) != len(raw.Lines) {
		return nil, fmt.Errorf("ft files count %d != lines %d", len(ftFiles), len(raw.Lines))
	}

	inst := &model.Instance{
		Name:       raw.Meta.Name,
		T:          raw.Time.OperatingSteps,
		HeadwayMin: raw.Headway.MinStep,
		HeadwayMax: raw.Headway.MaxStep,
		ChargeLen:  raw.Charging.FullChargeLenStep,
		Chargers:   raw.Charging.Chargers,
		Alpha:      raw.Economics.TicketPrice,
		Beta:       raw.Economics.FullChargeCost,
		Gamma:      raw.Economics.GammaUnitCost,
		Vehicles:   make([]model.Vehicle, len(raw.Fleet.SocMaxKWh)),
		Lines:      make([]model.Line, len(raw.Lines)),
	}
	for i, emax := range raw.Fleet.SocMaxKWh {
		inst.Vehicles[i] = model.Vehicle{ID: i, Emin: raw.Fleet.SocMinKWh, Emax: emax}
	}

	for l := range raw.Lines {
		ft, err := loadFTCSV(filepath.Join(ftDir, ftFiles[l]), inst.T)
		if err != nil {
			return nil, fmt.Errorf("line %d ft load: %w", l, err)
		}
		opCost := make([]float64, inst.T)
		inst.Lines[l] = model.Line{ID: l, TripStep: raw.Lines[l].TripSteps, Energy: raw.Lines[l].EnergyKWh, FT: ft, OpCost: opCost}
	}
	return inst, nil
}

func loadFTCSV(path string, T int) ([]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	recs, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("empty csv")
	}

	start := 0
	if len(recs[0]) >= 2 && strings.EqualFold(strings.TrimSpace(recs[0][0]), "no") {
		start = 1
	}
	ft := make([]float64, T)
	for _, row := range recs[start:] {
		if len(row) < 2 {
			continue
		}
		idx, err := strconv.Atoi(strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		if err != nil {
			continue
		}
		t := idx - 1
		if t >= 0 && t < T {
			ft[t] = v
		}
	}
	return ft, nil
}
