package collectors_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/collectors"
	"github.com/felipecastillo-b/serverctl/internal/core"
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

// writeSensorFiles writes one hwmon fixture file per map entry into dir.
func writeSensorFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
}

func TestSensorsSymlinkedHwmonIsDetected(t *testing.T) {
	// Real sysfs exposes /sys/class/hwmon/hwmonN as a SYMLINK into
	// /sys/devices, and DirEntry.IsDir() is false for symlinks — the
	// production bug this test pins. The walk must follow the link.
	root := t.TempDir()
	deviceDir := filepath.Join(root, "sys", "devices", "platform", "coretemp.0", "hwmon", "hwmon0")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatalf("mkdir device dir: %v", err)
	}
	writeSensorFiles(t, deviceDir, map[string]string{
		"name":        "coretemp\n",
		"temp1_input": "43000\n",
		"temp1_label": "Package id 0\n",
	})

	classDir := filepath.Join(root, "sys", "class", "hwmon")
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		t.Fatalf("mkdir class dir: %v", err)
	}
	if err := os.Symlink(deviceDir, filepath.Join(classDir, "hwmon0")); err != nil {
		t.Fatalf("symlink hwmon0: %v", err)
	}

	readings, err := collectors.New(root).Sensors()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readings) != 1 {
		t.Fatalf("got %d readings, want 1: %+v", len(readings), readings)
	}
	want := core.SensorReading{Name: "coretemp", Label: "Package id 0", Celsius: 43.0}
	if readings[0] != want {
		t.Errorf("reading = %+v, want %+v", readings[0], want)
	}
}

func TestSensorsLegacyDeviceDirIsRead(t *testing.T) {
	// Older drivers keep their temp files under <hwmon>/device; the class
	// dir carries only the name. The fallback must find them.
	root := t.TempDir()
	dir := filepath.Join(root, "sys", "class", "hwmon", "hwmon0")
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	writeSensorFiles(t, dir, map[string]string{"name": "legacychip\n"})
	writeSensorFiles(t, filepath.Join(dir, "device"), map[string]string{
		"temp1_input": "51000\n",
		"temp1_label": "Chip\n",
	})

	readings, err := collectors.New(root).Sensors()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readings) != 1 {
		t.Fatalf("got %d readings, want 1: %+v", len(readings), readings)
	}
	want := core.SensorReading{Name: "legacychip", Label: "Chip", Celsius: 51.0}
	if readings[0] != want {
		t.Errorf("reading = %+v, want %+v", readings[0], want)
	}
}

func TestSensorsDeviceDirNotReadWhenClassDirHasTemps(t *testing.T) {
	// When the class dir already yields temps, the device/ fallback must
	// not run — a real device exposes the same sensor in both places, and
	// double counting would show it twice on the dashboard.
	root := t.TempDir()
	dir := filepath.Join(root, "sys", "class", "hwmon", "hwmon0")
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	writeSensorFiles(t, dir, map[string]string{
		"name":        "chip\n",
		"temp1_input": "40000\n",
	})
	writeSensorFiles(t, filepath.Join(dir, "device"), map[string]string{
		"temp1_input": "41000\n",
	})

	readings, err := collectors.New(root).Sensors()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readings) != 1 || readings[0].Celsius != 40.0 {
		t.Errorf("readings = %+v, want only the single 40.0 class-dir sensor", readings)
	}
}
