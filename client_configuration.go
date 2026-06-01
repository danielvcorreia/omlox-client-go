// Copyright (c) Omlox Client Go Contributors
// SPDX-License-Identifier: MIT

package omlox

import (
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/go-cleanhttp"
	"golang.org/x/time/rate"
)

// GetDefaultOptions returns default configuration options for the client.
func DefaultConfiguration() ClientConfiguration {
	// Use cleanhttp, which has the same default values as net/http client, but
	// does not share state with other clients (see: gh/hashicorp/go-cleanhttp)
	defaultClient := cleanhttp.DefaultPooledClient()

	return ClientConfiguration{
		HTTPClient:     defaultClient,
		RequestTimeout: 60 * time.Second,
	}
}

// / ClientConfiguration is used to configure the creation of the client.
type ClientConfiguration struct {
	// HTTPClient is the HTTP client to use for all API requests.
	HTTPClient *http.Client

	// RequestTimeout, given a non-negative value, will apply the timeout to
	// each request function unless an earlier deadline is passed to the
	// request function through context.Context.
	//
	// Default: 60s
	RequestTimeout time.Duration

	// RateLimiter controls how frequently requests are allowed to happen.
	// If this pointer is nil, then there will be no limit set. Note that an
	// empty struct rate.Limiter is equivalent to blocking all requests.
	//
	// Default: nil
	RateLimiter *rate.Limiter

	// UserAgent sets a name for the http client User-Agent header.
	UserAgent string

	// Reconnect configures automatic websocket reconnection with
	// exponential backoff.
	//
	// Default: nil
	Reconnect *ReconnectOptions
}

// ReconnectOptions configures automatic websocket reconnection behavior.
type ReconnectOptions struct {
	// MinWait is the minimum duration to wait before attempting reconnection.
	MinWait time.Duration

	// MaxWait is the maximum duration to wait before the next reconnection attempt.
	MaxWait time.Duration
}

// ClientOption is a configuration option to initialize a client.
type ClientOption func(*ClientConfiguration) error

// WithHTTPClient sets the HTTP client to use for all API requests.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *ClientConfiguration) error {
		c.HTTPClient = client
		return nil
	}
}

// WithRequestTimeout, given a non-negative value, will apply the timeout to
// each request function unless an earlier deadline is passed to the request
// function through context.Context.
//
// Default: 60s
func WithRequestTimeout(timeout time.Duration) ClientOption {
	return func(c *ClientConfiguration) error {
		if timeout < 0 {
			return fmt.Errorf("request timeout must not be negative")
		}
		c.RequestTimeout = timeout
		return nil
	}
}

// WithRateLimiter configures how frequently requests are allowed to happen.
// If this pointer is nil, then there will be no limit set. Note that an
// empty struct rate.Limiter is equivalent to blocking all requests.
//
// Default: nil
func WithRateLimiter(limiter *rate.Limiter) ClientOption {
	return func(c *ClientConfiguration) error {
		c.RateLimiter = limiter
		return nil
	}
}

// WithReconnect enables automatic websocket reconnection with exponential
// backoff and jitter.
//
// Default: nil
func WithReconnect(minWait, maxWait time.Duration) ClientOption {
	return func(c *ClientConfiguration) error {
		if minWait <= 0 {
			return fmt.Errorf("minWait must be positive")
		}
		if maxWait <= 0 {
			return fmt.Errorf("maxWait must be positive")
		}
		if minWait > maxWait {
			return fmt.Errorf("minWait must not exceed maxWait")
		}
		c.Reconnect = &ReconnectOptions{
			MinWait: minWait,
			MaxWait: maxWait,
		}
		return nil
	}
}
