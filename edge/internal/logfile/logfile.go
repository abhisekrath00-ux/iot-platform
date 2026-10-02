// Package logfile is a small size-rotating log writer. Windows services have no
// journald, so the agent writes its own file; Linux keeps using the journal
// unless -log-file is given.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Writer appends to Path and rotates to Path.1 ... Path.<Keep> when the file
// would pass MaxBytes. Safe for concurrent use.
type Writer struct {
	Path     string
	MaxBytes int64
	Keep     int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// Open creates the directory (0750) and opens the file (0640).
func Open(path string, maxBytes int64, keep int) (*Writer, error) {
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	if keep < 1 {
		keep = 3
	}
	w := &Writer{Path: path, MaxBytes: maxBytes, Keep: keep}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

func (w *Writer) rotate() error {
	w.f.Close()
	for i := w.Keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.Path, i), fmt.Sprintf("%s.%d", w.Path, i+1))
	}
	if err := os.Rename(w.Path, w.Path+".1"); err != nil {
		// Windows refuses to rename a file another process holds open; keep
		// appending rather than lose log lines.
		return w.open()
	}
	return w.open()
}

// Write implements io.Writer.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > w.MaxBytes && w.size > 0 {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
