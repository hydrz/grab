package grab

import (
	"context"
	"log/slog"

	"github.com/go-resty/resty/v2"
)

// Context implements the Context for internal use.
type Context struct {
	ctx              context.Context
	option           Option
	client           *resty.Client
	logger           *slog.Logger
	progressCallback ProgressCallback
}

// NewContext creates a new Context with the provided options.
func NewContext(ctx context.Context, option Option) *Context {
	client := newClient(option)
	logger := newLogger(option)
	return &Context{
		ctx:    ctx,
		option: option,
		client: client,
		logger: logger,
	}
}

// Context returns the context associated with this Context.
func (c *Context) Context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// Option returns the options associated with this Context.
func (c *Context) Option() Option {
	return c.option
}

// Client returns the resty client associated with this Context.
func (c *Context) Client() *resty.Client {
	if c.client == nil {
		c.client = newClient(c.Option())
	}
	return c.client
}

// Logger returns the logger associated with this Context.
func (c *Context) Logger() *slog.Logger {
	if c.logger == nil {
		c.logger = newLogger(c.Option())
	}
	return c.logger
}

// SetProgressCallback sets the progress callback for the context
func (c *Context) SetProgressCallback(callback ProgressCallback) {
	c.progressCallback = callback
}

// GetProgressCallback returns the progress callback
func (c *Context) GetProgressCallback() ProgressCallback {
	return c.progressCallback
}
