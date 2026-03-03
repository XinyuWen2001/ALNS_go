package instance

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Instance 存储全局参数和模型系数
type Instance struct {
	Name string

	StepMinutes int
	T           int // 对应模型 T_max

	// 经济参数（模型目标函数系数）
	TicketPrice    float64 // 对应模型 alpha 相关项
	FullChargeCost float64 // 对应模型 beta (充电固定成本)
	Gamma          float64 // 对应模型 gamma (非运营期补电系数)

	// 时刻表约束参数
	HeadwayMin int // 对应模型 δ1
	HeadwayMax int // 对应模型 δ2

	// 充电资源约束参数
	Chargers  int // 对应模型 K
	ChargeLen int // 对应模型 τ

	Lines    []Line
	Vehicles []Vehicle

	// FT[l][t] 对应模型客流价值 f_l^t
	FT [][]float64
}

type Line struct {
	TripSteps int     `json:"trip_steps"` // 对应模型 δ3^l
	EnergyKWh float64 `json:"energy_kwh"` // 对应模型 η_l
}

type Vehicle struct {
	Emin float64 // 对应模型 E_n^min
	Emax float64 // 对应模型 E_n^max
}

// -------------------

// rawInstance 用于解析 JSON 结构的中间体
type rawInstance struct {
	Meta struct {
		Name string `json:"name"`
	} `json:"meta"`

	Time struct {
		StepMinutes    int `json:"step_minutes"`
		OperatingSteps int `json:"operating_steps"`
	} `json:"time"`

	Economics struct {
		TicketPrice    float64 `json:"ticket_price"`
		FullChargeCost float64 `json:"full_charge_cost"`
		GammaUnitCost  float64 `json:"gamma_unit_cost"` // 对应模型 gamma 参数
	} `json:"economics"`

	Headway struct {
		MinStep int `json:"min_step"`
		MaxStep int `json:"max_step"`
	} `json:"headway"`

	Charging struct {
		Chargers          int `json:"chargers"`
		FullChargeLenStep int `json:"full_charge_len_step"`
	} `json:"charging"`

	Lines []Line `json:"lines"`

	Fleet struct {
		SocMinKWh float64   `json:"soc_min_kwh"`
		SocMaxKWh []float64 `json:"soc_max_kwh"`
	} `json:"fleet"`

	FTFiles []string `json:"ft_files"`
}

// LoadJSON 解析 instance.json 并进行基础校验
func LoadJSON(path string) (*Instance, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read instance json: %w", err)
	}

	var raw rawInstance
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, fmt.Errorf("unmarshal instance json: %w", err)
	}

	inst := &Instance{
		Name: raw.Meta.Name,

		StepMinutes: raw.Time.StepMinutes,
		T:           raw.Time.OperatingSteps,

		TicketPrice:    raw.Economics.TicketPrice,
		FullChargeCost: raw.Economics.FullChargeCost,
		Gamma:          raw.Economics.GammaUnitCost, // 加载模型 gamma 项

		HeadwayMin: raw.Headway.MinStep,
		HeadwayMax: raw.Headway.MaxStep,

		Chargers:  raw.Charging.Chargers,
		ChargeLen: raw.Charging.FullChargeLenStep,

		Lines: raw.Lines,
	}

	// 初始化车辆数据
	inst.Vehicles = make([]Vehicle, len(raw.Fleet.SocMaxKWh))
	for i, emax := range raw.Fleet.SocMaxKWh {
		inst.Vehicles[i] = Vehicle{
			Emin: raw.Fleet.SocMinKWh,
			Emax: emax,
		}
	}

	// 执行完整性校验
	if err := inst.Validate(); err != nil {
		return nil, nil, err
	}
	return inst, raw.FTFiles, nil
}

// Validate 确保输入参数满足模型的基本可行性前提
func (inst *Instance) Validate() error {
	if inst.StepMinutes <= 0 {
		return fmt.Errorf("invalid step_minutes: %d", inst.StepMinutes)
	}
	if inst.T <= 0 {
		return fmt.Errorf("invalid operating_steps: %d", inst.T)
	}
	if inst.HeadwayMin <= 0 || inst.HeadwayMax <= 0 || inst.HeadwayMin > inst.HeadwayMax {
		return fmt.Errorf("invalid headway: min=%d max=%d", inst.HeadwayMin, inst.HeadwayMax)
	}
	if inst.HeadwayMax > inst.T {
		return fmt.Errorf("headway_max (%d) > T (%d)", inst.HeadwayMax, inst.T)
	}
	if inst.ChargeLen <= 0 || inst.ChargeLen > inst.T {
		return fmt.Errorf("invalid charge_len_step: %d (T=%d)", inst.ChargeLen, inst.T)
	}
	if inst.Chargers <= 0 {
		return fmt.Errorf("invalid chargers: %d", inst.Chargers)
	}
	if len(inst.Lines) == 0 {
		return fmt.Errorf("no lines provided")
	}
	if len(inst.Vehicles) == 0 {
		return fmt.Errorf("no vehicles provided")
	}

	for l, line := range inst.Lines {
		if line.TripSteps <= 0 || line.TripSteps > inst.T {
			return fmt.Errorf("line %d invalid trip_steps=%d (T=%d)", l, line.TripSteps, inst.T)
		}
		if line.EnergyKWh <= 0 {
			return fmt.Errorf("line %d invalid energy_kwh=%v", l, line.EnergyKWh)
		}
	}

	for n, v := range inst.Vehicles {
		if v.Emin < 0 {
			return fmt.Errorf("vehicle %d invalid Emin=%v", n, v.Emin)
		}
		if v.Emax <= v.Emin {
			return fmt.Errorf("vehicle %d invalid Emax=%v <= Emin=%v", n, v.Emax, v.Emin)
		}
	}

	// 检查车辆电量是否足以支持该线路至少一次行程 (Emax - eta >= Emin)
	for l, line := range inst.Lines {
		ok := false
		for _, v := range inst.Vehicles {
			if v.Emax-line.EnergyKWh >= v.Emin {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("line %d cannot be served by any vehicle (Emax - eta < Emin)", l)
		}
	}

	return nil
}

// LoadFTFromDir 加载 FT 矩阵数据
func (inst *Instance) LoadFTFromDir(dir string, files []string) error {
	L := len(inst.Lines)
	if len(files) != L {
		return fmt.Errorf("ft files count mismatch: need %d, got %d", L, len(files))
	}
	inst.FT = make([][]float64, L)

	for l := 0; l < L; l++ {
		fp := filepath.Join(dir, files[l])
		ft, err := readFTCSV(fp, inst.T)
		if err != nil {
			return fmt.Errorf("read ft for line %d from %s: %w", l, fp, err)
		}
		inst.FT[l] = ft
	}
	return nil
}

// readFTCSV 具体读取 CSV 逻辑
func readFTCSV(path string, T int) ([]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	// 跳过表头
	if _, err := r.Read(); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	ft := make([]float64, 0, T)
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}
		if len(rec) < 2 {
			continue
		}

		vStr := strings.TrimSpace(rec[1])
		if vStr == "" {
			ft = append(ft, 0)
			continue
		}
		v, err := strconv.ParseFloat(vStr, 64)
		if err != nil {
			return nil, fmt.Errorf("parse ft value %q: %w", vStr, err)
		}
		ft = append(ft, v)
	}

	// 长度标准化为 T
	if len(ft) < T {
		ft = append(ft, make([]float64, T-len(ft))...)
	} else if len(ft) > T {
		ft = ft[:T]
	}
	return ft, nil
}