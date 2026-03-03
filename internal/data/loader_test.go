package data

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadInstance(t *testing.T) {
	d := t.TempDir()
	jsonPath := filepath.Join(d, "instance.json")
	if err := os.WriteFile(jsonPath, []byte(`{
  "meta": {"name":"demo"},
  "time": {"operating_steps": 6},
  "economics": {"ticket_price": 2, "full_charge_cost": 100, "gamma_unit_cost": 0.5},
  "headway": {"min_step": 2, "max_step": 3},
  "charging": {"chargers": 2, "full_charge_len_step": 2},
  "lines": [{"trip_steps": 2, "energy_kwh": 10}],
  "fleet": {"soc_min_kwh": 4, "soc_max_kwh": [20,18]}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ftPath := filepath.Join(d, "ft_1.csv")
	if err := os.WriteFile(ftPath, []byte("no,ft\n1,1\n2,2\n3,3\n4,4\n5,5\n6,6\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, err := LoadInstance(jsonPath, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inst.T != 6 || len(inst.Lines) != 1 || len(inst.Vehicles) != 2 {
		t.Fatalf("unexpected instance shape: %+v", inst)
	}
	if inst.Lines[0].FT[5] != 6 {
		t.Fatalf("ft parse failed: got %.2f", inst.Lines[0].FT[5])
	}
}
