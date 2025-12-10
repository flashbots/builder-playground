package playground

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// pullFuture represents a pending image pull operation
type pullFuture struct {
	waitCh chan struct{}
	err    error
}

func newPullFuture() *pullFuture {
	return &pullFuture{
		waitCh: make(chan struct{}),
	}
}

// wait blocks until the pull completes or context is canceled
func (f *pullFuture) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.waitCh:
		return f.err
	}
}

// complete marks the pull as done and unblocks all waiters
func (f *pullFuture) complete(err error) {
	f.err = err
	close(f.waitCh)
}

// imagePuller coordinates image pulls to prevent duplicate pulls
type imagePuller struct {
	client *client.Client
	mu     sync.Mutex
	pulls  map[string]*pullFuture
}

func newImagePuller(client *client.Client) *imagePuller {
	return &imagePuller{
		client: client,
		pulls:  make(map[string]*pullFuture),
	}
}

// PullImage pulls an image if needed, or waits for an existing pull
func (p *imagePuller) PullImage(ctx context.Context, imageName string) error {
	p.mu.Lock()

	// Check if already pulling
	if future, ok := p.pulls[imageName]; ok {
		p.mu.Unlock()
		return future.wait(ctx)
	}

	// Start a new pull
	future := newPullFuture()
	p.pulls[imageName] = future
	p.mu.Unlock()

	// Do the actual pull
	err := p.pullImageImpl(ctx, imageName)
	future.complete(err)

	// Clean up the future
	p.mu.Lock()
	delete(p.pulls, imageName)
	p.mu.Unlock()

	return err
}

func (p *imagePuller) pullImageImpl(ctx context.Context, imageName string) error {
	reader, err := p.client.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Consume the output to ensure pull completes
	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return fmt.Errorf("error during image pull %s: %w", imageName, err)
	}

	return nil
}
