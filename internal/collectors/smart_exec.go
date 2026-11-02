// smart_exec.go holds the live half of the Health module
// (ARCHITECTURE.md §3, §8): the live, guarded exec adapter around the
// optional smartctl binary. The pure half — wire shapes, decode and
// mapping — lives in smart.go.
//
// smartctl is an optional tool, not a hard dependency: smartmontools
// may not be installed, may predate JSON output, or may be unable to
// open the drive as a regular user. Each of those is a typed state
// (core.ErrSmartctlMissing, core.ErrSmartctlUnsupported,
// core.ErrSmartUnprivileged), never a crash and never an escalation
// attempt: serverctl runs the tool it finds and reports the
// requirement the tool names (§7).
package collectors

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

const (
	// smartScanTimeout bounds the device scan. `smartctl -j --scan`
	// only probes the kernel's device list and finishes in
	// milliseconds live; five seconds is generous enough never to cut
	// a slow host short while keeping one wedged probe from eating the
	// whole round.
	smartScanTimeout = 5 * time.Second

	// smartDeviceTimeout bounds one device's health read. A single
	// `-a` read spins the drive up and walks its log pages; five
	// seconds covers a healthy disk and abandons one that hangs
	// instead of stalling the other devices.
	smartDeviceTimeout = 5 * time.Second

	// smartRoundTimeout bounds the whole collection: the scan, every
	// per-device read, and the version probe a decode failure may
	// trigger. Twenty seconds is four device timeouts — enough for a
	// small fleet of slow disks, short enough that a wedged drive
	// cannot stall the Health refresh chain for more than one round.
	smartRoundTimeout = 20 * time.Second

	// smartMaxDevices caps the devices one round reads. The scan is
	// host data, so its length is not trusted: a hundred entries (a
	// USB hub misreporting, a hostile smartctl) still costs at most
	// the round budget and never an unbounded number of execs.
	smartMaxDevices = 64
)

// smartDeviceTypePattern is the allowed grammar of the device type
// passed back as `-d`. smartctl's documented types are short tokens
// ("sat", "scsi", "nvme", "megaraid,0"), so letters, digits, comma,
// plus, underscore and hyphen cover every real spelling while
// rejecting shell metacharacters and path syntax.
var smartDeviceTypePattern = regexp.MustCompile(`^[A-Za-z0-9,+_-]+$`)

// runSmartctl runs smartctl with a fixed argv and returns its stdout
// alongside the process error. The stdout comes back even when the
// error is non-nil on purpose: smartctl's exit status is a bitmask,
// and bits 3–7 report disk-health conditions — failing drive, prefail
// attributes, error log — not an invocation failure. The JSON it
// printed is still complete and valid in that case, so the caller
// parses first and treats the bitmask as data. Stderr is deliberately
// left alone: the JSON carries the tool's messages, and a shell never
// enters the picture (§7).
func runSmartctl(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, smartctlBinary, args...).Output()
}

// validSmartTarget reports whether a scanned device may reach argv.
// It is defense in depth for scan-derived arguments: the scan is host
// data, and a device entry is untrusted input even when it comes from
// the tool serverctl chose to run. The name must be an absolute /dev/
// path with no traversal segment and no whitespace, control or NUL
// byte — characters that could split an argument or point the read at
// a file outside the device namespace. The type, when the scan
// supplies one, must match smartctl's short token grammar so it can
// never smuggle a flag or a separator into the fixed argv. Pure, so
// both halves are table-tested without a device or a process.
func validSmartTarget(name, devType string) bool {
	if !strings.HasPrefix(name, "/dev/") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	if devType != "" && !smartDeviceTypePattern.MatchString(devType) {
		return false
	}
	return true
}

// DiskHealth implements core.DiskHealthLister: one SMART round over
// the optional smartctl binary (ARCHITECTURE.md §3, §8). The live
// guard comes first because exec has no root prefix — a fixture
// System must never run the runner's own smartctl against the
// runner's own disks, the same refusal the rpm query, logind and the
// container socket keep. Then LookPath decides the missing-package
// state, and one round context bounds the scan and every device read.
//
// The scan is parsed even when smartctl exited non-zero: the tool's
// bitmask describes the disks, not the invocation. A scan that does
// not decode may mean a smartctl older than 7.0 (the first release
// with -j), so the version probe disambiguates an unsupported tool
// from a decode failure; only the decode error is reported when the
// version cannot explain it. Scan order is preserved and each device
// gets its own bounded read.
//
// Partial rounds are rows, not errors: a device that cannot be opened
// joins the listing as an UNKNOWN row (no verdict, protocol from the
// scan) while its siblings still report their health. Only the
// whole-round case — every row without a verdict and every answered
// read refused for lack of privilege — becomes the typed
// core.ErrSmartUnprivileged, so the screen can show the root hint.
// The sysfs disk list is a best-effort model fallback for drives
// smartctl does not name, and an empty scan is a legitimate answer
// (a host with no SMART devices), never an error.
func (s System) DiskHealth() (core.DiskHealthReport, error) {
	if !s.IsLive() {
		return core.DiskHealthReport{}, fmt.Errorf("collectors: SMART disk health requires the live smartctl binary: System is rooted at %q", s.root)
	}
	if _, err := exec.LookPath(smartctlBinary); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return core.DiskHealthReport{}, core.ErrSmartctlMissing
		}
		return core.DiskHealthReport{}, fmt.Errorf("collectors: look up smartctl: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), smartRoundTimeout)
	defer cancel()

	scanCtx, cancelScan := context.WithTimeout(ctx, smartScanTimeout)
	scanOut, _ := runSmartctl(scanCtx, "-j", "--scan")
	cancelScan()

	scan, err := parseSmartScan(scanOut)
	if err != nil {
		// A scan that does not decode is either an old smartctl or a
		// genuine failure. The version probe separates the two; its
		// own failure is not the story — the scan decode error is.
		versionCtx, cancelVersion := context.WithTimeout(ctx, smartScanTimeout)
		versionOut, _ := runSmartctl(versionCtx, "--version")
		cancelVersion()
		if major, minor, ok := parseSmartVersion(versionOut); ok && !smartctlJSONSupported(major, minor) {
			return core.DiskHealthReport{}, fmt.Errorf("%w: smartctl %d.%d", core.ErrSmartctlUnsupported, major, minor)
		}
		return core.DiskHealthReport{}, fmt.Errorf("collectors: smartctl scan: %w", err)
	}
	if len(scan.Devices) == 0 {
		return core.DiskHealthReport{Disks: []core.DiskHealth{}, ToolVersion: scan.Version}, nil
	}

	// Best-effort model fallback: sysfs names the drive even when
	// smartctl's JSON omits model_name. It is one read of the same
	// sysfs tree the Storage module uses, and a failure simply leaves
	// models empty — the round goes on with whatever smartctl said.
	models := make(map[string]string)
	if disks, err := s.Disks(); err == nil {
		for _, disk := range disks {
			models[disk.Name] = disk.Model
		}
	}

	devices := scan.Devices
	if len(devices) > smartMaxDevices {
		devices = devices[:smartMaxDevices]
	}
	rows := make([]core.DiskHealth, 0, len(devices))
	permissionReads, failedReads := 0, 0
	for _, device := range devices {
		if !validSmartTarget(device.Name, device.Type) {
			continue
		}
		args := []string{"-j", "-a"}
		if device.Type != "" {
			// The scan's type is what makes USB/SAT bridges and
			// megaraid volumes readable; it is re-validated above
			// before it reaches this argv.
			args = append(args, "-d", device.Type)
		}
		args = append(args, device.Name)

		deviceCtx, cancelDevice := context.WithTimeout(ctx, smartDeviceTimeout)
		out, _ := runSmartctl(deviceCtx, args...)
		cancelDevice()

		row, messages, parseErr := parseSmartDevice(out)
		if parseErr != nil {
			// A device that cannot describe itself still appears:
			// the scan named it, so the screen shows it as UNKNOWN
			// instead of dropping a disk that may be the problem.
			rows = append(rows, core.DiskHealth{Device: device.Name, Protocol: device.Protocol})
			failedReads++
			continue
		}
		if row.Device == "" {
			row.Device = device.Name
		}
		if row.Model == "" {
			row.Model = models[strings.TrimPrefix(row.Device, "/dev/")]
		}
		if smartPermissionDenied(messages) {
			permissionReads++
		}
		rows = append(rows, row)
	}

	// Every row without a verdict plus every answered read refused for
	// privilege is the whole-round unprivileged state, not a pile of
	// unsupported disks. The len(rows) equality carries the check: a
	// failed read or a row that answered a verdict breaks it, and the
	// common case — every device denied — reads one permission message
	// per row.
	verdictSeen := false
	for _, row := range rows {
		if row.HasVerdict {
			verdictSeen = true
			break
		}
	}
	if len(rows) > 0 && failedReads == 0 && permissionReads > 0 && permissionReads == len(rows) && !verdictSeen {
		return core.DiskHealthReport{}, fmt.Errorf("%w: all %d scanned devices refused to open", core.ErrSmartUnprivileged, len(rows))
	}

	return core.DiskHealthReport{Disks: rows, ToolVersion: scan.Version}, nil
}

// Compile-time proof that System satisfies the Health read port.
var _ core.DiskHealthLister = System{}
