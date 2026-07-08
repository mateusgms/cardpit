package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zeebo/xxh3"
)

const (
	tmpSuffix = ".cardpit-tmp"
	bufSize   = 4 << 20 // 4 MiB, per PRD
)

// copier copies single files with streamed hashing and atomic finalization.
// One copier is shared by all jobs: renameMu serializes final-name selection
// so concurrent jobs can never race the same destination name.
type copier struct {
	retryDelays []time.Duration
	renameMu    sync.Mutex
	bufPool     sync.Pool
	tmpSeq      atomic.Int64
}

func newCopier() *copier {
	return &copier{
		retryDelays: []time.Duration{time.Second, 4 * time.Second, 10 * time.Second},
		bufPool: sync.Pool{New: func() any {
			b := make([]byte, bufSize)
			return &b
		}},
	}
}

// copyOne copies entry.src into dstDir. It returns the final path and the
// XXH3-64 hash ("%016x"). Failed attempts are retried (retryDelays), except
// on context cancellation or a full destination disk.
func (c *copier) copyOne(ctx context.Context, entry fileEntry, dstDir string, paranoid bool) (string, string, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		dst, hash, err := c.copyOnce(ctx, entry, dstDir, paranoid)
		if err == nil {
			return dst, hash, nil
		}
		lastErr = err
		if ctx.Err() != nil || isDiskFull(err) || attempt >= len(c.retryDelays) {
			return "", "", lastErr
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(c.retryDelays[attempt]):
		}
	}
}

func (c *copier) copyOnce(ctx context.Context, entry fileEntry, dstDir string, paranoid bool) (finalPath, hash string, err error) {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", "", err
	}

	src, err := os.Open(entry.src)
	if err != nil {
		return "", "", err
	}
	defer src.Close()

	// The sequence keeps tmp paths unique when concurrent jobs land files
	// with the same name in the same date folder.
	tmpPath := filepath.Join(dstDir,
		fmt.Sprintf("%s.%d%s", entry.name, c.tmpSeq.Add(1), tmpSuffix))
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", "", err
	}
	// Any failure below must leave no tmp file behind.
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}

	h := xxh3.New()
	bufp := c.bufPool.Get().(*[]byte)
	_, err = copyBufferCtx(ctx, tmp, io.TeeReader(src, h), *bufp)
	c.bufPool.Put(bufp)
	if err != nil {
		cleanup()
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", "", err
	}

	sum := fmt.Sprintf("%016x", h.Sum64())
	if entry.knownHash != "" && entry.knownHash != sum {
		// The source changed between dedup planning and the copy — treat as
		// an I/O anomaly and let the retry loop take another full pass.
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("hash mismatch vs planning read: %s != %s", sum, entry.knownHash)
	}

	if paranoid {
		bufp := c.bufPool.Get().(*[]byte)
		reread, err := hashFile(ctx, tmpPath, *bufp)
		c.bufPool.Put(bufp)
		if err != nil {
			os.Remove(tmpPath)
			return "", "", fmt.Errorf("paranoid re-read: %w", err)
		}
		if reread != sum {
			os.Remove(tmpPath)
			return "", "", fmt.Errorf("paranoid verify failed: destination %s != source %s", reread, sum)
		}
	}

	// Preserve the capture timestamp on the destination file.
	os.Chtimes(tmpPath, entry.mtime, entry.mtime)

	// Serialize name selection + rename so two jobs writing files with the
	// same name into the same date folder cannot clobber each other.
	c.renameMu.Lock()
	defer c.renameMu.Unlock()
	finalPath = pickFreeName(dstDir, entry.name)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", "", err
	}
	return finalPath, sum, nil
}

// pickFreeName returns dir/name, or "name (N).ext" if taken — an existing
// file is never overwritten.
func pickFreeName(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Lstat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
	}
}

// hashFile computes the XXH3-64 of a file ("%016x").
func hashFile(ctx context.Context, path string, buf []byte) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := xxh3.New()
	if _, err := copyBufferCtx(ctx, h, f, buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// copyBufferCtx is io.CopyBuffer with cancellation checked between chunks,
// so a yanked-card job stops within one buffer's worth of work.
func copyBufferCtx(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			written += int64(w)
			if werr != nil {
				return written, werr
			}
			if w < n {
				return written, io.ErrShortWrite
			}
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}

func isDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || isDiskFullOS(err)
}
