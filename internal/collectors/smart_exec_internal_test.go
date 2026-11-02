package collectors

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// The fake smartctl payloads the exec tests serve: a scan with two
// devices, an ATA drive reporting the top-level attributes, an NVMe
// drive reporting them in its health log, and the version banner an
// old tool prints. The JSON keeps the live wire shapes smart.go
// decodes, so the exec path exercises the same grammar as the parser
// tests rather than a pre-trimmed fixture.
const (
	smartTestScanJSON = `{
		"smartctl": {"version": [7, 5], "argv": ["--scan"]},
		"devices": [
			{"name": "/dev/sda", "type": "scsi", "protocol": "SCSI"},
			{"name": "/dev/sdb", "type": "sat", "protocol": "ATA"}
		]
	}`

	smartTestATAJSON = `{
		"device": {"name": "/dev/sda", "type": "scsi", "protocol": "ATA"},
		"model_name": "Samsung SSD 860 EVO 1TB",
		"smart_status": {"passed": true},
		"temperature": {"current": 32},
		"power_on_time": {"hours": 1234}
	}`

	smartTestNVMeJSON = `{
		"device": {"name": "/dev/sdb", "type": "sat", "protocol": "NVMe"},
		"model_name": "WD Black SN850",
		"smart_status": {"passed": true},
		"nvme_smart_health_information_log": {
			"temperature": 38,
			"power_on_hours": 500
		}
	}`

	smartTestVersionText = "smartctl 7.5 2025-04-30 r5714 [x86_64-linux] (local build)\n"
)

// newSmartShimDir returns a short temp directory that holds one
// test's fake smartctl and puts it on PATH. os.MkdirTemp (not
// t.TempDir) mirrors the socket tests: the script path stays short,
// and the test owns the cleanup. PATH carries only this directory,
// so the shim's /bin/cat calls are absolute and the test can never
// fall through to a real smartctl on the host.
func newSmartShimDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "srvctl-smart-")
	if err != nil {
		t.Fatalf("mkdir temp smartctl dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("PATH", dir)
	return dir
}

// writeSmartPayload writes one JSON (or version text) payload into
// the shim directory. The script cats it by absolute path.
func writeSmartPayload(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write smartctl payload %s: %v", name, err)
	}
}

// installSmartShim writes the executable fake smartctl into dir. The
// shared prologue logs every invocation's argv to argv.log — the
// marker the invalid-target test reads and the fixture test asserts
// absent — and the caller-supplied case arms dispatch on "$*". The
// directory is embedded as a shell-quoted absolute path.
func installSmartShim(t *testing.T, dir, dispatch string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"dir=" + strconv.Quote(dir) + "\n" +
		"printf '%s\\n' \"$*\" >> \"$dir/argv.log\"\n" +
		"case \"$*\" in\n" +
		dispatch +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, smartctlBinary), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake smartctl: %v", err)
	}
}

// smartDeniedPayload is the JSON smartctl prints when it cannot open
// a device as a regular user, with the permission message the
// collector classifies. The exit status is handled by the script, not
// this payload.
func smartDeniedPayload(device string) string {
	return `{
		"device": {"name": "` + device + `", "type": "sat", "protocol": "ATA"},
		"smartctl": {"messages": [
			{"string": "Smartctl open device: ` + device + ` failed: Permission denied", "severity": "error"}
		]}
	}`
}

// TestDiskHealthRound pins the success path end to end: the scan is
// followed in order, every device gets its own read, and the JSON
// maps into rows with the version label the screen shows.
func TestDiskHealthRound(t *testing.T) {
	dir := newSmartShimDir(t)
	writeSmartPayload(t, dir, "scan.json", smartTestScanJSON)
	writeSmartPayload(t, dir, "version.txt", smartTestVersionText)
	writeSmartPayload(t, dir, "sda.json", smartTestATAJSON)
	writeSmartPayload(t, dir, "sdb.json", smartTestNVMeJSON)
	installSmartShim(t, dir, `*--scan*) /bin/cat "$dir/scan.json" ;;
*--version*) /bin/cat "$dir/version.txt" ;;
*"/dev/sda"*) /bin/cat "$dir/sda.json" ;;
*"/dev/sdb"*) /bin/cat "$dir/sdb.json" ;;
*) exit 1 ;;
`)

	report, err := New("/").DiskHealth()
	if err != nil {
		t.Fatalf("DiskHealth: %v", err)
	}
	if report.ToolVersion != "7.5" {
		t.Errorf("ToolVersion = %q, want 7.5", report.ToolVersion)
	}
	want := []core.DiskHealth{
		{
			Device:       "/dev/sda",
			Model:        "Samsung SSD 860 EVO 1TB",
			Protocol:     "ATA",
			Passed:       true,
			HasVerdict:   true,
			TemperatureC: 32,
			HasTemp:      true,
			PowerOnHours: 1234,
			HasPowerOn:   true,
		},
		{
			Device:       "/dev/sdb",
			Model:        "WD Black SN850",
			Protocol:     "NVMe",
			Passed:       true,
			HasVerdict:   true,
			TemperatureC: 38,
			HasTemp:      true,
			PowerOnHours: 500,
			HasPowerOn:   true,
		},
	}
	if !reflect.DeepEqual(report.Disks, want) {
		t.Errorf("DiskHealth rows = %#v, want %#v", report.Disks, want)
	}
}

// TestDiskHealthMissingBinary pins the optional-tool contract: with
// no smartctl on PATH the port returns the typed missing sentinel and
// the app keeps running. No process runs and nothing panics.
func TestDiskHealthMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := New("/").DiskHealth()
	if !errors.Is(err, core.ErrSmartctlMissing) {
		t.Fatalf("DiskHealth error = %v, want core.ErrSmartctlMissing", err)
	}
}

// TestDiskHealthFixtureSystemRefuses pins the live guard: exec has no
// root prefix, so a fixture System refuses before LookPath or any
// process. The shim's argv log proves smartctl never ran.
func TestDiskHealthFixtureSystemRefuses(t *testing.T) {
	dir := newSmartShimDir(t)
	installSmartShim(t, dir, "*) exit 1 ;;\n")

	_, err := New("testdata").DiskHealth()
	if err == nil {
		t.Fatal("a fixture System must refuse SMART collection")
	}
	if !strings.Contains(err.Error(), "requires the live smartctl binary") {
		t.Fatalf("DiskHealth error = %v, want the live smartctl requirement", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "argv.log")); statErr == nil {
		t.Fatal("DiskHealth executed smartctl on a fixture System")
	}
}

// TestDiskHealthUnprivileged pins the whole-round privilege state:
// every scanned device answers with a permission message and a
// non-zero (bitmask) exit, so the round is typed as unprivileged
// rather than reported as generic errors.
func TestDiskHealthUnprivileged(t *testing.T) {
	dir := newSmartShimDir(t)
	writeSmartPayload(t, dir, "scan.json", smartTestScanJSON)
	writeSmartPayload(t, dir, "sda.json", smartDeniedPayload("/dev/sda"))
	writeSmartPayload(t, dir, "sdb.json", smartDeniedPayload("/dev/sdb"))
	installSmartShim(t, dir, `*--scan*) /bin/cat "$dir/scan.json" ;;
*"/dev/sda"*) /bin/cat "$dir/sda.json"; exit 2 ;;
*"/dev/sdb"*) /bin/cat "$dir/sdb.json"; exit 2 ;;
*) exit 1 ;;
`)

	_, err := New("/").DiskHealth()
	if !errors.Is(err, core.ErrSmartUnprivileged) {
		t.Fatalf("DiskHealth error = %v, want core.ErrSmartUnprivileged", err)
	}
}

// TestDiskHealthOldSmartctl pins the unsupported-tool state: the
// JSON scan fails to decode, the version probe reads 6.6, and the
// collector reports the typed sentinel naming the found version.
func TestDiskHealthOldSmartctl(t *testing.T) {
	dir := newSmartShimDir(t)
	writeSmartPayload(t, dir, "version.txt", "smartctl 6.6 2017-11-05 r4594 [x86_64-linux] (local build)\n")
	installSmartShim(t, dir, `*--scan*) exit 1 ;;
*--version*) /bin/cat "$dir/version.txt" ;;
*) exit 1 ;;
`)

	_, err := New("/").DiskHealth()
	if !errors.Is(err, core.ErrSmartctlUnsupported) {
		t.Fatalf("DiskHealth error = %v, want core.ErrSmartctlUnsupported", err)
	}
	if !strings.Contains(err.Error(), "smartctl 6.6") {
		t.Errorf("DiskHealth error = %v, want the found 6.6 version", err)
	}
}

// TestDiskHealthPartialRound pins the per-device partial contract:
// one denied device is an UNKNOWN row, not a failed round, and its
// healthy sibling keeps its verdict.
func TestDiskHealthPartialRound(t *testing.T) {
	dir := newSmartShimDir(t)
	writeSmartPayload(t, dir, "scan.json", smartTestScanJSON)
	writeSmartPayload(t, dir, "sda.json", smartTestATAJSON)
	writeSmartPayload(t, dir, "sdb.json", smartDeniedPayload("/dev/sdb"))
	installSmartShim(t, dir, `*--scan*) /bin/cat "$dir/scan.json" ;;
*"/dev/sda"*) /bin/cat "$dir/sda.json" ;;
*"/dev/sdb"*) /bin/cat "$dir/sdb.json"; exit 2 ;;
*) exit 1 ;;
`)

	report, err := New("/").DiskHealth()
	if err != nil {
		t.Fatalf("DiskHealth: %v", err)
	}
	if len(report.Disks) != 2 {
		t.Fatalf("DiskHealth rows = %d, want 2", len(report.Disks))
	}
	if report.Disks[0].Device != "/dev/sda" || !report.Disks[0].HasVerdict {
		t.Errorf("first row = %#v, want /dev/sda with a verdict", report.Disks[0])
	}
	if report.Disks[1].Device != "/dev/sdb" || report.Disks[1].HasVerdict {
		t.Errorf("second row = %#v, want /dev/sdb without a verdict", report.Disks[1])
	}
}

// TestDiskHealthEmptyScan pins the empty-host contract: no SMART
// devices is a coherent round with a non-nil empty listing and the
// tool version preserved.
func TestDiskHealthEmptyScan(t *testing.T) {
	dir := newSmartShimDir(t)
	writeSmartPayload(t, dir, "scan.json", `{"smartctl": {"version": [7, 5]}, "devices": []}`)
	installSmartShim(t, dir, `*--scan*) /bin/cat "$dir/scan.json" ;;
*) exit 1 ;;
`)

	report, err := New("/").DiskHealth()
	if err != nil {
		t.Fatalf("DiskHealth: %v", err)
	}
	if report.Disks == nil {
		t.Fatal("DiskHealth Disks = nil, want a non-nil empty slice")
	}
	if len(report.Disks) != 0 {
		t.Errorf("DiskHealth rows = %d, want 0", len(report.Disks))
	}
	if report.ToolVersion != "7.5" {
		t.Errorf("ToolVersion = %q, want 7.5", report.ToolVersion)
	}
}

// TestDiskHealthSkipsInvalidTargets pins the argv defense: a scan
// entry with traversal in its name or a metacharacter in its type
// never reaches exec, while a valid sibling still produces its row.
// The shim's argv log is the proof of what did — and did not — run.
func TestDiskHealthSkipsInvalidTargets(t *testing.T) {
	dir := newSmartShimDir(t)
	writeSmartPayload(t, dir, "scan.json", `{
		"smartctl": {"version": [7, 5]},
		"devices": [
			{"name": "/dev/../etc/passwd", "type": "scsi", "protocol": "SCSI"},
			{"name": "/dev/sda", "type": "sat;x", "protocol": "ATA"},
			{"name": "/dev/sdb", "type": "sat", "protocol": "ATA"}
		]
	}`)
	writeSmartPayload(t, dir, "sdb.json", smartTestNVMeJSON)
	installSmartShim(t, dir, `*--scan*) /bin/cat "$dir/scan.json" ;;
*"/dev/sdb"*) /bin/cat "$dir/sdb.json" ;;
*) exit 1 ;;
`)

	report, err := New("/").DiskHealth()
	if err != nil {
		t.Fatalf("DiskHealth: %v", err)
	}
	if len(report.Disks) != 1 || report.Disks[0].Device != "/dev/sdb" {
		t.Fatalf("DiskHealth rows = %#v, want only /dev/sdb", report.Disks)
	}
	log, err := os.ReadFile(filepath.Join(dir, "argv.log"))
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	if strings.Contains(string(log), "passwd") {
		t.Errorf("invalid name reached argv:\n%s", log)
	}
	if strings.Contains(string(log), "sat;x") {
		t.Errorf("invalid type reached argv:\n%s", log)
	}
	if !strings.Contains(string(log), "/dev/sdb") {
		t.Errorf("valid device never reached argv:\n%s", log)
	}
}

// TestValidSmartTarget pins the scan-derived argv validation: only
// absolute /dev/ names without traversal, whitespace or control
// characters and a short smartctl type token pass.
func TestValidSmartTarget(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		devType string
		want    bool
	}{
		{name: "plain device with no type", target: "/dev/sda", want: true},
		{name: "sat type", target: "/dev/sda", devType: "sat", want: true},
		{name: "scsi type", target: "/dev/sda", devType: "scsi", want: true},
		{name: "nvme type", target: "/dev/nvme0n1", devType: "nvme", want: true},
		{name: "megaraid volume type", target: "/dev/sda", devType: "megaraid,0", want: true},
		{name: "type may carry underscore and plus", target: "/dev/sda", devType: "a_b+c-1", want: true},
		{name: "relative name", target: "sda", devType: "sat", want: false},
		{name: "traversal name", target: "/dev/../etc/passwd", devType: "scsi", want: false},
		{name: "traversal inside device name", target: "/dev/sda..", devType: "sat", want: false},
		{name: "whitespace in name", target: "/dev/sd a", devType: "sat", want: false},
		{name: "tab in name", target: "/dev/sda\t", devType: "sat", want: false},
		{name: "control byte in name", target: "/dev/sda\x00", devType: "sat", want: false},
		{name: "shell metacharacter in type", target: "/dev/sda", devType: "sat;x", want: false},
		{name: "space in type", target: "/dev/sda", devType: "megaraid 0", want: false},
		{name: "path syntax in type", target: "/dev/sda", devType: "sat/../x", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSmartTarget(tt.target, tt.devType); got != tt.want {
				t.Errorf("validSmartTarget(%q, %q) = %v, want %v", tt.target, tt.devType, got, tt.want)
			}
		})
	}
}
