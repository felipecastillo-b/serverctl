package collectors_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
)

func TestSensorsFixture(t *testing.T) {
	sys := collectors.New("testdata")
	readings, err := sys.Sensors()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readings) != 2 {
		t.Fatalf("got %d readings, want 2: %+v", len(readings), readings)
	}

	// Sorted by device name, then label: "Package id 0" before "temp2".
	first, second := readings[0], readings[1]
	if first.Name != "coretemp" || first.Label != "Package id 0" || first.Celsius != 43.0 {
		t.Errorf("first sensor = %+v, want coretemp/Package id 0/43.0", first)
	}
	// temp2 has no label file: its label falls back to the sysfs base name.
	if second.Name != "coretemp" || second.Label != "temp2" || second.Celsius != 41.0 {
		t.Errorf("second sensor = %+v, want coretemp/temp2/41.0", second)
	}
}

func TestSensorsMissingHwmonIsEmptyNotError(t *testing.T) {
	// Headless servers without sensors: empty result, nil error.
	sys := collectors.New(t.TempDir())
	readings, err := sys.Sensors()
	if err != nil {
		t.Fatalf("missing hwmon must not error, got %v", err)
	}
	if len(readings) != 0 {
		t.Errorf("readings = %+v, want empty", readings)
	}
}

func TestSensorsMalformedValueIsSkipped(t *testing.T) {
	// One good sensor and one malformed input file in the same device.
	root := t.TempDir()
	dir := filepath.Join(root, "sys", "class", "hwmon", "hwmon0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	files := map[string]string{
		"name":        "testchip\n",
		"temp1_label": "good\n",
		"temp1_input": "55000\n",
		"temp2_input": "not-a-number\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	readings, err := collectors.New(root).Sensors()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readings) != 1 || readings[0].Celsius != 55.0 || readings[0].Label != "good" {
		t.Errorf("readings = %+v, want only the valid 55.0 sensor", readings)
	}
}
