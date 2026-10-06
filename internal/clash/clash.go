// Package clash is a minimal client for sing-box's Clash-compatible API.
package clash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

const Group = "proxy"

type Client struct {
	Base string // "127.0.0.1:9090"
	http *http.Client
}

func New(addr string) *Client {
	return &Client{Base: "http://" + addr, http: &http.Client{Timeout: 15 * time.Second}}
}

type GroupInfo struct {
	Now string   `json:"now"`
	All []string `json:"all"`
}

func (c *Client) do(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("control API unreachable (is the VPN on?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, e.Message)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Group(name string) (*GroupInfo, error) {
	var g GroupInfo
	return &g, c.do(http.MethodGet, "/proxies/"+url.PathEscape(name), nil, &g)
}

func (c *Client) Select(group, name string) error {
	return c.do(http.MethodPut, "/proxies/"+url.PathEscape(group), map[string]string{"name": name}, nil)
}

// Delay measures latency of one outbound in ms; -1 means it failed.
func (c *Client) Delay(name string) int {
	q := url.Values{"timeout": {"5000"}, "url": {"https://www.gstatic.com/generate_204"}}
	var r struct {
		Delay int `json:"delay"`
	}
	if err := c.do(http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay?"+q.Encode(), nil, &r); err != nil {
		return -1
	}
	return r.Delay
}

type Result struct {
	Name  string
	Delay int
}

// DelayAll tests names concurrently and returns them fastest-first (failed last).
func (c *Client) DelayAll(names []string) []Result {
	res := make([]Result, len(names))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			res[i] = Result{n, c.Delay(n)}
			<-sem
		}()
	}
	wg.Wait()
	sort.SliceStable(res, func(i, j int) bool {
		a, b := res[i].Delay, res[j].Delay
		if (a < 0) != (b < 0) {
			return b < 0
		}
		return a < b
	})
	return res
}
