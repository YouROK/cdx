package mcp

import "context"

// transport carries JSON-RPC frames to and from one server.
type transport interface {
	// Start connects the transport and delivers every received frame to handler.
	Start(ctx context.Context, handler func([]byte)) error
	// Send writes one frame. The handler may be called from Send when the transport
	// receives the reply on the same stream. The headers are transport hints such as
	// the Mcp-* request mirrors of Streamable HTTP; stdio ignores them.
	Send(ctx context.Context, frame []byte, headers map[string]string) error
	// Closed reports that the connection is gone and no reply will arrive.
	Closed() <-chan struct{}
	Close() error
}

// revisionSetter is implemented by transports that mirror the protocol revision into headers.
type revisionSetter interface {
	SetRevision(revision string)
}

// failureReporter is implemented by transports that can explain a closed connection.
type failureReporter interface {
	Failure() (error, []string)
}
