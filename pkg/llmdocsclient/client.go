package llmdocsclient

import (
	"context"

	"google.golang.org/grpc"
)

// Client wraps the generated gRPC client with connection ownership for easy reuse.
type Client struct {
	conn *grpc.ClientConn
	DocsServiceClient
}

// DialContext dials a llm-docs gRPC endpoint and returns a typed client.
func DialContext(ctx context.Context, target string, opts ...grpc.DialOption) (*Client, error) {
	conn, err := grpc.DialContext(ctx, target, opts...)
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
