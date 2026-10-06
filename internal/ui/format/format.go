// Package format renders sizes and durations in compact, human-readable
// form for the TUI panels.
package format

import (
	"fmt"
	"time"
)

// Bytes renders a byte count in IEC binary units (base 1024), matching what
// Linux tools like `free -h` and `df -h` show: "512 B", "1.5 KiB",
// "8.0 GiB". Values at KiB scale and above carry one decimal.
func Bytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGT"[exp])
}

// Duration renders an uptime compactly with its two largest units:
// "45s", "12m 30s", "5h 12m", "3d 4h".
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int64(d.Seconds())
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}
