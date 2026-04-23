package llmdocsclient

import (
	"google.golang.org/grpc"
)

// Client wraps the generated gRPC client with connection ownership for easy reuse.
type Client struct {
	conn *grpc.ClientConn
	DocsServiceClient
}

// Dial creates a llm-docs gRPC client for the given target. The connection
// is established lazily on the first RPC.
func Dial(target string, opts ...grpc.DialOption) (*Client, error) {
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:              conn,
		DocsServiceClient: NewDocsServiceClient(conn),
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
