// Package tail follows a log file like `tail -F`, surviving truncation,
// rotation (inode change), and brief disappearance.
package tail

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// Options tunes the tailer.
type Options struct {
	// PollInterval is how often we check for new data after hitting EOF.
	PollInterval time.Duration
	// StartAtEnd controls the initial seek: true = skip existing content.
	StartAtEnd bool
	// ReopenCheckEvery throttles inode checks.
	ReopenCheckEvery time.Duration
}

// Follow opens path and emits each line on the returned channel.
// The channel is closed only when ctx is cancelled.
func Follow(ctx context.Context, path string, opts Options) (<-chan string, <-chan error) {
	if opts.PollInterval == 0 {
		opts.PollInterval = 200 * time.Millisecond
	}
	if opts.ReopenCheckEvery == 0 {
		opts.ReopenCheckEvery = 2 * time.Second
	}

	lines := make(chan string, 128)
	errs := make(chan error, 1)

	go func() {
		defer close(lines)
		defer close(errs)

		var (
			f        *os.File
			reader   *bufio.Reader
			curIno   uint64
			lastStat time.Time
		)

		openFile := func() error {
			if f != nil {
				f.Close()
			}
			var err error
			f, err = os.Open(path)
			if err != nil {
				return err
			}
			if opts.StartAtEnd {
				if _, err := f.Seek(0, io.SeekEnd); err != nil {
					return err
				}
				// Only seek-to-end on very first open; future reopens start at 0.
				opts.StartAtEnd = false
			}
			reader = bufio.NewReader(f)
			fi, err := f.Stat()
			if err == nil {
				curIno = inode(fi)
			}
			lastStat = time.Now()
			return nil
		}

		if err := openFile(); err != nil {
			errs <- err
			return
		}

		for {
			if err := ctx.Err(); err != nil {
				return
			}

			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				if line[len(line)-1] == '\n' {
					line = line[:len(line)-1]
				}
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
				continue
			}

			if err != nil && !errors.Is(err, io.EOF) {
				errs <- err
				return
			}

			// EOF — wait a bit, then check for rotation.
			select {
			case <-ctx.Done():
				return
			case <-time.After(opts.PollInterval):
			}

			if time.Since(lastStat) >= opts.ReopenCheckEvery {
				lastStat = time.Now()
				fi, err := os.Stat(path)
				if err == nil && inode(fi) != curIno {
					_ = openFile()
				}
			}
		}
	}()

	return lines, errs
}
