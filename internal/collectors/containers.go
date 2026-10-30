// containers.go holds the pure half of the Containers module
// (ARCHITECTURE.md §3, §4): the wire decode that turns the
// Docker-compatible Engine API's GET /containers/json array into
// core.Container rows, and the socket-candidate resolution that
// decides which unix socket a collector should dial. Both are pure —
// bytes or environment values in, domain data or paths out — so the
// wire grammar and the candidate order are table-tested without a
// daemon, a socket or a live host (§10). Nothing here performs I/O;
// the unix-socket client and System.Containers live in
// containers_client.go.

package collectors

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// The daemon endpoint overrides, in resolution order. CONTAINER_HOST
// is Podman's variable; DOCKER_HOST is the Docker ecosystem's. Both
// are authoritative when set — see containerSocketCandidates — and
// only unix sockets are supported, so a tcp:// value is an explicit
// error rather than a silent fallback (remote daemons are out of
// scope for the module).
const (
	containerHostEnv = "CONTAINER_HOST"
	dockerHostEnv    = "DOCKER_HOST"
)

// xdgRuntimeDirEnv is the per-user runtime directory the rootless
// socket candidates hang off. Only the first candidate pair uses it;
// the system-wide paths below are fixed.
const xdgRuntimeDirEnv = "XDG_RUNTIME_DIR"

// containerWire mirrors the fields of one Docker Engine
// GET /containers/json array element that serverctl consumes. The
// daemon returns far more — Status, Ports, Labels, HostConfig and
// friends — and every field outside this struct is deliberately
// ignored: the screen needs identity, name, image, state and creation
// time, and decoding less keeps the mapping honest about what it
// depends on.
type containerWire struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	Created int64    `json:"Created"`
	State   string   `json:"State"`
}

// decodeContainerList maps a Docker-compatible GET /containers/json
// response body into core.Container rows, in daemon order. It is pure:
// the caller owns the bounded read and passes the bytes in, so the
// decode is table-testable against fixtures without a socket.
//
// Mapping rules: Name is the first entry of Names with the daemon's
// leading "/" stripped (Docker reports names that way), falling back
// to the 12-character short ID when the daemon reports no name at
// all; Created is Unix seconds in local time, the convention the
// other collectors follow. An empty array is a legitimate empty
// listing — a runtime with no containers — and yields an empty,
// non-nil slice; malformed JSON is an error the screen degrades on
// (§5). State passes through untouched (core.Container documents the
// vocabulary).
func decodeContainerList(data []byte) ([]core.Container, error) {
	var wire []containerWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("decode container list: %w", err)
	}
	containers := make([]core.Container, 0, len(wire))
	for _, w := range wire {
		containers = append(containers, core.Container{
			ID:      w.ID,
			Name:    containerName(w),
			Image:   w.Image,
			State:   w.State,
			Created: time.Unix(w.Created, 0),
		})
	}
	return containers, nil
}

// containerName picks the row's display name from the daemon's Names
// array: the first name is the primary one, and the leading "/" is
// Docker's naming convention, not part of the name. A container with
// no names (a daemon that omits them, or a malformed row) falls back
// to the short ID so every row still carries a stable handle instead
// of an empty NAME cell.
func containerName(w containerWire) string {
	if len(w.Names) == 0 {
		return shortContainerID(w.ID)
	}
	return strings.TrimPrefix(w.Names[0], "/")
}

// shortContainerID truncates a full daemon ID to the 12-character
// form Docker and Podman print — the same length the CLI shows in
// `docker ps` and the length the Containers screen uses when a row
// has no name. Shorter IDs are returned unchanged: nothing is ever
// sliced past its length, so a malformed or absent ID reads back
// as-is rather than panicking the round.
func shortContainerID(id string) string {
	const shortLen = 12
	if len(id) <= shortLen {
		return id
	}
	return id[:shortLen]
}

// containerSocketCandidates resolves the unix sockets a collector
// should try, first successful wins (ARCHITECTURE.md §8). The order is
// deliberate: an explicit endpoint override is authoritative, then
// the caller's own rootless runtime, then the system-wide sockets, so
// a host running both a user Podman and a system Docker reads the
// least surprising source first.
//
// CONTAINER_HOST wins over DOCKER_HOST, and either one ends the list:
// an explicit endpoint that fails to parse is an error, never a
// silent fallback to guessed paths, because the user named the
// daemon they wanted. With no override, the candidates are:
//
//	$XDG_RUNTIME_DIR/podman/podman.sock
//	$XDG_RUNTIME_DIR/docker.sock
//	/run/user/<uid>/podman/podman.sock
//	/run/user/<uid>/docker.sock
//	/run/podman/podman.sock
//	/var/run/docker.sock
//
// The XDG pair only exists when XDG_RUNTIME_DIR is non-empty — an
// empty value would otherwise produce a bare relative path — and
// duplicates are dropped preserving first occurrence, since
// XDG_RUNTIME_DIR commonly IS /run/user/<uid> (systemd sets it that
// way), which would otherwise repeat both rootless paths. getenv and
// uid are injected, keeping the resolution hermetic for tests.
func containerSocketCandidates(getenv func(string) string, uid int) ([]string, error) {
	if raw := getenv(containerHostEnv); raw != "" {
		path, err := containerSocketPath(raw)
		if err != nil {
			return nil, err
		}
		return []string{path}, nil
	}
	if raw := getenv(dockerHostEnv); raw != "" {
		path, err := containerSocketPath(raw)
		if err != nil {
			return nil, err
		}
		return []string{path}, nil
	}

	var candidates []string
	seen := make(map[string]struct{}, 6)
	add := func(path string) {
		if path == "" {
			return
		}
		if _, dup := seen[path]; dup {
			return
		}
		seen[path] = struct{}{}
		candidates = append(candidates, path)
	}

	if runtimeDir := getenv(xdgRuntimeDirEnv); runtimeDir != "" {
		add(filepath.Join(runtimeDir, "podman", "podman.sock"))
		add(filepath.Join(runtimeDir, "docker.sock"))
	}
	userDir := fmt.Sprintf("/run/user/%d", uid)
	add(filepath.Join(userDir, "podman", "podman.sock"))
	add(filepath.Join(userDir, "docker.sock"))
	add("/run/podman/podman.sock")
	add("/var/run/docker.sock")
	return candidates, nil
}

// containerSocketPath normalizes one endpoint value into the socket
// path to dial. Docker and Podman spell the local endpoint
// unix:///path/to.sock, and serverctl accepts a bare absolute path
// too, because that is how the same socket is usually written in
// docs and environment files. Any other scheme — tcp://, ssh://,
// npipe:// — is rejected with an explicit error: remote and
// non-unix daemons are out of scope, and silently falling back to
// the next candidate would hide the misconfiguration.
//
// The path is only trimmed of its scheme; it is not cleaned, made
// absolute or stat'ed. Resolution belongs to the dialer, and a
// nonexistent path must fail there with its own reason, not here
// where the collector cannot yet distinguish "typo" from "socket
// not started".
func containerSocketPath(raw string) (string, error) {
	path := raw
	if scheme, rest, found := strings.Cut(raw, "://"); found {
		if scheme != "unix" {
			return "", fmt.Errorf("unsupported container endpoint scheme %q (unix sockets only)", scheme)
		}
		path = rest
	}
	if path == "" {
		return "", fmt.Errorf("container endpoint %q names an empty unix socket path", raw)
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("container endpoint %q is not an absolute unix socket path", raw)
	}
	return path, nil
}

// containerRuntime labels a listing with the runtime its socket
// belongs to: "podman" when the path mentions podman, "docker"
// otherwise. The label is provenance for the screen — the status line
// names the daemon — and never changes the decode: both runtimes
// answer the same compat API. A case-insensitive substring match is
// enough because the candidate paths (and the usual overrides) name
// the runtime explicitly, and an unknown custom path safely reads as
// docker, the API's namesake.
func containerRuntime(socketPath string) string {
	if strings.Contains(strings.ToLower(socketPath), "podman") {
		return "podman"
	}
	return "docker"
}
