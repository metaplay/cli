/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package sitedeploy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// The manifest is kept next to the site's files, under this name.
const manifestFileName = ".metaplay-site.json"

const manifestFormat = 1

// Manifest records the objects deploys of a site have uploaded. A deploy
// skips files it already uploaded identically, and only ever removes objects
// a manifest names, so nothing else in the bucket can be touched. It is
// public, next to the site, so that it also tells others, such as the
// developer portal, that a site is deployed there and when.
type Manifest struct {
	Format     int                       `json:"format"`
	Name       string                    `json:"name"` // The web client's name in metaplay-project.yaml.
	DeployedAt time.Time                 `json:"deployedAt"`
	Objects    map[string]ManifestObject `json:"objects"`
}

// ManifestObject is an object a deploy uploaded.
type ManifestObject struct {
	Size            int64     `json:"size"`
	MD5             string    `json:"md5"`
	ContentType     string    `json:"contentType"`
	ContentEncoding string    `json:"contentEncoding,omitempty"`
	CacheControl    string    `json:"cacheControl"`
	DeployedAt      time.Time `json:"deployedAt"` // The last deploy whose site had the file.
}

// ErrNotFound is what an ObjectStore returns for a key that has no object.
var ErrNotFound = errors.New("object not found")

// PutRequest is an object to upload.
type PutRequest struct {
	Key             string
	Body            io.ReadSeeker
	Size            int64
	MD5             string
	ContentType     string
	ContentEncoding string
	CacheControl    string
}

// ObjectStore is the bucket a site is deployed to.
type ObjectStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, req PutRequest) error
}

// ReadManifest reads the manifest of the site at prefix, or nil where nothing
// has been deployed there.
func ReadManifest(ctx context.Context, store ObjectStore, prefix string) (*Manifest, error) {
	data, err := store.Get(ctx, ManifestKey(prefix))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the site manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse the site manifest %s: %w", ManifestKey(prefix), err)
	}
	if manifest.Format != manifestFormat {
		return nil, fmt.Errorf("the site manifest %s has format %d, expecting %d", ManifestKey(prefix), manifest.Format, manifestFormat)
	}
	return &manifest, nil
}

// Deploy uploads the plan's changed objects phase by phase, each phase
// finished before the next starts, and then the manifest of the site called
// name. onUploaded is called after each upload with how many of the plan's
// uploads are done.
func Deploy(ctx context.Context, store ObjectStore, name string, plan *Plan, previous *Manifest, now time.Time, concurrency int, onUploaded func(done, total int)) (*Manifest, error) {
	total := plan.UploadCount()
	var done atomic.Int64

	for start := 0; start < len(plan.Objects); {
		end := start
		for end < len(plan.Objects) && plan.Objects[end].Phase == plan.Objects[start].Phase {
			end++
		}

		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(concurrency)
		for _, obj := range plan.Objects[start:end] {
			if obj.Unchanged {
				continue
			}
			group.Go(func() error {
				if err := putFile(groupCtx, store, obj); err != nil {
					return fmt.Errorf("failed to upload %s: %w", obj.Key, err)
				}
				if onUploaded != nil {
					onUploaded(int(done.Add(1)), total)
				}
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return nil, err
		}
		start = end
	}

	manifest := nextManifest(name, previous, plan, now)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	sum := md5.Sum(data)
	err = store.Put(ctx, PutRequest{
		Key:          plan.ManifestKey,
		Body:         bytes.NewReader(data),
		Size:         int64(len(data)),
		MD5:          hex.EncodeToString(sum[:]),
		ContentType:  contentTypes[".json"],
		CacheControl: DefaultCacheControl,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload the site manifest: %w", err)
	}
	return manifest, nil
}

func putFile(ctx context.Context, store ObjectStore, obj Object) error {
	file, err := os.Open(obj.LocalFile)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return store.Put(ctx, PutRequest{
		Key:             obj.Key,
		Body:            file,
		Size:            obj.Size,
		MD5:             obj.MD5,
		ContentType:     obj.ContentType,
		ContentEncoding: obj.ContentEncoding,
		CacheControl:    obj.CacheControl,
	})
}

// nextManifest records the plan's objects on top of the previous manifest's:
// a deploy deletes nothing, so the objects of earlier deploys are still in the
// bucket and stay on record.
func nextManifest(name string, previous *Manifest, plan *Plan, now time.Time) *Manifest {
	manifest := &Manifest{Format: manifestFormat, Name: name, DeployedAt: now, Objects: map[string]ManifestObject{}}
	if previous != nil {
		for key, obj := range previous.Objects {
			manifest.Objects[key] = obj
		}
	}
	for _, obj := range plan.Objects {
		manifest.Objects[obj.Key] = ManifestObject{
			Size:            obj.Size,
			MD5:             obj.MD5,
			ContentType:     obj.ContentType,
			ContentEncoding: obj.ContentEncoding,
			CacheControl:    obj.CacheControl,
			DeployedAt:      now,
		}
	}
	return manifest
}
