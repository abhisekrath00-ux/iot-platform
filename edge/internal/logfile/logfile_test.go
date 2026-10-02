package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatesAndKeepsLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "a.log")
	w, err := Open(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	line := strings.Repeat("x", 40) + "\n" // 41 bytes
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{p, p + ".1", p + ".2"} {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
		if st.Size() > 100 {
			t.Fatalf("%s is %d bytes, over the limit", f, st.Size())
		}
	}
	if _, err := os.Stat(p + ".3"); err == nil {
		t.Fatal("kept more rotated files than Keep")
	}
}

func TestAppendsToExisting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("old\n"), 0o640)
	w, err := Open(p, 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("new\n"))
	w.Close()
	b, _ := os.ReadFile(p)
	if string(b) != "old\nnew\n" {
		t.Fatalf("got %q", b)
	}
}
