// smart.go holds the pure half of the Health module (ARCHITECTURE.md
// §3, §4): the wire shapes and decoders for smartctl's JSON output,
// plus the text and predicate helpers the live collector builds on.
// Everything here is pure — bytes in, domain data out — so the JSON
// grammar and the mapping rules are table-tested without smartctl, a
// device or a live host (§10). Nothing here performs I/O; the guarded
// exec adapter lives in a later slice.

package collectors

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// smartctlBinary is the executable name the Health module looks up on
// PATH and invokes through fixed argv. It is optional: a host without
// smartmontools reads as core.ErrSmartctlMissing, never a crash.
const smartctlBinary = "smartctl"

// smartScanResult is what parseSmartScan yields: the scanned devices
// in scan order and the smartctl version that reported them ("" when
// the scan JSON did not say).
type smartScanResult struct {
	Devices []smartDevice
	Version string
}

// smartDevice is one entry of smartctl --scan: the kernel device path,
// the device type to pass back as -d (e.g. "sat", "scsi", "nvme"), and
// the transport protocol smartctl printed.
type smartDevice struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
}

// smartMessage is one entry of smartctl's messages array: the text
// smartctl printed about a read and its severity ("error",
// "information", ...). The parsers return them verbatim; the live
// collector interprets them (permission detection, partial rounds).
type smartMessage struct {
	String   string `json:"string"`
	Severity string `json:"severity"`
}

// smartScanWire mirrors the fields of smartctl --json --scan that
// serverctl consumes. The tool also reports argv and build details;
// decoding only the version and the device list keeps the mapping
// honest about what it depends on.
type smartScanWire struct {
	Smartctl smartScanSmartctlWire `json:"smartctl"`
	Devices  []smartDevice         `json:"devices"`
}

// smartScanSmartctlWire carries the tool metadata of a scan response.
type smartScanSmartctlWire struct {
	Version []int `json:"version"`
}

// smartDeviceWire mirrors the fields of smartctl -j -a that
// serverctl consumes. Optional blocks are pointers so an absent block
// stays distinguishable from a present zero value — a real 0 °C or a
// zero-hour drive is a value, not "unknown".
type smartDeviceWire struct {
	Device        smartDevice           `json:"device"`
	ModelName     string                `json:"model_name"`
	SCSIModelName string                `json:"scsi_model_name"`
	SmartStatus   *smartStatusWire      `json:"smart_status"`
	Temperature   *smartTemperatureWire `json:"temperature"`
	PowerOnTime   *smartPowerOnWire     `json:"power_on_time"`
	NVMe          *smartNVMeWire        `json:"nvme_smart_health_information_log"`
	Smartctl      smartSmartctlWire     `json:"smartctl"`
}

type smartStatusWire struct {
	Passed bool `json:"passed"`
}

type smartTemperatureWire struct {
	Current int `json:"current"`
}

type smartPowerOnWire struct {
	Hours uint64 `json:"hours"`
}

// smartNVMeWire is the NVMe health log: drives that expose no
// top-level temperature or power_on_time report both here instead.
// The attribute pointers keep the same unknown-versus-zero contract
// as the top-level blocks.
type smartNVMeWire struct {
	Temperature  *int    `json:"temperature"`
	PowerOnHours *uint64 `json:"power_on_hours"`
}

type smartSmartctlWire struct {
	Messages []smartMessage `json:"messages"`
}

// parseSmartScan maps a smartctl --json --scan response into the
// scanned devices, in scan order, and the tool version string. It is
// pure: the caller owns the bounded exec and passes the bytes in.
//
// The version comes from smartctl's own version array — "major.minor"
// when at least two entries are present, "major" for a single entry
// and "" when the array is absent. An empty or missing device list is
// a legitimate empty scan (a host with no SMART devices) and yields
// an empty, non-nil slice; malformed JSON is the error the round
// degrades on (§5).
func parseSmartScan(data []byte) (smartScanResult, error) {
	var wire smartScanWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return smartScanResult{}, fmt.Errorf("decode smartctl scan: %w", err)
	}
	devices := make([]smartDevice, 0, len(wire.Devices))
	devices = append(devices, wire.Devices...)
	return smartScanResult{
		Devices: devices,
		Version: smartVersionString(wire.Smartctl.Version),
	}, nil
}

// smartVersionString renders the tool's version array the way the
// screen labels its source: "7.5" for [7 5], "7" for [7], "" when the
// tool did not report a version.
func smartVersionString(version []int) string {
	switch len(version) {
	case 0:
		return ""
	case 1:
		return strconv.Itoa(version[0])
	default:
		return fmt.Sprintf("%d.%d", version[0], version[1])
	}
}

// parseSmartDevice maps one smartctl -j -a response into a
// core.DiskHealth row and returns the tool's messages verbatim (nil
// when the tool printed none).
//
// Mapping rules: Device is device.name; Model is the trimmed
// model_name, falling back to the trimmed scsi_model_name (SCSI
// drives carry only the latter); Protocol is device.protocol. The
// verdict exists only when smart_status is present — an absent block
// leaves HasVerdict false so the screen shows UNKNOWN rather than a
// false PASSED. Temperature prefers the top-level temperature.current
// and falls back to the NVMe health log's temperature; power-on time
// prefers power_on_time.hours and falls back to the NVMe log's
// power_on_hours. Each fallback sets its Has* flag only when a source
// was actually present, so a real zero is never mistaken for unknown.
// Malformed or empty JSON is an explicit decode error.
func parseSmartDevice(data []byte) (core.DiskHealth, []smartMessage, error) {
	var wire smartDeviceWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return core.DiskHealth{}, nil, fmt.Errorf("decode smartctl device: %w", err)
	}

	health := core.DiskHealth{
		Device:   wire.Device.Name,
		Model:    smartModel(wire),
		Protocol: wire.Device.Protocol,
	}
	if wire.SmartStatus != nil {
		health.HasVerdict = true
		health.Passed = wire.SmartStatus.Passed
	}
	if wire.Temperature != nil {
		health.TemperatureC = wire.Temperature.Current
		health.HasTemp = true
	} else if wire.NVMe != nil && wire.NVMe.Temperature != nil {
		health.TemperatureC = *wire.NVMe.Temperature
		health.HasTemp = true
	}
	if wire.PowerOnTime != nil {
		health.PowerOnHours = wire.PowerOnTime.Hours
		health.HasPowerOn = true
	} else if wire.NVMe != nil && wire.NVMe.PowerOnHours != nil {
		health.PowerOnHours = *wire.NVMe.PowerOnHours
		health.HasPowerOn = true
	}
	return health, wire.Smartctl.Messages, nil
}

// smartModel picks the row's model string: smartctl's model_name when
// it carries text, else the SCSI model_name — the field SCSI drives
// populate instead — with surrounding whitespace trimmed either way.
func smartModel(wire smartDeviceWire) string {
	if model := strings.TrimSpace(wire.ModelName); model != "" {
		return model
	}
	return strings.TrimSpace(wire.SCSIModelName)
}

// parseSmartVersion reads the first line of `smartctl --version` and
// extracts the tool's major and minor version. The accepted shape is
// the first line smartctl prints — "smartctl 7.5 2025-04-30 r5714
// ..." — with tokens split on whitespace; a lone major ("smartctl 7")
// is accepted with minor 0. Anything else, including empty output, is
// ok=false: the caller uses this to distinguish an old tool from a
// decode failure, so guessing a version would erase that signal.
func parseSmartVersion(out []byte) (major, minor int, ok bool) {
	line, _, _ := strings.Cut(string(out), "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != smartctlBinary {
		return 0, 0, false
	}

	majorText, minorText, hasMinor := strings.Cut(fields[1], ".")
	var err error
	major, err = strconv.Atoi(majorText)
	if err != nil || major < 0 {
		return 0, 0, false
	}
	if !hasMinor {
		return major, 0, true
	}
	minor, err = strconv.Atoi(minorText)
	if err != nil || minor < 0 {
		return 0, 0, false
	}
	return major, minor, true
}

// smartctlJSONSupported reports whether the tool version can print
// JSON: smartctl 7.0 introduced the -j flag, so the major version is
// the whole test.
func smartctlJSONSupported(major, minor int) bool {
	return major >= 7
}

// smartPermissionDenied reports whether any of the tool's messages
// describes a privilege refusal. smartctl words an unreadable device
// as "Permission denied" on a normal user session and "Operation not
// permitted" on some hardened setups; a case-insensitive substring
// match covers both spellings without pinning the surrounding text.
func smartPermissionDenied(messages []smartMessage) bool {
	for _, message := range messages {
		text := strings.ToLower(message.String)
		if strings.Contains(text, "permission denied") || strings.Contains(text, "operation not permitted") {
			return true
		}
	}
	return false
}
