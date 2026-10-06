package collectors

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// Sensors walks sysfs hwmon (root/sys/class/hwmon/hwmon*) and returns every
// temperature sensor found. Headless servers frequently expose NO sensors,
// so a missing hwmon tree is an empty result, not an error. A malformed
// individual sensor file is skipped without failing the whole read.
func (s System) Sensors() ([]core.SensorReading, error) {
	hwmonRoot := s.sys("class", "hwmon")
	entries, err := os.ReadDir(hwmonRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hwmon: %w", err)
	}

	var readings []core.SensorReading
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(hwmonRoot, entry.Name())
		name := readSensorName(dir, entry.Name())
		readings = append(readings, readHwmonDir(dir, name)...)
	}

	sort.Slice(readings, func(i, j int) bool {
		if readings[i].Name != readings[j].Name {
			return readings[i].Name < readings[j].Name
		}
		return readings[i].Label < readings[j].Label
	})
	return readings, nil
}

// readSensorName reads the hwmon device name, falling back to the directory
// name when the name file is absent.
func readSensorName(dir, fallback string) string {
	name, err := readTrimmed(filepath.Join(dir, "name"))
	if err != nil || name == "" {
		return fallback
	}
	return name
}

// readHwmonDir parses every tempN_input (+ optional tempN_label) pair of one
// hwmon device directory.
func readHwmonDir(dir, name string) []core.SensorReading {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var readings []core.SensorReading
	for _, entry := range entries {
		base, ok := strings.CutSuffix(entry.Name(), "_input")
		if !ok || !strings.HasPrefix(base, "temp") {
			continue
		}
		raw, err := readTrimmed(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // Unreadable sensor: skip, never abort the walk.
		}
		milli, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue // Malformed value: same policy.
		}
		label, err := readTrimmed(filepath.Join(dir, base+"_label"))
		if err != nil || label == "" {
			label = base // e.g. "temp2" when the kernel gives no label.
		}
		readings = append(readings, core.SensorReading{
			Name:    name,
			Label:   label,
			Celsius: milli / 1000, // hwmon reports millidegrees Celsius.
		})
	}
	return readings
}
