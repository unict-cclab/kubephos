package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

type monitorStore struct {
	renew     func(context.Context, string, string) error
	requested func(context.Context, string) (bool, error)
}

func (s monitorStore) RenewLease(ctx context.Context, operationID, owner string) error {
	return s.renew(ctx, operationID, owner)
}

func (s monitorStore) OperationCancelRequested(ctx context.Context, operationID string) (bool, error) {
	return s.requested(ctx, operationID)
}

func TestMonitorRenewsLeaseDespiteCancellationReadFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	renewals, reads := 0, 0
	store := monitorStore{
		renew: func(ctx context.Context, operationID, owner string) error {
			if operationID != "operation" || owner != "worker" {
				t.Fatalf("incorrect lease identity %q/%q", operationID, owner)
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("database call has no bounded deadline")
			}
			renewals++
			return nil
		},
		requested: func(context.Context, string) (bool, error) {
			reads++
			if reads == 1 {
				return false, errors.New("temporary database error")
			}
			return true, nil
		},
	}
	canceled := false
	monitorOperation(ctx, func() { canceled = true; cancel() }, make(chan struct{}), store, "worker", "operation", time.Millisecond)
	if !canceled || reads != 2 || renewals != 2 {
		t.Fatalf("canceled=%v reads=%d renewals=%d", canceled, reads, renewals)
	}
}

func TestMonitorStillChecksCancellationAfterRenewalFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	checked := false
	store := monitorStore{
		renew:     func(context.Context, string, string) error { return errors.New("temporary database error") },
		requested: func(context.Context, string) (bool, error) { checked = true; return true, nil },
	}
	monitorOperation(ctx, cancel, make(chan struct{}), store, "worker", "operation", time.Millisecond)
	if !checked {
		t.Fatal("cancellation check skipped")
	}
}

func TestMonitorStopsWhenOperationCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	close(done)
	store := monitorStore{
		renew: func(context.Context, string, string) error { t.Fatal("renewed a completed operation"); return nil },
		requested: func(context.Context, string) (bool, error) {
			t.Fatal("queried a completed operation")
			return false, nil
		},
	}
	monitorOperation(ctx, cancel, done, store, "worker", "operation", time.Hour)
	if ctx.Err() != nil {
		t.Fatal("completed monitor canceled operation context")
	}
}
