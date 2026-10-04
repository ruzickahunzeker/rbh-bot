package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownFailureStillDrainsRecovery(t *testing.T) {
	shutdownErr := errors.New("HTTP shutdown timed out")
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- shutdownAndDrain(func() error { return shutdownErr }, func(ctx context.Context) error {
			if _, bounded := ctx.Deadline(); bounded || ctx.Err() != nil {
				return errors.New("drain must outlive HTTP shutdown deadline")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	select {
	case err := <-done:
		t.Fatalf("returned before recovery stopped: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-done; !errors.Is(err, shutdownErr) {
		t.Fatalf("shutdown error lost: %v", err)
	}
}

func TestShutdownAndDrainPreservesBothErrors(t *testing.T) {
	shutdownErr, drainErr := errors.New("HTTP failure"), errors.New("drain failure")
	err := shutdownAndDrain(func() error { return shutdownErr }, func(context.Context) error { return drainErr })
	if !errors.Is(err, shutdownErr) || !errors.Is(err, drainErr) {
		t.Fatalf("errors lost: %v", err)
	}
}
