package tgclient

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// Client wraps a gotd/td Telegram MTProto client for userbot operations.
type Client struct {
	appID      int
	appHash    string
	sessStore  session.Storage
	log        *slog.Logger
	client     *telegram.Client
	api        *tg.Client
	cancelRun  context.CancelFunc
	runDone    chan struct{}
}

// New creates a new MTProto client. Call Run to start the connection.
func New(appID int, appHash string, sessStore session.Storage, log *slog.Logger) *Client {
	return &Client{
		appID:   appID,
		appHash: appHash,
		sessStore: sessStore,
		log:     log,
		runDone: make(chan struct{}),
	}
}

// Run starts the MTProto client and executes fn within the active connection.
// The client is connected for the lifetime of fn. When fn returns, the client disconnects.
func (c *Client) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	c.client = telegram.NewClient(c.appID, c.appHash, telegram.Options{
		SessionStorage: c.sessStore,
	})

	return c.client.Run(ctx, func(ctx context.Context) error {
		c.api = c.client.API()
		return fn(ctx)
	})
}

// Auth performs interactive authentication using BotConversationAuth.
// It checks if the user is already authorized; if so, returns immediately.
func (c *Client) Auth(ctx context.Context, conversationAuth *BotConversationAuth) error {
	if c.api == nil {
		return fmt.Errorf("client not running: call Run first")
	}

	status, err := c.client.Auth().Status(ctx)
	if err != nil {
		return fmt.Errorf("check auth status: %w", err)
	}
	if status.Authorized {
		c.log.Info("already authorized", "user_id", status.User.ID)
		return nil
	}

	flow := auth.NewFlow(conversationAuth, auth.SendCodeOptions{})
	if err := c.client.Auth().IfNecessary(ctx, flow); err != nil {
		return fmt.Errorf("auth flow: %w", err)
	}

	return nil
}

// API returns the raw tg.Client for making MTProto calls.
// Only valid after Run has been called and while fn is executing.
func (c *Client) API() *tg.Client {
	return c.api
}

// Self returns the authorized user info.
func (c *Client) Self(ctx context.Context) (*tg.User, error) {
	if c.api == nil {
		return nil, fmt.Errorf("client not running")
	}
	me, err := c.api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return nil, fmt.Errorf("get self: %w", err)
	}
	if len(me) == 0 {
		return nil, fmt.Errorf("empty user response")
	}
	user, ok := me[0].(*tg.User)
	if !ok {
		return nil, fmt.Errorf("unexpected user type: %T", me[0])
	}
	return user, nil
}
