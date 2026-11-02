package collectors

import (
	"reflect"
	"strings"
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestParseSmartScan pins the smartctl --json --scan mapping: devices
// keep scan order, the version array renders as the tool's
// "major.minor" label, and an empty or missing device list is a
// legitimate non-nil empty scan. Malformed JSON is the decode error
// the Health round degrades on (§5).
func TestParseSmartScan(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		wantDevices []smartDevice
		wantVersion string
		wantErr     bool
	}{
		{
			name: "two devices keep scan order with the tool version",
			json: `{
				"smartctl": {"version": [7, 5], "argv": ["--scan"]},
				"devices": [
					{"name": "/dev/sda", "type": "scsi", "protocol": "SCSI"},
					{"name": "/dev/sdb", "type": "sat", "protocol": "ATA"}
				]
			}`,
			wantDevices: []smartDevice{
				{Name: "/dev/sda", Type: "scsi", Protocol: "SCSI"},
				{Name: "/dev/sdb", Type: "sat", Protocol: "ATA"},
			},
			wantVersion: "7.5",
		},
		{
			name:        "empty device array is a non-nil empty scan",
			json:        `{"smartctl": {"version": [7, 4]}, "devices": []}`,
			wantDevices: []smartDevice{},
			wantVersion: "7.4",
		},
		{
			name:        "missing device key is a non-nil empty scan",
			json:        `{"smartctl": {"version": [7, 5]}}`,
			wantDevices: []smartDevice{},
			wantVersion: "7.5",
		},
		{
			name:        "missing version reads empty",
			json:        `{"devices": [{"name": "/dev/nvme0", "type": "nvme", "protocol": "NVMe"}]}`,
			wantDevices: []smartDevice{{Name: "/dev/nvme0", Type: "nvme", Protocol: "NVMe"}},
			wantVersion: "",
		},
		{
			name:        "single version entry reads as the major",
			json:        `{"smartctl": {"version": [7]}}`,
			wantDevices: []smartDevice{},
			wantVersion: "7",
		},
		{
			name:    "malformed json errors",
			json:    `{"devices": [`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSmartScan([]byte(tt.json))
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseSmartScan(malformed) = nil error, want a decode error")
				}
				if !strings.Contains(err.Error(), "decode smartctl scan") {
					t.Errorf("parseSmartScan(malformed) error = %v, want the decode smartctl scan prefix", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSmartScan: %v", err)
			}
			if got.Devices == nil {
				t.Fatal("parseSmartScan devices = nil, want a non-nil slice")
			}
			if !reflect.DeepEqual(got.Devices, tt.wantDevices) {
				t.Errorf("parseSmartScan devices = %#v, want %#v", got.Devices, tt.wantDevices)
			}
			if got.Version != tt.wantVersion {
				t.Errorf("parseSmartScan version = %q, want %q", got.Version, tt.wantVersion)
			}
		})
	}
}

// TestParseSmartDevice pins the per-device JSON mapping and the
// presence-flag contract: verdict, temperature and power-on time are
// data with explicit "was it present" bits, so a device without a
// verdict still yields a row (SCSI) and a real zero stays a value
// instead of reading as unknown. NVMe drives report the two
// attributes in the health log instead of the top-level blocks, and
// SCSI drives carry only scsi_model_name.
func TestParseSmartDevice(t *testing.T) {
	tests := []struct {
		name         string
		json         string
		want         core.DiskHealth
		wantMessages []smartMessage
		wantErr      bool
	}{
		{
			name: "ATA healthy disk maps verdict and attributes",
			json: `{
				"device": {"name": "/dev/sda", "type": "scsi", "protocol": "ATA"},
				"model_name": "Samsung SSD 860 EVO 1TB",
				"smart_status": {"passed": true},
				"temperature": {"current": 32},
				"power_on_time": {"hours": 1234}
			}`,
			want: core.DiskHealth{
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
		},
		{
			name: "ATA failing disk is a row, not an error",
			json: `{
				"device": {"name": "/dev/sdb", "type": "sat", "protocol": "ATA"},
				"model_name": "WDC WD20EARX",
				"smart_status": {"passed": false}
			}`,
			want: core.DiskHealth{
				Device:     "/dev/sdb",
				Model:      "WDC WD20EARX",
				Protocol:   "ATA",
				Passed:     false,
				HasVerdict: true,
			},
		},
		{
			name: "device without smart_status maps attributes as UNKNOWN verdict",
			json: `{
				"device": {"name": "/dev/sdc", "type": "scsi", "protocol": "SCSI"},
				"scsi_model_name": "SEAGATE ST3300657SS",
				"temperature": {"current": 41},
				"power_on_time": {"hours": 88}
			}`,
			want: core.DiskHealth{
				Device:       "/dev/sdc",
				Model:        "SEAGATE ST3300657SS",
				Protocol:     "SCSI",
				HasVerdict:   false,
				TemperatureC: 41,
				HasTemp:      true,
				PowerOnHours: 88,
				HasPowerOn:   true,
			},
		},
		{
			name: "NVMe health log supplies missing top-level attributes",
			json: `{
				"device": {"name": "/dev/nvme0n1", "type": "nvme", "protocol": "NVMe"},
				"model_name": "WD Black SN850",
				"smart_status": {"passed": true},
				"nvme_smart_health_information_log": {
					"temperature": 38,
					"power_on_hours": 500
				}
			}`,
			want: core.DiskHealth{
				Device:       "/dev/nvme0n1",
				Model:        "WD Black SN850",
				Protocol:     "NVMe",
				Passed:       true,
				HasVerdict:   true,
				TemperatureC: 38,
				HasTemp:      true,
				PowerOnHours: 500,
				HasPowerOn:   true,
			},
		},
		{
			name: "explicit zero temperature and power-on are present values",
			json: `{
				"device": {"name": "/dev/sdd", "type": "sat", "protocol": "ATA"},
				"smart_status": {"passed": true},
				"temperature": {"current": 0},
				"power_on_time": {"hours": 0}
			}`,
			want: core.DiskHealth{
				Device:       "/dev/sdd",
				Protocol:     "ATA",
				Passed:       true,
				HasVerdict:   true,
				TemperatureC: 0,
				HasTemp:      true,
				PowerOnHours: 0,
				HasPowerOn:   true,
			},
		},
		{
			name: "absent model_name falls back to scsi_model_name",
			json: `{
				"device": {"name": "/dev/sde", "protocol": "SCSI"},
				"scsi_model_name": "  IBM-ESXS ST900MM0006  "
			}`,
			want: core.DiskHealth{Device: "/dev/sde", Model: "IBM-ESXS ST900MM0006", Protocol: "SCSI"},
		},
		{
			name: "blank model_name falls back to scsi_model_name",
			json: `{
				"device": {"name": "/dev/sdf", "protocol": "SCSI"},
				"model_name": "   ",
				"scsi_model_name": "HP EG0900FBVFQ"
			}`,
			want: core.DiskHealth{Device: "/dev/sdf", Model: "HP EG0900FBVFQ", Protocol: "SCSI"},
		},
		{
			name: "messages come back verbatim",
			json: `{
				"device": {"name": "/dev/sda"},
				"smartctl": {"messages": [
					{"string": "Smartctl open device: /dev/sda failed: Permission denied", "severity": "error"},
					{"string": "Read SMART Data failed", "severity": "information"}
				]}
			}`,
			want: core.DiskHealth{Device: "/dev/sda"},
			wantMessages: []smartMessage{
				{String: "Smartctl open device: /dev/sda failed: Permission denied", Severity: "error"},
				{String: "Read SMART Data failed", Severity: "information"},
			},
		},
		{
			name:    "malformed json errors",
			json:    `{"device":`,
			wantErr: true,
		},
		{
			name:    "empty input errors",
			json:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, messages, err := parseSmartDevice([]byte(tt.json))
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseSmartDevice(malformed) = nil error, want a decode error")
				}
				if !strings.Contains(err.Error(), "decode smartctl device") {
					t.Errorf("parseSmartDevice(malformed) error = %v, want the decode smartctl device prefix", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSmartDevice: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSmartDevice = %#v, want %#v", got, tt.want)
			}
			if !reflect.DeepEqual(messages, tt.wantMessages) {
				t.Errorf("parseSmartDevice messages = %#v, want %#v", messages, tt.wantMessages)
			}
		})
	}
}

// TestParseSmartVersion pins the smartctl --version text grammar: the
// first line must start with the binary name and a numeric version,
// the minor part is optional, and anything else — including the
// multi-line build output smartctl prints — is not a version. The
// collector uses this to tell an old tool from a decode failure.
func TestParseSmartVersion(t *testing.T) {
	tests := []struct {
		name      string
		out       string
		wantMajor int
		wantMinor int
		wantOK    bool
	}{
		{
			name:      "smartctl 7.5 release line",
			out:       "smartctl 7.5 2025-04-30 r5714 [x86_64-linux-6.11.0] (local build)\nCopyright (C) 2002-25\n",
			wantMajor: 7,
			wantMinor: 5,
			wantOK:    true,
		},
		{
			name:      "smartctl 6.6 release line",
			out:       "smartctl 6.6 2017-11-05 r4594 [x86_64-linux-6.10.0] (local build)",
			wantMajor: 6,
			wantMinor: 6,
			wantOK:    true,
		},
		{
			name:      "lone major is accepted with minor zero",
			out:       "smartctl 7",
			wantMajor: 7,
			wantMinor: 0,
			wantOK:    true,
		},
		{
			name:   "garbage is not a version",
			out:    "smartmontools version unknown\n",
			wantOK: false,
		},
		{
			name:   "empty output is not a version",
			out:    "",
			wantOK: false,
		},
		{
			name:   "non-numeric version is not a version",
			out:    "smartctl seven\n",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor, ok := parseSmartVersion([]byte(tt.out))
			if ok != tt.wantOK {
				t.Fatalf("parseSmartVersion(%q) ok = %v, want %v", tt.out, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if major != tt.wantMajor || minor != tt.wantMinor {
				t.Errorf("parseSmartVersion(%q) = %d.%d, want %d.%d", tt.out, major, minor, tt.wantMajor, tt.wantMinor)
			}
		})
	}
}

// TestSmartctlJSONSupported pins the JSON capability threshold: -j
// arrived in smartctl 7.0, so the major version decides.
func TestSmartctlJSONSupported(t *testing.T) {
	tests := []struct {
		name         string
		major, minor int
		want         bool
	}{
		{"7.0 is the first JSON release", 7, 0, true},
		{"7.5 supports JSON", 7, 5, true},
		{"6.6 predates JSON", 6, 6, false},
		{"8.0 supports JSON", 8, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := smartctlJSONSupported(tt.major, tt.minor); got != tt.want {
				t.Errorf("smartctlJSONSupported(%d, %d) = %v, want %v", tt.major, tt.minor, got, tt.want)
			}
		})
	}
}

// TestSmartPermissionDenied pins the privilege-refusal detector: the
// two spellings smartctl uses on an unprivileged session match
// case-insensitively, and an unrelated message never reads as a
// permission failure.
func TestSmartPermissionDenied(t *testing.T) {
	tests := []struct {
		name     string
		messages []smartMessage
		want     bool
	}{
		{
			name:     "permission denied",
			messages: []smartMessage{{String: "Smartctl open device: /dev/sda failed: Permission denied", Severity: "error"}},
			want:     true,
		},
		{
			name:     "operation not permitted",
			messages: []smartMessage{{String: "Operation not permitted", Severity: "error"}},
			want:     true,
		},
		{
			name:     "case-insensitive match",
			messages: []smartMessage{{String: "PERMISSION DENIED opening device", Severity: "error"}},
			want:     true,
		},
		{
			name:     "unrelated message is not a permission failure",
			messages: []smartMessage{{String: "Read SMART Data failed: checksum error", Severity: "error"}},
			want:     false,
		},
		{
			name:     "no messages is not a permission failure",
			messages: nil,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := smartPermissionDenied(tt.messages); got != tt.want {
				t.Errorf("smartPermissionDenied(%#v) = %v, want %v", tt.messages, got, tt.want)
			}
		})
	}
}
