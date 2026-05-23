// Package leaderelect provides leader election for rollup transforms.
//
// Two implementations are available:
//   - FileElector: file-based locking (single-node deployments)
//   - DBElector: database-based locking (multi-node deployments)
package leaderelect

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Elector provides leader election for exclusive task execution.
type Elector interface {
	// Acquire attempts to become the leader. Returns true if leadership
	// was acquired, false if another instance holds the lock.
	Acquire(ctx context.Context) (bool, error)

	// Renew extends the leadership lease. Should be called periodically
	// (every ttl/2). Returns false if leadership was lost.
	Renew(ctx context.Context) (bool, error)

	// Release voluntarily gives up leadership.
	Release(ctx context.Context) error
}

// FileElector implements Elector using file-based locking.
// Suitable for single-node deployments where all rollups run on the same host.
type FileElector struct {
	dir      string
	lockName string
	lockFile *os.File
	mu       sync.Mutex
}

// NewFileElector creates a file-based elector.
// lockName identifies the specific lock (e.g., "request-metrics-rollup").
func NewFileElector(dir, lockName string) *FileElector {
	return &FileElector{
		dir:      dir,
		lockName: lockName,
	}
}

func (e *FileElector) Acquire(_ context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.lockFile != nil {
		return true, nil // Already hold the lock
	}

	if err := os.MkdirAll(e.dir, 0755); err != nil {
		return false, fmt.Errorf("create lock dir: %w", err)
	}

	lockPath := filepath.Join(e.dir, "."+e.lockName+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			if isLockStale(lockPath) {
				slog.Warn("removing stale lock file", "path", lockPath)
				os.Remove(lockPath)
				f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
				if err != nil {
					return false, nil // Another instance grabbed it
				}
			} else {
				return false, nil // Another instance holds the lock
			}
		} else {
			return false, fmt.Errorf("open lock file: %w", err)
		}
	}

	fmt.Fprintf(f, "pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	e.lockFile = f
	return true, nil
}

func (e *FileElector) Renew(_ context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.lockFile == nil {
		return false, nil
	}
	// File lock is held as long as the process lives; just verify the file exists
	_, err := os.Stat(e.lockFile.Name())
	return err == nil, nil
}

func (e *FileElector) Release(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.lockFile == nil {
		return nil
	}

	name := e.lockFile.Name()
	e.lockFile.Close()
	os.Remove(name)
	e.lockFile = nil
	return nil
}

// isLockStale reads the PID from a lock file and checks if the process is alive.
func isLockStale(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return true
	}
	line := strings.TrimSpace(string(data))
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true
	}
	pidStr := strings.TrimPrefix(parts[0], "pid=")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	err = proc.Signal(syscall.Signal(0))
	return err != nil
}

// DBElector implements Elector using database advisory locking.
// Works with both SQLite and PostgreSQL.
type DBElector struct {
	db       *sql.DB
	lockName string
	holderID string
	ttl      time.Duration
	mu       sync.Mutex
	held     bool
}

// NewDBElector creates a database-based elector.
// ttl is the lock expiry time; renew at ttl/2 to prevent expiry.
func NewDBElector(db *sql.DB, lockName string, ttl time.Duration) *DBElector {
	hostname, _ := os.Hostname()
	holderID := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	return &DBElector{
		db:       db,
		lockName: lockName,
		holderID: holderID,
		ttl:      ttl,
	}
}

func (e *DBElector) Acquire(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().UTC()
	expiresAt := now.Add(e.ttl)

	// Try to insert a new lock or take over an expired one.
	// This is portable across SQLite and PostgreSQL.
	result, err := e.db.ExecContext(ctx,
		`INSERT INTO leader_locks (lock_name, holder_id, acquired_at, expires_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (lock_name) DO UPDATE
		 SET holder_id = excluded.holder_id,
		     acquired_at = excluded.acquired_at,
		     expires_at = excluded.expires_at
		 WHERE leader_locks.expires_at < ? OR leader_locks.holder_id = ?`,
		e.lockName, e.holderID, now, expiresAt, now, e.holderID,
	)
	if err != nil {
		return false, fmt.Errorf("acquire lock: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check rows affected: %w", err)
	}

	e.held = rows > 0
	return e.held, nil
}

func (e *DBElector) Renew(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.held {
		return false, nil
	}

	now := time.Now().UTC()
	expiresAt := now.Add(e.ttl)

	result, err := e.db.ExecContext(ctx,
		`UPDATE leader_locks
		 SET expires_at = ?
		 WHERE lock_name = ? AND holder_id = ?`,
		expiresAt, e.lockName, e.holderID,
	)
	if err != nil {
		return false, fmt.Errorf("renew lock: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check rows affected: %w", err)
	}

	if rows == 0 {
		e.held = false
		return false, nil
	}
	return true, nil
}

func (e *DBElector) Release(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.held {
		return nil
	}

	_, err := e.db.ExecContext(ctx,
		`DELETE FROM leader_locks WHERE lock_name = ? AND holder_id = ?`,
		e.lockName, e.holderID,
	)
	e.held = false
	return err
}

// RunWithLeadership is a helper that acquires leadership and starts a
// background goroutine to renew it at ttl/2 intervals. The cancel function
// should be called when done to stop renewal.
func RunWithLeadership(ctx context.Context, elector Elector, ttl time.Duration) (acquired bool, cancel func(), err error) {
	acquired, err = elector.Acquire(ctx)
	if err != nil || !acquired {
		return acquired, func() {}, err
	}

	ctx2, cancelFn := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(ttl / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ctx2.Done():
				elector.Release(context.Background())
				return
			case <-ticker.C:
				ok, err := elector.Renew(ctx2)
				if err != nil || !ok {
					slog.Warn("lost leadership", "error", err)
					return
				}
			}
		}
	}()

	return true, cancelFn, nil
}
