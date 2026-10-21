/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package sitedeploy

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Store is an S3 bucket as an ObjectStore.
type S3Store struct {
	client *s3.Client
	bucket string
}

// NewS3Store stores objects in bucket.
func NewS3Store(client *s3.Client, bucket string) *S3Store {
	return &S3Store{client: client, bucket: bucket}
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer func() { _ = out.Body.Close() }()
	return io.ReadAll(out.Body)
}

func (s *S3Store) Put(ctx context.Context, req PutRequest) error {
	sum, err := hex.DecodeString(req.MD5)
	if err != nil {
		return err
	}
	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(req.Key),
		Body:          req.Body,
		ContentLength: aws.Int64(req.Size),
		ContentMD5:    aws.String(base64.StdEncoding.EncodeToString(sum)),
		ContentType:   aws.String(req.ContentType),
		CacheControl:  aws.String(req.CacheControl),
	}
	if req.ContentEncoding != "" {
		input.ContentEncoding = aws.String(req.ContentEncoding)
	}
	_, err = s.client.PutObject(ctx, input)
	return err
}
