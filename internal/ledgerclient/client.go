package ledgerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultTimeout bounds a single request. The CLI is interactive/scripted,
// never long-running, so a generous-but-finite timeout beats hanging forever
// against an unreachable server.
const defaultTimeout = 30 * time.Second

// Client talks to a single lineleader server's /api/v1/ledger API.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New returns a Client for the given server base URL and bearer token. token
// may be empty when the server has no auth secret configured.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

// APIError is returned when the server responds with a non-2xx status. It
// carries the server's {"error": "..."} message (or the raw HTTP status line
// when the body isn't the expected JSON shape, e.g. the plain-text 401 the
// auth middleware writes for a bad bearer token).
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.StatusCode == http.StatusUnauthorized {
		return fmt.Sprintf("unauthorized: %s (check your token)", e.Message)
	}
	return fmt.Sprintf("server returned %d: %s", e.StatusCode, e.Message)
}

// do sends an HTTP request and, on success, decodes the JSON response body
// into out (when non-nil). body, when non-nil, is marshaled as the JSON
// request body.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("ledgerclient: encoding request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("ledgerclient: building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to lineleader server at %s: %w (check --server/LINELEADER_SERVER, and that the server is running)", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiErrorFrom(resp)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("ledgerclient: decoding response from %s: %w", path, err)
	}
	return nil
}

// apiErrorFrom builds an *APIError from a non-2xx response, preferring the
// server's {"error": "..."} body but falling back to the HTTP status line
// (e.g. for the plain-text 401 written by internal/web's auth middleware,
// which never got as far as apiHandlers).
func apiErrorFrom(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	msg := body.Error
	if msg == "" {
		msg = strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)+" ")
	}
	return &APIError{StatusCode: resp.StatusCode, Message: msg}
}

// ListEntries fetches every ledger entry, in date order, with running
// balance.
func (c *Client) ListEntries(ctx context.Context) ([]Entry, error) {
	var out []Entry
	if err := c.do(ctx, http.MethodGet, "/api/v1/ledger/entries", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddEntry creates a new entry.
func (c *Client) AddEntry(ctx context.Context, in EntryInput) (Entry, error) {
	var out Entry
	if err := c.do(ctx, http.MethodPost, "/api/v1/ledger/entries", in, &out); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// UpdateEntry overwrites the entry with the given id.
func (c *Client) UpdateEntry(ctx context.Context, id int64, in EntryInput) (Entry, error) {
	var out Entry
	path := fmt.Sprintf("/api/v1/ledger/entries/%d", id)
	if err := c.do(ctx, http.MethodPut, path, in, &out); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// DeleteEntry removes the entry with the given id.
func (c *Client) DeleteEntry(ctx context.Context, id int64) error {
	path := fmt.Sprintf("/api/v1/ledger/entries/%d", id)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ListContracts fetches every contract.
func (c *Client) ListContracts(ctx context.Context) ([]Contract, error) {
	var out []Contract
	if err := c.do(ctx, http.MethodGet, "/api/v1/ledger/contracts", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddContract creates a new contract.
func (c *Client) AddContract(ctx context.Context, in ContractInput) (Contract, error) {
	var out Contract
	if err := c.do(ctx, http.MethodPost, "/api/v1/ledger/contracts", in, &out); err != nil {
		return Contract{}, err
	}
	return out, nil
}

// DeleteContract removes the contract with the given id.
func (c *Client) DeleteContract(ctx context.Context, id int64) error {
	path := fmt.Sprintf("/api/v1/ledger/contracts/%d", id)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// Summaries fetches the per-use-year rollup.
func (c *Client) Summaries(ctx context.Context) ([]Summary, error) {
	var out []Summary
	if err := c.do(ctx, http.MethodGet, "/api/v1/ledger/summaries", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Distribute triggers the next-use-year allocation rollover and returns
// whatever entries it created (empty when already up to date).
func (c *Client) Distribute(ctx context.Context) ([]Entry, error) {
	var out []Entry
	if err := c.do(ctx, http.MethodPost, "/api/v1/ledger/distribute", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
