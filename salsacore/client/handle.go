package client

import (
	"fmt"
	
	"io"
	"time"
	
	"path/filepath"
	
	"encoding/json"
	
	"context"
	"net/url"
	"net/http"
)

// This type describes server connection
type ServerConnection struct {
	// Name of the connection
	Name string
	
	// Base URL for server
	URL string
	
	// TODO: authentication
}

type Handle struct {
	Servers []ServerConnection
	
	// Index of the active server in Servers list
	activeServer int
	
	// If set, key of the Repository object
	repoKey string
	
	// HTTP client used to perform operations
	client *http.Client
	
	contexts map[int]*HandleContext
}

type HandleContext struct {
	handle *Handle
	
	id int
	
	ctx context.Context
	cancel context.CancelFunc
	
	srv *ServerConnection
	serverIndex int
	repoKey string
}

// Creates a new handle object
func NewHandle() *Handle {
	h := new(Handle)
	
	h.Servers = make([]ServerConnection, 0, 1)
	h.contexts = make(map[int]*HandleContext)
	h.client = http.DefaultClient
	h.activeServer = -1
	
	return h
}

// Creates a new context to send requests somewhere
func (h *Handle) NewServerContext(id int, serverIndex int) (*HandleContext, error) {
	if serverIndex < 0 || serverIndex >= len(h.Servers) {
		return nil, fmt.Errorf("No active server was picked")
	}
	
	hctx := new(HandleContext)
	hctx.handle = h
	hctx.id = id
	
	// Create context which support cancellation
	hctx.ctx, hctx.cancel = context.WithCancel(context.Background())
	h.contexts[id] = hctx
	
	hctx.serverIndex = serverIndex
	hctx.srv = &hctx.handle.Servers[serverIndex]
	
	return hctx, nil
}

// Creates new context using active server
func (h *Handle) NewContext(id int) (*HandleContext, error) {
	return h.NewServerContext(id, h.activeServer)
}

// Creates new repository context
func (h *Handle) NewRepositoryContext(id int) (*HandleContext, error) {
	hctx, err := h.NewServerContext(id, h.activeServer)
	if err != nil {
		return nil, err
	}
	
	hctx.repoKey = h.repoKey
	return hctx, nil
}

// See context.WithDeadline. If deadline is set to zero time, the call 
// is ignored
func (hctx *HandleContext) WithDeadline(deadline time.Time) {
	if !deadline.IsZero() {
		hctx.ctx, hctx.cancel = context.WithDeadline(hctx.ctx, deadline)
	}
}

// Deferred operation which should be called to remove context from handle
func (hctx *HandleContext) Done() {
	delete(hctx.handle.contexts, hctx.id)
}

// Cancels request identified by identifier id
func (h *Handle) Cancel(id int) error {
	if hctx, ok := h.contexts[id]; ok {
		hctx.cancel()
	}
	return fmt.Errorf("No such context %d", id)
}

func (hctx *HandleContext) newRequest(method string, body io.Reader, path ...string) (*http.Request, error) {
	url, err := url.Parse(hctx.srv.URL)
	if err != nil {
		return nil, err
	}
	
	// Build up URL
	if len(hctx.repoKey) > 0 {
		path = append([]string{"repo", hctx.repoKey}, path...)
	}
	url.Path = filepath.Join(append([]string{url.Path}, path...)...)
	
	rq, err := http.NewRequest(method, url.String(), body)
	if err != nil {
		return nil, err
	}
	
	return rq, nil
}

func (hctx *HandleContext) newGETRequest(path ...string) (*http.Request, error) {
	return hctx.newRequest("GET", nil, path...)
}

func (hctx *HandleContext) doRequest(rq *http.Request) (*http.Response, error) {
	resp, err := hctx.handle.client.Do(rq.WithContext(hctx.ctx))
	
	if err != nil {
		select {
		case <-hctx.ctx.Done():
			err = hctx.ctx.Err()
		default:
		}
	}
	return resp, err
}

// Performs request and decodes json from response after handling it
func (hctx *HandleContext) doRequestDecodeJSON(rq *http.Request, value interface{}) error {
	resp, err := hctx.doRequest(rq)
	
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil {
		return fmt.Errorf("No response received")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Error in %s %s: %s", resp.Request.Method, 
					resp.Request.URL.Path, resp.Status)
	}
	
	return json.NewDecoder(resp.Body).Decode(value)
}

func (hctx *HandleContext) doGETRequestDecodeJSON(value interface{}, path ...string) error {
	rq, err := hctx.newGETRequest(path...)
	if err != nil {
		return err
	}
	
	return hctx.doRequestDecodeJSON(rq, value)
}
