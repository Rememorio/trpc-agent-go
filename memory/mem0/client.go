//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	httpHeaderAuthorization = "Authorization"
	httpHeaderAccept        = "Accept"
	httpHeaderContentType   = "Content-Type"

	httpContentTypeJSON = "application/json"

	httpMethodGet    = "GET"
	httpMethodPost   = "POST"
	httpMethodPut    = "PUT"
	httpMethodDelete = "DELETE"

	maxRetries       = 3
	retryBaseBackoff = 200 * time.Millisecond
	retryMaxBackoff  = 2 * time.Second
)

type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("mem0 api request failed: status=%d body=%s",
		e.StatusCode, e.Body)
}

type client struct {
	host      string
	apiKey    string
	orgID     string
	projectID string

	hc      *http.Client
	timeout time.Duration

	rndMu sync.Mutex
	rnd   *rand.Rand
}

func newClient(opts serviceOpts) (*client, error) {
	if opts.apiKey == "" {
		return nil, errors.New("mem0 api key is required")
	}
	hc := opts.client
	if hc == nil {
		hc = &http.Client{}
	}

	host := strings.TrimRight(opts.host, "/")
	seed := time.Now().UnixNano()

	return &client{
		host:      host,
		apiKey:    opts.apiKey,
		orgID:     opts.orgID,
		projectID: opts.projectID,
		hc:        hc,
		timeout:   opts.timeout,
		rnd:       rand.New(rand.NewSource(seed)),
	}, nil
}

func (c *client) doJSON(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	in any,
	out any,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	u, err := url.Parse(c.host)
	if err != nil {
		return fmt.Errorf("mem0: invalid host: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("mem0: marshal request failed: %w", err)
		}
		payload = b
	}

	attempts := maxRetries + 1
	for i := 0; i < attempts; i++ {
		err = c.doJSONOnce(ctx, method, u.String(), payload, out)
		if err == nil {
			return nil
		}
		if !shouldRetry(err) {
			return err
		}
		if i == attempts-1 {
			return err
		}
		sleep := c.retrySleepDuration(i)
		t := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return err
}

func (c *client) doJSONOnce(
	ctx context.Context,
	method string,
	urlStr string,
	payload []byte,
	out any,
) error {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	if method == "" {
		return errors.New("mem0: http method is empty")
	}
	if urlStr == "" {
		return errors.New("mem0: url is empty")
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return fmt.Errorf("mem0: build request failed: %w", err)
	}

	req.Header.Set(httpHeaderAuthorization, "Token "+c.apiKey)
	req.Header.Set(httpHeaderAccept, httpContentTypeJSON)
	if payload != nil {
		req.Header.Set(httpHeaderContentType, httpContentTypeJSON)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("mem0: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("mem0: read response failed: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	if out == nil {
		return nil
	}
	if len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("mem0: unmarshal response failed: %w", err)
	}
	return nil
}

func shouldRetry(err error) bool {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		if apiErr.StatusCode >= 500 {
			return true
		}
		return false
	}
	return false
}

// retrySleepDuration returns a jittered backoff duration for the
// given attempt, protecting the internal rand source with a mutex.
func (c *client) retrySleepDuration(attempt int) time.Duration {
	c.rndMu.Lock()
	defer c.rndMu.Unlock()
	return retrySleep(c.rnd, attempt)
}

func retrySleep(r *rand.Rand, attempt int) time.Duration {
	base := retryBaseBackoff
	max := retryMaxBackoff
	if attempt <= 0 {
		attempt = 1
	}
	pow := 1 << attempt
	d := time.Duration(pow) * base
	if d > max {
		d = max
	}
	if r == nil {
		return d
	}
	jitter := time.Duration(r.Int63n(int64(d / 2)))
	return d/2 + jitter
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
