package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const (
	defaultLockTimeout = 2 * time.Second
	defaultLockRetry   = 10 * time.Millisecond
)

type appendResult struct {
	Event     Event
	Recorded  bool
	Duplicate bool
}

func appendEvent(path string, event Event) (appendResult, error) {
	return appendEventWithTimeout(path, event, defaultLockTimeout)
}

func appendEventWithTimeout(path string, event Event, timeout time.Duration) (appendResult, error) {
	if path == "" {
		return appendResult{}, fmt.Errorf("activity path is empty")
	}
	if err := ValidateEvent(event); err != nil {
		return appendResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return appendResult{}, fmt.Errorf("create activity directory for %s: %w", path, err)
	}

	lock := flock.New(path + ".lock")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, defaultLockRetry)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return appendResult{}, fmt.Errorf("timed out acquiring activity lock for %s", path)
		}
		return appendResult{}, fmt.Errorf("acquire activity lock for %s: %w", path, err)
	}
	if !locked {
		return appendResult{}, fmt.Errorf("timed out acquiring activity lock for %s", path)
	}
	defer func() { _ = lock.Unlock() }()

	if event.IdempotencyKey != "" {
		existing, err := findIdempotencyEvent(path, event.IdempotencyKey)
		if err != nil {
			return appendResult{}, err
		}
		if existing != nil {
			if existing.SkillKey != event.SkillKey || existing.EventType != event.EventType {
				return appendResult{}, fmt.Errorf("idempotency key %q conflicts with an existing %s event for skill %s", event.IdempotencyKey, existing.EventType, existing.SkillID)
			}
			return appendResult{Event: *existing, Duplicate: true}, nil
		}
	}

	data, err := json.Marshal(event)
	if err != nil {
		return appendResult{}, fmt.Errorf("serialize activity event: %w", err)
	}
	line := append(data, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return appendResult{}, fmt.Errorf("open activity file %s: %w", path, err)
	}
	written, writeErr := file.Write(line)
	if writeErr == nil && written != len(line) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return appendResult{}, fmt.Errorf("write activity file %s: %w", path, writeErr)
	}
	if closeErr != nil {
		return appendResult{}, fmt.Errorf("close activity file %s: %w", path, closeErr)
	}
	return appendResult{Event: event, Recorded: true}, nil
}

func findIdempotencyEvent(path, key string) (*Event, error) {
	scan, err := scanEvents(path)
	if err != nil {
		return nil, err
	}
	for _, event := range scan.Events {
		if event.IdempotencyKey == key {
			return &event, nil
		}
	}
	return nil, nil
}
