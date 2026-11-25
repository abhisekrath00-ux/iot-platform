//go:build linux

package claim

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func destinationTestFiles() map[string][]byte {
	return map[string][]byte{"identity.key": []byte("synthetic-key"), "identity.json": []byte(`{"gateway_id":"synthetic"}`), "identity.crt": []byte("synthetic-cert"), "device-ca.pem": []byte("synthetic-ca")}
}
func TestCandidateDestinationChild(t *testing.T) {
	if os.Getenv("HEX_DESTINATION_CHILD") != "1" {
		t.Skip("child only")
	}
	if e := candidateAcceptDestination(os.Getenv("HEX_DESTINATION_PARENT"), "accepted", strings.Repeat("a", 64), destinationTestFiles()); e != nil {
		os.Exit(23)
	}
}
func TestCandidateDestinationTransaction(t *testing.T) {
	base := t.TempDir()
	if e := os.Chmod(base, 0700); e != nil {
		t.Fatal(e)
	}
	parent := filepath.Join(base, "private")
	if e := os.Mkdir(parent, 0700); e != nil {
		t.Fatal(e)
	}
	files := destinationTestFiles()
	attempt := strings.Repeat("a", 64)
	if e := candidateAcceptDestination(parent, "accepted", attempt, files); e != nil {
		t.Fatal(e)
	}
	if e := candidateAcceptDestination(parent, "accepted", attempt, files); e != nil {
		t.Fatal("exact reconcile", e)
	}
	before, _ := os.ReadFile(filepath.Join(parent, "accepted/identity.key"))
	if e := candidateAcceptDestination(parent, "accepted", strings.Repeat("b", 64), files); e == nil {
		t.Fatal("different attempt accepted")
	}
	after, _ := os.ReadFile(filepath.Join(parent, "accepted/identity.key"))
	if !bytes.Equal(before, after) {
		t.Fatal("changed retained identity")
	}
	if e := os.WriteFile(filepath.Join(parent, "accepted/unknown"), []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := candidateAcceptDestination(parent, "accepted", attempt, files); e == nil {
		t.Fatal("extra destination file accepted")
	}
}
func TestCandidateDestinationProcessCuts(t *testing.T) {
	if os.Getenv("HEX_DESTINATION_FAULT_TEST") != "1" {
		t.Skip("requires separate fault-tag acceptance build")
	}
	for _, stage := range []string{"destination_after_intent", "destination_after_directory", "destination_after_identity.json", "destination_after_identity.key", "destination_after_identity.crt", "destination_after_device-ca.pem", "destination_before_promotion", "destination_after_promotion", "destination_after_receipt", "partial_destination_intent", "partial_destination_receipt"} {
		t.Run(stage, func(t *testing.T) {
			base := t.TempDir()
			if e := os.Chmod(base, 0700); e != nil {
				t.Fatal(e)
			}
			parent := filepath.Join(base, "private")
			if e := os.Mkdir(parent, 0700); e != nil {
				t.Fatal(e)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestCandidateDestinationChild$")
			child.Env = append(os.Environ(), "HEX_DESTINATION_CHILD=1", "HEX_DESTINATION_PARENT="+parent, "HEX_CANDIDATE_TEST_CRASH="+stage)
			if e := child.Run(); e == nil {
				t.Fatal("no fault exit")
			} else if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 86 {
				t.Fatal("unexpected exit", e)
			}
			snapshot := map[string][]byte{}
			filepath.WalkDir(parent, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if !d.IsDir() {
					snapshot[p], _ = os.ReadFile(p)
				}
				return nil
			})
			e := candidateAcceptDestination(parent, "accepted", strings.Repeat("a", 64), destinationTestFiles())
			complete := !strings.HasPrefix(stage, "partial_destination_")
			if (e == nil) != complete {
				t.Fatal("unexpected reconciliation", e)
			}
			for p, want := range snapshot {
				got, readErr := os.ReadFile(p)
				if os.IsNotExist(readErr) && strings.Contains(p, "accepted.pending/") && complete {
					p = strings.Replace(p, "accepted.pending/", "accepted/", 1)
					got, readErr = os.ReadFile(p)
				}
				if readErr != nil || !bytes.Equal(got, want) {
					t.Fatal("retained prefix changed", p, readErr)
				}
			}
			if complete {
				raw, e := os.ReadFile(filepath.Join(parent, "accepted.complete.json"))
				var record candidateDestinationIntent
				if e != nil || json.Unmarshal(raw, &record) != nil || record.Attempt != strings.Repeat("a", 64) {
					t.Fatal("missing receipt", e)
				}
			}
		})
	}
}
