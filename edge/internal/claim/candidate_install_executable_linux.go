//go:build linux

package claim

import (
	"fmt"
	"io"
	"os"
)

func candidateReadOwnExecutable() ([]byte, error) {
	// Linux procfs resolves this to the image actually executing, even if its
	// original pathname has since been replaced. Never resolve another PID.
	f, e := os.Open("/proc/self/exe")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 64<<20 {
		return nil, fmt.Errorf("running executable unavailable or too large")
	}
	b, e := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if e != nil || len(b) > 64<<20 {
		return nil, fmt.Errorf("running executable unreadable")
	}
	return b, nil
}
