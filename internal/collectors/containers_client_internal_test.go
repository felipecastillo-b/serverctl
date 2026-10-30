package collectors

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

// The daemon fixtures the socket tests serve: one running and one
// exited container in the shape the Engine API reports, with the
// leading-slash name and the fields serverctl ignores (Status, Ports)
// kept in, so the live path exercises the same decode the wire tests
// cover rather than a pre-trimmed body.
const (
	containerTestRunningID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	containerTestExitedID  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	containerTestListing = `[` +
		`{"Id":"` + containerTestRunningID + `","Names":["/web"],"Image":"nginx:latest","Created":1700000000,"State":"running","Status":"Up 2 hours","Ports":[]},` +
		`{"Id":"` + containerTestExitedID + `","Names":["/db"],"Image":"postgres:16","Created":1700000100,"State":"exited"}` +
		`]`
)

// containerTestUnixPathMax is Linux's sun_path capacity — 108 bytes
// including the terminating NUL — so 107 bytes is the longest path
// that can be bound.
const containerTestUnixPathMax = 107

// containerSocketDir returns a fresh directory for one test's unix
// sockets. t.TempDir() embeds the test name and can push a socket path
// past sun_path's limit, so this uses os.MkdirTemp("", ...) — a short
// /tmp path — and owns the directory's cleanup.
func containerSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "srvctl-cont-")
	if err != nil {
		t.Fatalf("mkdir temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// containerTestSocket returns dir/name, skipping the test if the
// resulting path could not be bound: a unix socket path is capped by
// sun_path, and a long TMPDIR is the environment's problem, not the
// collector's.
func containerTestSocket(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if len(path) > containerTestUnixPathMax {
		t.Skipf("unix socket path %q exceeds the %d-byte platform limit", path, containerTestUnixPathMax)
	}
	return path
}

// serveContainerSocket binds an HTTP server to the unix socket at
// socketPath and answers with handler until the test ends. The
// cleanup closes the server, waits for Serve to return and lets the
// caller-removed directory go — no listener or goroutine outlives its
// test.
func serveContainerSocket(t *testing.T, socketPath string, handler http.Handler) {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket %s: %v", socketPath, err)
	}
	server := &http.Server{Handler: handler}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		<-served
	})
}

// containerListHandler answers with body and asserts the request shape
// the daemon contract requires: GET, /containers/json and all=1. A
// client that drifts from the documented endpoint fails here, not only
// against a real daemon. Handler code runs off the test goroutine, so
// failures are reported with t.Errorf — t.Fatalf would stop the wrong
// one.
func containerListHandler(t *testing.T, body string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("container list method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/containers/json" {
			t.Errorf("container list path = %q, want /containers/json", r.URL.Path)
		}
		if got := r.URL.Query().Get("all"); got != "1" {
			t.Errorf("container list all query = %q, want 1", got)
		}
		_, _ = io.WriteString(w, body)
	})
}

// TestListContainersFromFallsThroughToWorkingSocket pins the fallback
// walk: a missing candidate is a recorded failure, not the end of the
// round, and the first socket that answers owns the listing — rows,
// order and the runtime label all come from that socket.
func TestListContainersFromFallsThroughToWorkingSocket(t *testing.T) {
	dir := containerSocketDir(t)
	missing := containerTestSocket(t, dir, "missing.sock")
	working := containerTestSocket(t, dir, "podman.sock")
	serveContainerSocket(t, working, containerListHandler(t, containerTestListing))

	got, err := listContainersFrom(context.Background(), []string{missing, working})
	if err != nil {
		t.Fatalf("listContainersFrom: %v", err)
	}
	if got.Runtime != "podman" {
		t.Errorf("listContainersFrom Runtime = %q, want podman", got.Runtime)
	}
	want := []core.Container{
		{
			ID:      containerTestRunningID,
			Name:    "web",
			Image:   "nginx:latest",
			State:   "running",
			Created: time.Unix(1700000000, 0),
		},
		{
			ID:      containerTestExitedID,
			Name:    "db",
			Image:   "postgres:16",
			State:   "exited",
			Created: time.Unix(1700000100, 0),
		},
	}
	if !reflect.DeepEqual(got.Containers, want) {
		t.Errorf("listContainersFrom Containers = %#v, want %#v", got.Containers, want)
	}
}

// TestListContainersFromAcceptsEmptyListing pins the empty-is-an-answer
// rule: a daemon error falls through to the next candidate, and that
// candidate's empty array ends the walk as a successful, non-nil empty
// listing labeled with its own runtime — never a reason to ask a
// farther socket.
func TestListContainersFromAcceptsEmptyListing(t *testing.T) {
	dir := containerSocketDir(t)
	failing := containerTestSocket(t, dir, "first.sock")
	empty := containerTestSocket(t, dir, "podman.sock")
	serveContainerSocket(t, failing, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "daemon failure", http.StatusInternalServerError)
	}))
	serveContainerSocket(t, empty, containerListHandler(t, "[]"))

	got, err := listContainersFrom(context.Background(), []string{failing, empty})
	if err != nil {
		t.Fatalf("listContainersFrom: %v", err)
	}
	if got.Runtime != "podman" {
		t.Errorf("listContainersFrom Runtime = %q, want podman", got.Runtime)
	}
	if got.Containers == nil {
		t.Fatal("listContainersFrom Containers = nil, want an empty non-nil slice")
	}
	if len(got.Containers) != 0 {
		t.Errorf("listContainersFrom Containers = %d rows, want 0", len(got.Containers))
	}
}

// TestListContainersFromSkipsMalformedResponse pins decode failures as
// candidate failures: a daemon answering garbage does not end the
// round, and the next socket's well-formed listing still wins.
func TestListContainersFromSkipsMalformedResponse(t *testing.T) {
	dir := containerSocketDir(t)
	broken := containerTestSocket(t, dir, "broken.sock")
	working := containerTestSocket(t, dir, "podman.sock")
	serveContainerSocket(t, broken, containerListHandler(t, `{"Id":`))
	serveContainerSocket(t, working, containerListHandler(t, containerTestListing))

	got, err := listContainersFrom(context.Background(), []string{broken, working})
	if err != nil {
		t.Fatalf("listContainersFrom: %v", err)
	}
	if got.Runtime != "podman" {
		t.Errorf("listContainersFrom Runtime = %q, want podman", got.Runtime)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("listContainersFrom Containers = %d rows, want 2", len(got.Containers))
	}
	if got.Containers[0].Name != "web" || got.Containers[1].Name != "db" {
		t.Errorf("listContainersFrom names = %q, %q, want web, db", got.Containers[0].Name, got.Containers[1].Name)
	}
}

// TestListContainersFromErrorsNamingEveryCandidate pins the aggregated
// error: when no socket answers, the message names every path tried,
// so a user can see which runtimes were looked for and in what order.
func TestListContainersFromErrorsNamingEveryCandidate(t *testing.T) {
	dir := containerSocketDir(t)
	first := containerTestSocket(t, dir, "one.sock")
	second := containerTestSocket(t, dir, "two.sock")

	_, err := listContainersFrom(context.Background(), []string{first, second})
	if err == nil {
		t.Fatal("listContainersFrom = nil error, want the aggregate failure")
	}
	if !strings.Contains(err.Error(), "no container daemon reached") {
		t.Errorf("listContainersFrom error = %v, want the no-container-daemon prefix", err)
	}
	for _, path := range []string{first, second} {
		if !strings.Contains(err.Error(), path) {
			t.Errorf("listContainersFrom error = %v, want it to name %s", err, path)
		}
	}
}

// TestListContainersFromEmptyCandidates pins the empty-candidate case:
// no attempt is possible, so the walk errors instead of panicking on a
// nil wrap or reporting a bogus success.
func TestListContainersFromEmptyCandidates(t *testing.T) {
	_, err := listContainersFrom(context.Background(), nil)
	if err == nil {
		t.Fatal("listContainersFrom(nil) = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), "no container daemon reached") {
		t.Errorf("listContainersFrom(nil) error = %v, want the no-container-daemon prefix", err)
	}
}

// TestContainerClientListNon200 pins the status gate: a daemon error
// is reported with the socket path and the daemon's status, never
// decoded as a listing.
func TestContainerClientListNon200(t *testing.T) {
	dir := containerSocketDir(t)
	socket := containerTestSocket(t, dir, "docker.sock")
	serveContainerSocket(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "daemon failure", http.StatusInternalServerError)
	}))

	rows, err := newContainerClient(socket).list(context.Background())
	if err == nil {
		t.Fatalf("list = %d rows, nil error, want a status error", len(rows))
	}
	if !strings.Contains(err.Error(), socket) {
		t.Errorf("list error = %v, want it to name %s", err, socket)
	}
	status := http.StatusText(http.StatusInternalServerError)
	if !strings.Contains(err.Error(), status) {
		t.Errorf("list error = %v, want it to carry the %q status", err, status)
	}
}

// zeroReader is an endless stream of zero bytes: the oversized-response
// test writes past the cap without allocating a body first.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// TestContainerClientListRejectsOversizedResponse pins the read bound:
// a body one byte over the cap is refused whole with an error naming
// the cap, so a runaway daemon cannot stream unbounded data into the
// round or get a truncated body decoded as if it were complete.
func TestContainerClientListRejectsOversizedResponse(t *testing.T) {
	dir := containerSocketDir(t)
	socket := containerTestSocket(t, dir, "docker.sock")
	serveContainerSocket(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyN(w, zeroReader{}, containerListMaxBytes+1)
	}))

	_, err := newContainerClient(socket).list(context.Background())
	if err == nil {
		t.Fatal("list = nil error, want the oversize error")
	}
	if !strings.Contains(err.Error(), "response exceeds") {
		t.Errorf("list error = %v, want the response exceeds message", err)
	}
	if !strings.Contains(err.Error(), "4194304") {
		t.Errorf("list error = %v, want it to name the %d-byte cap", err, containerListMaxBytes)
	}
}

// TestSystemContainersRequiresLiveSystem pins the live guard: sockets
// and daemons are host state, so a fixture root must refuse before any
// resolution or dial — the same trade logind, the journal and the rpm
// query make.
func TestSystemContainersRequiresLiveSystem(t *testing.T) {
	_, err := New("testdata").Containers()
	if err == nil {
		t.Fatal("Containers() on a fixture root = nil error, want the live guard")
	}
	if !strings.Contains(err.Error(), "containers require the live container socket") {
		t.Errorf("Containers() error = %v, want the live container socket message", err)
	}
}

// TestSystemContainersReadsDockerHostSocket exercises the whole live
// path end to end: a live System resolves DOCKER_HOST (with
// CONTAINER_HOST cleared so the override order is real), dials the
// socket, decodes the listing and labels it docker. The socket is a
// real unix listener answered by an http.Server, so the transport,
// dialer and decode are all under test — nothing is faked at the
// client boundary.
func TestSystemContainersReadsDockerHostSocket(t *testing.T) {
	dir := containerSocketDir(t)
	socket := containerTestSocket(t, dir, "docker.sock")
	serveContainerSocket(t, socket, containerListHandler(t, containerTestListing))

	t.Setenv(containerHostEnv, "")
	t.Setenv(dockerHostEnv, "unix://"+socket)

	got, err := New("/").Containers()
	if err != nil {
		t.Fatalf("Containers() over DOCKER_HOST: %v", err)
	}
	if got.Runtime != "docker" {
		t.Errorf("Containers() Runtime = %q, want docker", got.Runtime)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("Containers() = %d rows, want 2", len(got.Containers))
	}
	if got.Containers[0].Name != "web" || got.Containers[0].State != "running" {
		t.Errorf("Containers()[0] = %#v, want the running web row", got.Containers[0])
	}
	if got.Containers[1].Name != "db" || got.Containers[1].State != "exited" {
		t.Errorf("Containers()[1] = %#v, want the exited db row", got.Containers[1])
	}
}
