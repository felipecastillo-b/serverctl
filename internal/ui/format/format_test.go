package format_test

import (
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/ui/format"
)

func TestBytes(t *testing.T) {
	tests := []struct {
		name  string
		input uint64
		want  string
	}{
		{"zero", 0, "0 B"},
		{"bytes stay raw", 512, "512 B"},
		{"exact kib", 1024, "1.0 KiB"},
		{"kib fraction", 1536, "1.5 KiB"},
		{"mib", 1572864, "1.5 MiB"},
		{"gib", 8388608000, "7.8 GiB"},
		{"tib", 1099511627776, "1.0 TiB"},
		{"huge stays tib", 5 << 40, "5.0 TiB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := format.Bytes(tt.input); got != tt.want {
				t.Errorf("Bytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		name  string
		input time.Duration
		want  string
	}{
		{"zero", 0, "0s"},
		{"negative clamps to zero", -time.Minute, "0s"},
		{"seconds", 45 * time.Second, "45s"},
		{"minutes and seconds", 12*time.Minute + 30*time.Second, "12m 30s"},
		{"hours and minutes", 5*time.Hour + 12*time.Minute, "5h 12m"},
		{"days and hours", 3*24*time.Hour + 4*time.Hour, "3d 4h"},
		{"drops third unit", 3*24*time.Hour + 4*time.Hour + 59*time.Minute, "3d 4h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := format.Duration(tt.input); got != tt.want {
				t.Errorf("Duration(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
