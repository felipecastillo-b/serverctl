// containers_client.go holds the live half of the Containers module
// (ARCHITECTURE.md §3, §8): the one-shot unix-socket HTTP client that
// speaks the Docker-compatible Engine API, the fallback walk over the
// candidate sockets, and System.Containers itself. The pure half —
// wire decode and candidate resolution — lives in containers.go.
//
// Nothing here has a root prefix: a unix socket and a daemon are host
// state no fixture root can fake, so System.Containers refuses on any
// non-live System before resolving or dialing anything, the same live
// guard logind, the journal and the rpm query keep. The client is
// read-only by construction — one GET per candidate, no lifecycle, no
// second call — and every attempt shares one 4 s context, so a wedged
// socket costs one round, not the refresh chain.
package collectors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/core"
)

const (
	// containerListPath is the unversioned Engine API listing path:
	// the documented Docker form, which Podman's compat API also
	// implements. all=1 includes stopped containers — the screen's
	// job is the whole fleet, not only what is running. Deliberately
	// no /v1.x prefix: the daemon negotiates nothing here, and the
	// unversioned form is stable across both runtimes.
	containerListPath = "/containers/json?all=1"

	// containerListMaxBytes bounds one listing response, the read
	// half of the module's defense against a misbehaving daemon. A
	// host with thousands of containers still fits well inside
	// 4 MiB; the read is capped one byte over so the client can tell
	// "exactly at the cap" from "over it" without decoding a
	// truncated body.
	containerListMaxBytes = 4 << 20

	// containerTimeout bounds the whole collection: every candidate
	// dial, the successful listing's read, and its decode share it.
	// Four seconds is generous for a local daemon round trip and
	// short enough that a wedged socket cannot stall the containers
	// refresh chain for more than one round.
	containerTimeout = 4 * time.Second
)

// containerClient speaks the Docker-compatible Engine API over one
// local unix socket. It is a per-attempt value, not a shared or
// global client: one refresh owns one client per candidate socket,
// dials through the transport's unix DialContext, and keeps no
// connection afterwards (DisableKeepAlives), because there is no
// lifecycle on System to hang a pool or a close hook on — the same
// per-call dial trade Units() and logindSessions() make.
type containerClient struct {
	socketPath string
	http       *http.Client
}

// newContainerClient builds a client dialing socketPath. The URL's
// host is irrelevant — the dialer owns the socket, see list — so the
// transport ignores the address it is handed and always dials the one
// named path with the standard library's net.Dialer, which honors the
// attempt's context. No proxy: the transport's Proxy field stays nil,
// so a host-wide HTTP proxy can never intercept a local socket read.
func newContainerClient(socketPath string) *containerClient {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		DisableKeepAlives: true,
	}
	return &containerClient{
		socketPath: socketPath,
		http:       &http.Client{Transport: transport},
	}
}

// list performs the module's single daemon read: GET
// /containers/json?all=1 on the client's socket, bounded and decoded
// into core.Container rows. The request URL carries a dummy host —
// "http://container" — because net/http requires one and the
// transport's DialContext never resolves it; the socket path is the
// only address that matters.
//
// The response is read through an io.LimitReader one byte over the
// cap: a body that reaches the extra byte is refused whole rather than
// decoded truncated, since a partial array could silently drop
// containers. A non-200 answer is an error naming the socket and the
// daemon's status, never a decode of the error body. The body is
// always closed — one-shot read, so a close error cannot lose data.
func (c *containerClient) list(ctx context.Context) ([]core.Container, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://container"+containerListPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build container list request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request container list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("container list at %s: daemon answered %s", c.socketPath, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, containerListMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read container list from %s: %w", c.socketPath, err)
	}
	if len(body) > containerListMaxBytes {
		return nil, fmt.Errorf("container list at %s: response exceeds %d bytes", c.socketPath, containerListMaxBytes)
	}
	return decodeContainerList(body)
}

// listContainersFrom walks the candidate sockets in resolution order
// (ARCHITECTURE.md §8) and returns the first successful listing,
// labeled with the runtime that socket belongs to. A successful EMPTY
// listing ends the walk: the daemon answered, and "no containers" is
// an answer, not a reason to ask the next runtime — falling through
// there would replace an honest empty fleet with a stranger's.
//
// Every failure is collected with its path — a missing socket, a
// permission error, a daemon error, malformed JSON — so the aggregated
// error names each candidate and its reason once every socket has been
// tried. The walk stops early on a cancelled or expired context: the
// remaining candidates would fail for the same reason, and their dials
// would only widen the overrun.
func listContainersFrom(ctx context.Context, sockets []string) (core.ContainerList, error) {
	var errs []error
	for _, socketPath := range sockets {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		rows, err := newContainerClient(socketPath).list(ctx)
		if err == nil {
			return core.ContainerList{
				Runtime:    containerRuntime(socketPath),
				Containers: rows,
			}, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", socketPath, err))
	}
	// An empty candidate list leaves errs empty: errors.Join() is
	// nil then, and %w would render the garbled "%!w(<nil>)" instead
	// of a reason. Say why no attempt happened.
	if len(errs) == 0 {
		return core.ContainerList{}, fmt.Errorf("collectors: no container daemon reached: no candidate socket to try")
	}
	return core.ContainerList{}, fmt.Errorf("collectors: no container daemon reached: %w", errors.Join(errs...))
}

// Containers implements core.ContainerLister: one listing of the local
// Docker-compatible daemon's containers, read over a unix socket
// (ARCHITECTURE.md §3, §8). The live guard comes first because the
// socket and the daemon are host state a fixture root cannot redirect
// — exec and D-Bus have no root prefix either — so a fixture System
// refuses rather than silently reading the runner's containers, the
// way the rpm query and logind reads do.
//
// Socket resolution keeps its own error (a malformed CONTAINER_HOST or
// DOCKER_HOST is a user error with a clear message, never a silent
// fallback), and the 4 s context bounds every candidate attempt. A
// successful empty listing returns an empty, non-nil Containers slice
// and no error; an unreachable daemon returns the aggregated per-path
// error the screen degrades on (§5).
func (s System) Containers() (core.ContainerList, error) {
	if !s.IsLive() {
		return core.ContainerList{}, fmt.Errorf("collectors: containers require the live container socket: System is rooted at %q", s.root)
	}
	sockets, err := containerSocketCandidates(os.Getenv, os.Getuid())
	if err != nil {
		return core.ContainerList{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), containerTimeout)
	defer cancel()
	return listContainersFrom(ctx, sockets)
}

// Compile-time proof that System satisfies the Containers read port.
var _ core.ContainerLister = System{}
