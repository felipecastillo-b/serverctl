package collectors

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// TestDecodeContainerList pins the GET /containers/json mapping: the
// daemon's Id, first Name with its leading "/" stripped, Image, State
// and Created seconds land in core.Container rows in daemon order,
// and a row with no name falls back to the short ID. The empty-array
// row is the legitimate empty listing, while malformed and empty
// bodies are errors the screen degrades on (§5).
func TestDecodeContainerList(t *testing.T) {
	const (
		runningID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		exitedID  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
		// twoContain carries fields serverctl ignores (Status,
		// Ports, Labels) to prove the wire struct decodes what it
		// needs and nothing else.
		twoContain = `[
			{"Id":"` + runningID + `","Names":["/web"],"Image":"nginx:latest","Created":1700000000,"State":"running","Status":"Up 2 hours","Ports":[],"Labels":{"app":"web"}},
			{"Id":"` + exitedID + `","Names":[],"Image":"postgres:16","Created":1700000100,"State":"exited"}
		]`
	)

	t.Run("two rows map in daemon order", func(t *testing.T) {
		got, err := decodeContainerList([]byte(twoContain))
		if err != nil {
			t.Fatalf("decodeContainerList: %v", err)
		}
		want := []core.Container{
			{
				ID:      runningID,
				Name:    "web",
				Image:   "nginx:latest",
				State:   "running",
				Created: time.Unix(1700000000, 0),
			},
			{
				ID:      exitedID,
				Name:    "fedcba987654",
				Image:   "postgres:16",
				State:   "exited",
				Created: time.Unix(1700000100, 0),
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("decodeContainerList = %#v, want %#v", got, want)
		}
	})

	t.Run("empty array is an empty listing", func(t *testing.T) {
		got, err := decodeContainerList([]byte("[]"))
		if err != nil {
			t.Fatalf("decodeContainerList([]): %v", err)
		}
		if got == nil {
			t.Fatal("decodeContainerList([]) = nil, want an empty non-nil slice")
		}
		if len(got) != 0 {
			t.Errorf("decodeContainerList([]) = %d rows, want 0", len(got))
		}
	})

	t.Run("malformed json errors", func(t *testing.T) {
		if _, err := decodeContainerList([]byte(`{"Id":`)); err == nil {
			t.Fatal("decodeContainerList(malformed) = nil error, want a decode error")
		} else if !strings.Contains(err.Error(), "decode container list") {
			t.Errorf("decodeContainerList(malformed) error = %v, want the decode container list prefix", err)
		}
	})

	t.Run("empty input errors", func(t *testing.T) {
		if _, err := decodeContainerList(nil); err == nil {
			t.Fatal("decodeContainerList(nil) = nil error, want a decode error")
		} else if !strings.Contains(err.Error(), "decode container list") {
			t.Errorf("decodeContainerList(nil) error = %v, want the decode container list prefix", err)
		}
	})
}

// TestShortContainerID pins the 12-character row handle: full daemon
// IDs are truncated to the length the docker and podman CLIs print,
// and anything already short — or empty — passes through untouched so
// the fallback name can never panic.
func TestShortContainerID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "full id truncates to twelve",
			id:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want: "0123456789ab",
		},
		{"eleven characters stay whole", "0123456789a", "0123456789a"},
		{"twelve characters stay whole", "0123456789ab", "0123456789ab"},
		{"empty id stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortContainerID(tt.id); got != tt.want {
				t.Errorf("shortContainerID(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

// fakeGetenv builds the injected environment a candidate-resolution
// test reads: only the keys in values are set, everything else reads
// as the empty string os.Getenv would return for an unset variable.
func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// TestContainerSocketCandidates pins the documented resolution order
// (ARCHITECTURE.md §8): an explicit endpoint override is
// authoritative and parse-checked, and with no override the rootless
// sockets come before the system-wide ones, deduplicated with the
// first occurrence kept. The XDG-empty rows prove a missing runtime
// directory never injects an empty candidate path.
func TestContainerSocketCandidates(t *testing.T) {
	const uid = 1000

	tests := []struct {
		name    string
		env     map[string]string
		want    []string
		wantErr bool
	}{
		{
			name: "CONTAINER_HOST wins over DOCKER_HOST",
			env: map[string]string{
				containerHostEnv: "unix:///run/user/1000/podman/podman.sock",
				dockerHostEnv:    "unix:///var/run/docker.sock",
			},
			want: []string{"/run/user/1000/podman/podman.sock"},
		},
		{
			name: "CONTAINER_HOST unix path parses",
			env:  map[string]string{containerHostEnv: "unix:///run/user/1000/podman/podman.sock"},
			want: []string{"/run/user/1000/podman/podman.sock"},
		},
		{
			name: "DOCKER_HOST bare path parses",
			env:  map[string]string{dockerHostEnv: "/var/run/docker.sock"},
			want: []string{"/var/run/docker.sock"},
		},
		{
			name:    "CONTAINER_HOST tcp endpoint is an error",
			env:     map[string]string{containerHostEnv: "tcp://127.0.0.1:2375"},
			wantErr: true,
		},
		{
			name:    "DOCKER_HOST tcp endpoint is an error",
			env:     map[string]string{dockerHostEnv: "tcp://127.0.0.1:2375"},
			wantErr: true,
		},
		{
			name: "no override with XDG lists all candidates in order",
			env:  map[string]string{xdgRuntimeDirEnv: "/tmp/runtime-1000"},
			want: []string{
				"/tmp/runtime-1000/podman/podman.sock",
				"/tmp/runtime-1000/docker.sock",
				"/run/user/1000/podman/podman.sock",
				"/run/user/1000/docker.sock",
				"/run/podman/podman.sock",
				"/var/run/docker.sock",
			},
		},
		{
			name: "XDG equal to the user runtime dir dedupes",
			env:  map[string]string{xdgRuntimeDirEnv: "/run/user/1000"},
			want: []string{
				"/run/user/1000/podman/podman.sock",
				"/run/user/1000/docker.sock",
				"/run/podman/podman.sock",
				"/var/run/docker.sock",
			},
		},
		{
			name: "empty XDG injects no empty paths",
			env:  nil,
			want: []string{
				"/run/user/1000/podman/podman.sock",
				"/run/user/1000/docker.sock",
				"/run/podman/podman.sock",
				"/var/run/docker.sock",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := containerSocketCandidates(fakeGetenv(tt.env), uid)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("containerSocketCandidates = %v, want an error", got)
				}
				if !strings.Contains(err.Error(), "unsupported container endpoint scheme") {
					t.Errorf("containerSocketCandidates error = %v, want the unsupported-scheme message", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("containerSocketCandidates: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("containerSocketCandidates = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestContainerSocketPath pins the endpoint grammar: unix:///path and
// a bare absolute path are accepted, the scheme is the only thing
// stripped, and every other scheme, empty path or relative path is an
// explicit error instead of a guessed fallback.
func TestContainerSocketPath(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"unix scheme path", "unix:///var/run/docker.sock", "/var/run/docker.sock", false},
		{"bare absolute path", "/var/run/docker.sock", "/var/run/docker.sock", false},
		{"empty unix path", "unix://", "", true},
		{"empty bare value", "", "", true},
		{"tcp scheme rejected", "tcp://127.0.0.1:2375", "", true},
		{"ssh scheme rejected", "ssh://user@host/run/docker.sock", "", true},
		{"relative bare path rejected", "run/docker.sock", "", true},
		{"relative unix path rejected", "unix://run/docker.sock", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := containerSocketPath(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("containerSocketPath(%q) = %q, want an error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("containerSocketPath(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("containerSocketPath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestContainerRuntime pins the provenance label: a path mentioning
// podman reads as podman, everything else — including Docker's
// sockets and an unrecognized custom path — reads as docker, the
// compat API's namesake.
func TestContainerRuntime(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"rootless podman socket", "/run/user/1000/podman/podman.sock", "podman"},
		{"system podman socket", "/run/podman/podman.sock", "podman"},
		{"case-insensitive podman", "/run/Podman/podman.sock", "podman"},
		{"docker socket", "/var/run/docker.sock", "docker"},
		{"rootless docker socket", "/run/user/1000/docker.sock", "docker"},
		{"unknown path defaults to docker", "/tmp/custom.sock", "docker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerRuntime(tt.path); got != tt.want {
				t.Errorf("containerRuntime(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
