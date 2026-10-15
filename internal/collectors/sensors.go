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
// temperature sensor found, ordered by dashboard usefulness: CPU headline
// temps first, unknown chips last (sensorTier owns the ranking; the UI
// renders the slice as-is). Real sysfs exposes each hwmonN entry as a
// symlink into /sys/devices, so the walk never requires a directory, and
// older drivers keep their temp files in a legacy device/ subdir, read
// only when the class dir yields none. Headless servers frequently expose
// NO sensors, so a missing hwmon tree is an empty result, not an error. A
// malformed individual sensor file is skipped without failing the whole
// read.
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
		// Real sysfs exposes hwmonN as a SYMLINK into /sys/devices, and
		// entry.IsDir() is lstat-based (false for symlinks), so the walk
		// must not gate on it. A non-directory entry simply fails the
		// per-device reads below and is skipped gracefully.
		dir := filepath.Join(hwmonRoot, entry.Name())
		name := readSensorName(dir, entry.Name())
		found := readHwmonDir(dir, name)
		readings = append(readings, found...)
		if len(found) == 0 {
			// Legacy layout: older/odd drivers keep their temp files in
			// <hwmon>/device instead of the class dir. Read it only when
			// the class dir yielded no temps so readings never count twice.
			readings = append(readings, readHwmonDir(filepath.Join(dir, "device"), name)...)
		}
	}

	sort.Slice(readings, func(i, j int) bool {
		ti := sensorTier(readings[i].Name, readings[i].Label)
		tj := sensorTier(readings[j].Name, readings[j].Label)
		if ti != tj {
			return ti < tj
		}
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

// Chip classification tables for sensorTier. CPU, GPU and storage chips
// match exactly (canonical driver names); board and battery chips match by
// prefix because their names carry model suffixes (nct6775, it8720,
// w83627ehf, f71882fg, asusec, pch_cometlake, BAT0).
var (
	cpuChips = map[string]bool{
		"coretemp": true, "k10temp": true, "zenpower": true, "k8temp": true,
		"cpu_thermal": true, "x86_pkg_temp": true, "soc_thermal": true,
	}
	gpuChips = map[string]bool{
		"amdgpu": true, "radeon": true, "nouveau": true, "i915": true,
		"drm": true, "gpu_thermal": true,
	}
	storageChips = map[string]bool{
		"nvme": true, "drivetemp": true,
	}
	boardChipPrefixes = []string{
		"acpitz", "pch", "nct", "it8", "w836", "w837", "f718", "fintek", "asus",
	}
)

// hasChipPrefix reports whether chip starts with any of prefixes.
func hasChipPrefix(chip string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(chip, prefix) {
			return true
		}
	}
	return false
}

// sensorTier ranks one sensor for dashboard usefulness; Sensors sorts by
// tier first, so lower tiers render higher on the Dashboard.
//
// Tier table (chip and label are lowercased before matching):
//
//	 0- 2   CPU chips — coretemp, k10temp, zenpower, k8temp,
//	        cpu_thermal, x86_pkg_temp, soc_thermal:
//	        label package*/tdie = 0, tctl = 1, else 2
//	        (Core N, TccdN, ARM single sensors)
//	10-12   GPU chips — amdgpu, radeon, nouveau, i915, drm,
//	        gpu_thermal: label edge/gpu/core = 0,
//	        junction/hotspot = 1, else 2
//	20-21   storage chips — nvme, drivetemp:
//	        label composite/disk/drive = 0, else 1
//	30      board/chipset (prefix match): acpitz, pch, nct, it8,
//	        w836, w837, f718, fintek, asus
//	40      battery/power (prefix match): bat*, adp*
//	50      unknown chips
//
// Board is checked before battery, and battery deliberately has no "ac"
// prefix, so the ACPI thermal zone "acpitz" can never classify as an
// AC-adapter power chip.
func sensorTier(chip, label string) int {
	chip = strings.ToLower(chip)
	label = strings.ToLower(label)
	switch {
	case cpuChips[chip]:
		switch {
		case strings.HasPrefix(label, "package"), label == "tdie":
			return 0
		case label == "tctl":
			return 1
		default:
			return 2
		}
	case gpuChips[chip]:
		switch {
		case label == "edge", label == "gpu", label == "core":
			return 10
		case label == "junction", label == "hotspot":
			return 11
		default:
			return 12
		}
	case storageChips[chip]:
		if label == "composite" || label == "disk" || label == "drive" {
			return 20
		}
		return 21
	case hasChipPrefix(chip, boardChipPrefixes):
		return 30
	case strings.HasPrefix(chip, "bat"), strings.HasPrefix(chip, "adp"):
		return 40
	default:
		return 50
	}
}
