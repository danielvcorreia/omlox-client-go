// Copyright (c) Omlox Client Go Contributors
// SPDX-License-Identifier: MIT

package omlox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	chanSendTimeout = 100 * time.Millisecond

	// time allowed to read the next pong message from the peer.
	pongWait = 10 * time.Second
	// send pings to peer with this period. Must be less than pongWait.
	pingPeriod = pongWait * 9 / 10

	wsScheme    = "ws"
	wsSchemeTLS = "wss"

	httpScheme    = "http"
	httpSchemeTLS = "https"
)

var (
	SubscriptionTimeout = 3 * time.Second
)

// Errors
var (
	ErrBadWrapperObject = errors.New("invalid wrapper object")
	ErrTimeout          = errors.New("timeout")
)

// wrapperObject is an internal abstraction of the websockets data exchange object.
type wrapperObject struct {
	// Embedded error fields. Will only be present on error: `event` is error.
	WebsocketError

	// Wrapper object of websockets data exchanged between client and server
	WrapperObject
}

var _ slog.LogValuer = (*wrapperObject)(nil)

// Connect will attempt to connect to the Omlox™ Hub websockets interface.
func Connect(ctx context.Context, addr string, options ...ClientOption) (*Client, error) {
	c, err := New(addr, options...)
	if err != nil {
		return nil, err
	}

	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	return c, nil
}

// Connect dials the Omlox™ Hub websockets interface with automatic retry support.
// It follows the go-retryablehttp pattern for handling connection failures.
func (c *Client) Connect(ctx context.Context) error {
	var err error
	var shouldRetry bool
	var attempt int

	// Setup reconnection context if auto-reconnect is enabled
	if c.configuration.WSAutoReconnect {
		c.mu.Lock()
		c.reconnectCtx, c.reconnectCancel = context.WithCancel(context.Background())
		c.mu.Unlock()
	}

	for attempt = 0; ; attempt++ {
		err = c.connect(ctx)
		if err == nil {
			return nil
		}

		// Check if we should retry
		shouldRetry, err = c.configuration.WSCheckRetry(ctx, attempt, err)
		if !shouldRetry {
			break
		}

		// Check retry limit
		remain := c.configuration.WSMaxRetries - attempt
		if c.configuration.WSMaxRetries >= 0 && remain <= 0 {
			break
		}

		// Calculate backoff wait time
		wait := c.configuration.WSBackoff(
			c.configuration.WSMinRetryWait,
			c.configuration.WSMaxRetryWait,
			attempt,
		)

		slog.LogAttrs(
			ctx,
			slog.LevelDebug,
			"retrying connection",
			slog.Int("attempt", attempt+1),
			slog.Duration("wait", wait),
			slog.Int("remaining", remain),
			slog.Any("error", err),
		)

		// Wait before retrying
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	// All retries exhausted
	if err == nil {
		return fmt.Errorf("connection failed after %d attempt(s)", attempt)
	}
	return fmt.Errorf("connection failed after %d attempt(s): %w", attempt, err)
}

// connect performs a single connection attempt to the Omlox™ Hub websockets interface.
func (c *Client) connect(ctx context.Context) error {
	if !c.isClosed() {
		// close the connection if it happens to be open
		if err := c.Close(); err != nil {
			return err
		}
	}

	wsURL := c.baseAddress.JoinPath("/ws/socket")

	if err := upgradeToWebsocketScheme(wsURL); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)

	conn, _, err := websocket.Dial(ctx, wsURL.String(), &websocket.DialOptions{
		HTTPClient: c.client,
	})
	if err != nil {
		cancel()
		return err
	}

	slog.LogAttrs(
		ctx,
		slog.LevelDebug,
		"connected",
		slog.String("host", wsURL.Hostname()),
		slog.String("path", wsURL.EscapedPath()),
		slog.Bool("secured", wsURL.Scheme == wsSchemeTLS),
	)

	c.mu.Lock()
	c.conn = conn
	c.closed = false
	c.cancel = cancel
	c.mu.Unlock()

	// Start managed goroutines with WaitGroup tracking
	c.lifecycleWg.Add(2)

	go func() {
		defer c.lifecycleWg.Done()
		if err := c.readLoop(ctx); err != nil {
			c.handleConnectionLoss(err)
		}
	}()

	go func() {
		defer c.lifecycleWg.Done()
		if err := c.pingLoop(ctx); err != nil {
			c.handleConnectionLoss(err)
		}
	}()

	return nil
}

// handleConnectionLoss handles unexpected connection failures and triggers reconnection.
// This is called by readLoop or pingLoop when they detect a connection error.
func (c *Client) handleConnectionLoss(err error) {
	// Check if this is a normal shutdown
	select {
	case <-c.shutdownCh:
		// Normal shutdown, don't reconnect
		return
	default:
	}

	c.mu.RLock()
	reconnectCtx := c.reconnectCtx
	autoReconnect := c.configuration.WSAutoReconnect
	c.mu.RUnlock()

	// Check if auto-reconnect is disabled
	if !autoReconnect {
		slog.LogAttrs(
			context.Background(),
			slog.LevelWarn,
			"connection lost, auto-reconnect disabled",
			slog.Any("error", err),
		)
		return
	}

	// Check if reconnection context is cancelled
	select {
	case <-reconnectCtx.Done():
		// Reconnection cancelled, don't reconnect
		return
	default:
	}

	// Connection was lost unexpectedly, attempt to reconnect
	slog.LogAttrs(
		context.Background(),
		slog.LevelWarn,
		"connection lost, attempting to reconnect",
		slog.Any("error", err),
	)

	c.mu.Lock()
	c.reconnecting = true
	c.mu.Unlock()

	// Attempt to reconnect with backoff
	if err := c.reconnect(reconnectCtx); err != nil {
		slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			"reconnection failed",
			slog.Any("error", err),
		)
		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()
		return
	}

	c.mu.Lock()
	c.reconnecting = false
	c.mu.Unlock()

	slog.LogAttrs(
		context.Background(),
		slog.LevelInfo,
		"successfully reconnected",
	)
}

// Publish a message to the Omlox Hub.
func (c *Client) Publish(ctx context.Context, topic Topic, payload ...json.RawMessage) error {
	if topic == "" {
		return errors.New("empty topic")
	}

	wrObj := &WrapperObject{
		Event:   EventMsg,
		Topic:   topic,
		Payload: payload,
	}

	return c.publish(ctx, wrObj)
}

func (c *Client) publish(ctx context.Context, wrObj *WrapperObject) (err error) {
	// TODO @dvcorreia: maybe this log should be a metric instead.
	defer slog.LogAttrs(context.Background(), slog.LevelDebug, "published", slog.Any("err", err), slog.Any("event", wrObj))

	if c.isClosed() {
		return net.ErrClosed
	}

	// TODO @dvcorreia: use the easyjson marshal method.
	return wsjson.Write(ctx, c.conn, wrObj)
}

// Subscribe to a topic in Omlox Hub.
func (c *Client) Subscribe(ctx context.Context, topic Topic, params ...Parameter) (*Subcription, error) {
	parameters := make(Parameters)
	for _, param := range params {
		if err := param(topic, parameters); err != nil {
			return nil, err
		}
	}

	return c.subscribe(ctx, topic, parameters)
}

// Sends a subscription message and handles the confirmation from the server.
//
// The subscription will be attributed an ID that can used for futher context.
// There can only be one pending subscription at each time.
// Subsequent subscriptions will wait while the pending one is waiting for an ID from the server.
// Since each subscription on a topic can have a distinct parameters, we must synchronisly wait to match each one to its ID.
func (c *Client) subscribe(ctx context.Context, topic Topic, params Parameters) (*Subcription, error) {
	// channel to await subscription confirmation
	await := make(chan struct {
		sid int
		err error
	})
	defer close(await)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	// lock for pending subscription confirmation.
	// the pending will be freed by the subribed message handler.
	case c.pending <- await:
	}

	wrObj := &WrapperObject{
		Event:   EventSubscribe,
		Topic:   topic,
		Params:  params,
		Payload: nil,
	}

	if err := c.publish(ctx, wrObj); err != nil {
		<-c.pending // clear pending subscription
		return nil, err
	}

	// wait for subcription ID
	var r struct {
		sid int
		err error
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r = <-await:
	}

	if r.err != nil {
		return nil, r.err
	}

	sub := &Subcription{
		// sid:    r.sid,
		sid:    0, // BUG: deephub doesn't return the sid in subsequent messages (NEEDS FIX!)
		topic:  topic,
		params: params,
		mch:    make(chan *WrapperObject, 1),
	}

	// promote a pending subcription
	c.mu.Lock()
	c.subs[sub.sid] = sub
	c.mu.Unlock()

	return sub, nil
}

// ping pong loop that manages the websocket connection health.
func (c *Client) pingLoop(ctx context.Context) error {
	t := time.NewTicker(pingPeriod)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}

		ctx, cancel := context.WithTimeout(ctx, pongWait)
		defer cancel()

		begin := time.Now()
		err := c.conn.Ping(ctx)

		if err != nil {
			// context was exceded and the client should close
			if errors.Is(err, context.Canceled) {
				return nil
			}

			if errors.Is(err, context.DeadlineExceeded) { // TODO @dvcorreia: redundant?
				// ping could not be done, context exceded and connecting will be closed
				// reconnect or close the client
				return err
			}

			return err
		}

		slog.Debug("heartbeat", slog.Duration("latency", time.Since(begin)))
	}
}

// readLoop that will handle incomming data.
func (c *Client) readLoop(ctx context.Context) error {
	// set the client to closed state when this goroutine exits
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
	}()

	for {
		msgType, r, err := c.conn.Reader(ctx)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			// when received StatusNormalClosure or StatusGoingAway close frame will be translated to io.EOF when reading
			// the TCP connection can also return it, which is passed down from the lib to here
			if errors.Is(err, io.EOF) {
				return nil
			}

			// when the connection is closed, due to something (e.g. ping deadline, etc)
			var e net.Error
			if errors.As(err, &e) {
				return nil
			}

			switch s := websocket.CloseStatus(err); s {
			case websocket.StatusGoingAway, websocket.StatusNormalClosure:
				return nil
			}

			return err
		}

		// assumed that messages can only betext until further specification.
		if msgType != websocket.MessageText {
			continue
		}

		var (
			wrObj wrapperObject
			d     = json.NewDecoder(r) // TODO @dvcorreia: maybe use easyjson
		)
		if err := d.Decode(&wrObj); err != nil {
			// TODO @dvcorreia: print debug logs or provide metrics
			continue
		}

		slog.LogAttrs(context.Background(), slog.LevelDebug, "received", slog.Any("event", wrObj))

		c.handleMessage(ctx, &wrObj)
	}
}

// reconnect attempts to re-establish the WebSocket connection with retry logic.
func (c *Client) reconnect(ctx context.Context) error {
	var err error
	var shouldRetry bool
	var attempt int

	for attempt = 0; ; attempt++ {
		err = c.connect(ctx)
		if err == nil {
			// Connection successful, restore subscriptions
			return c.restoreSubscriptions(ctx)
		}

		// Check if we should retry
		shouldRetry, err = c.configuration.WSCheckRetry(ctx, attempt, err)
		if !shouldRetry {
			break
		}

		// Check retry limit
		remain := c.configuration.WSMaxRetries - attempt
		if c.configuration.WSMaxRetries >= 0 && remain <= 0 {
			break
		}

		// Calculate backoff wait time
		wait := c.configuration.WSBackoff(
			c.configuration.WSMinRetryWait,
			c.configuration.WSMaxRetryWait,
			attempt,
		)

		slog.LogAttrs(
			ctx,
			slog.LevelDebug,
			"retrying reconnection",
			slog.Int("attempt", attempt+1),
			slog.Duration("wait", wait),
			slog.Int("remaining", remain),
			slog.Any("error", err),
		)

		// Wait before retrying
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	// All retries exhausted
	if err == nil {
		return fmt.Errorf("reconnection failed after %d attempt(s)", attempt)
	}
	return fmt.Errorf("reconnection failed after %d attempt(s): %w", attempt, err)
}

// restoreSubscriptions re-establishes all active subscriptions after reconnection.
func (c *Client) restoreSubscriptions(ctx context.Context) error {
	// Capture subscriptions that need to be restored
	c.mu.RLock()
	subsToRestore := make(map[int]struct {
		topic  Topic
		params Parameters
		oldCh  chan *WrapperObject
	})
	for sid, sub := range c.subs {
		subsToRestore[sid] = struct {
			topic  Topic
			params Parameters
			oldCh  chan *WrapperObject
		}{
			topic:  sub.topic,
			params: sub.params,
			oldCh:  sub.mch,
		}
	}
	c.mu.RUnlock()

	slog.LogAttrs(
		ctx,
		slog.LevelInfo,
		"restoring subscriptions",
		slog.Int("count", len(subsToRestore)),
	)

	for sid, params := range subsToRestore {
		sub, err := c.subscribe(ctx, params.topic, params.params)
		if err != nil {
			slog.LogAttrs(
				ctx,
				slog.LevelError,
				"failed to restore subscription",
				slog.Int("sid", sid),
				slog.String("topic", string(params.topic)),
				slog.Any("error", err),
			)
			// Continue trying to restore other subscriptions
			continue
		}

		// Route new subscription messages to the old subscription's channel
		// This ensures consumers don't need to resubscribe after reconnection
		go func(oldCh, newCh chan *WrapperObject) {
			for msg := range newCh {
				select {
				case oldCh <- msg:
				case <-time.After(chanSendTimeout):
					slog.LogAttrs(
						context.Background(),
						slog.LevelWarn,
						"timeout sending restored subscription message",
					)
				}
			}
		}(params.oldCh, sub.mch)

		slog.LogAttrs(
			ctx,
			slog.LevelDebug,
			"subscription restored",
			slog.Int("sid", sid),
			slog.String("topic", string(params.topic)),
		)
	}

	return nil
}

// handleMessage received from the Omlox Hub server.
func (c *Client) handleMessage(ctx context.Context, msg *wrapperObject) {
	switch msg.Event {
	case EventError:
		c.handleError(ctx, msg)
	case EventSubscribed:
		// pop pending subscription and assign subscription ID
		pendingc := <-c.pending
		chsend(ctx, pendingc, struct {
			sid int
			err error
		}{
			sid: msg.SubscriptionID,
		})
		return
	case EventUnsubscribed:
		// TODO @dvcorreia: close subscription
	default:
		c.routeMessage(ctx, &msg.WrapperObject)
	}
}

// handleError handles any websocket error sent by the server.
func (c *Client) handleError(ctx context.Context, msg *wrapperObject) {
	switch msg.WebsocketError.Code {
	case ErrCodeSubscription, ErrCodeNotAuthorized, ErrCodeUnknownTopic, ErrCodeInvalid:
		// pop pending subscription and kill it
		pendingc := <-c.pending
		chsend(ctx, pendingc, struct {
			sid int
			err error
		}{
			err: msg.WebsocketError,
		})
		return
	case ErrCodeUnknown: // TODO @dvcorreia: handle error
	case ErrCodeUnsubscription: // TODO @dvcorreia: handle error
	}
}

// routeMessage sends the message to the its respective subscription.
func (c *Client) routeMessage(ctx context.Context, msg *WrapperObject) {
	// retrive subcription if exists
	c.mu.RLock()
	sub := c.subs[msg.SubscriptionID]
	c.mu.RUnlock()

	if sub == nil {
		// TODO @dvcorreia: handle unknown subscription IDs
		return
	}

	select {
	case <-ctx.Done():
		return
	case sub.mch <- msg: // TODO @dvcorreia: this will block other messages
	case <-time.After(chanSendTimeout):
		slog.LogAttrs(
			context.Background(),
			slog.LevelWarn,
			"timeout sending to subscription channel",
			slog.Any("event", msg),
		)
	}
}

// clearSubs closes resources of subscriptions.
func (c *Client) clearSubs() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for sid, sub := range c.subs {
		sub.close()
		delete(c.subs, sid)
	}

	// close any pending subscription
	select {
	case pending := <-c.pending:
		pending <- struct {
			sid int
			err error
		}{
			err: net.ErrClosed,
		}
	default:
	}

	close(c.pending)
}

// Close releases any resources held by the client,
// such as connections, memory and goroutines.
func (c *Client) Close() error {
	// Signal shutdown to prevent reconnection
	select {
	case <-c.shutdownCh:
		// Already closed
		return nil
	default:
		close(c.shutdownCh)
	}

	// Stop automatic reconnection
	c.mu.Lock()
	if c.reconnectCancel != nil {
		c.reconnectCancel()
	}
	c.mu.Unlock()

	// Close WebSocket connection
	if !c.isClosed() {
		err := c.conn.Close(websocket.StatusNormalClosure, "")
		if err != nil {
			return err
		}
	}

	// Cancel context to stop goroutines
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for all goroutines to finish
	c.lifecycleWg.Wait()

	// Clear subscriptions after all goroutines have stopped
	c.clearSubs()

	return nil
}

// isClosed reports if the client closed.
func (c *Client) isClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// IsConnected reports if the client is currently connected to the WebSocket.
// Returns true if connected, false if closed or reconnecting.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && !c.reconnecting
}

// IsReconnecting reports if the client is currently attempting to reconnect.
func (c *Client) IsReconnecting() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reconnecting
}

func upgradeToWebsocketScheme(u *url.URL) error {
	switch u.Scheme {
	case httpScheme:
		u.Scheme = wsScheme
	case httpSchemeTLS:
		u.Scheme = wsSchemeTLS
	default:
		return fmt.Errorf("invalid websocket scheme '%s'", u.Scheme)
	}

	return nil
}

// LogValue implements slog.LogValuer.
func (w wrapperObject) LogValue() slog.Value {
	if w.Event == EventError {
		return slog.GroupValue(
			slog.Any("error", w.WebsocketError),
		)
	}
	return w.WrapperObject.LogValue()
}

// chsend sends a value to channel with context cancelation.
func chsend[T any](ctx context.Context, ch chan T, v T) {
	select {
	case <-ctx.Done():
		return
	case ch <- v:
	}
}
