package claim

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCandidateInstallTargetsRefuseOverlapAndNoncanonical(t *testing.T) {
	dir := "/private/staging/attempt"
	valid := candidateInstallTargets{Binary: "/selected/bin/edge-agent", Config: "/selected/etc/config.yaml", State: "/selected/state", Unit: "/selected/unit/service", Destination: "/selected/identity"}
	if !candidateValidInstallTargets(dir, valid) {
		t.Fatal("distinct canonical targets refused")
	}
	for _, kind := range []string{"relative", "unclean", "root", "same", "inside-state", "contains-stage", "nested-stage", "missing"} {
		v := valid
		switch kind {
		case "relative":
			v.Binary = "relative"
		case "unclean":
			v.Config = "/selected/etc/../config"
		case "root":
			v.Unit = "/"
		case "same":
			v.Unit = v.Binary
		case "inside-state":
			v.Config = filepath.Join(v.State, "config.yaml")
		case "contains-stage":
			v.State = "/private"
		case "nested-stage":
			v.State = filepath.Join(dir, "installed")
		case "missing":
			v.Unit = ""
		}
		if candidateValidInstallTargets(dir, v) {
			t.Fatal("unsafe target accepted", kind)
		}
	}
}

func TestCandidateInstallReviewStrictNestedTargetsAndInput(t *testing.T) {
	for _, raw := range []string{`{}`, `{"Code":"secret"}`, `{"Targets":{"Binary":"a","Binary":"b"}}`, `{"Targets":{"Unknown":"a"}}`, `{"Targets":null}`, `{} {}`, strings.Repeat("x", 131073)} {
		var out bytes.Buffer
		if e := RunCandidateInstallReview(strings.NewReader(raw), &out); e == nil || out.Len() != 0 {
			t.Fatal("invalid installer review accepted")
		}
	}
	for _, nested := range []string{`{"Binary":"a","Binary":"b"}`, `{"Unknown":"a"}`, `null`, `[]`} {
		raw := []byte(`{"Targets":` + nested + `}`)
		if candidateStrictInstallTargets(raw) {
			t.Fatal("nested ambiguity accepted", nested)
		}
	}
	raw, _ := json.Marshal(map[string]any{"Targets": candidateInstallTargets{Binary: "/a", Config: "/b", State: "/c", Unit: "/d"}})
	if !candidateStrictInstallTargets(raw) {
		t.Fatal("exact target object refused")
	}
}

func TestCandidateInstallStageRejectsUnboundedAndOperationSwitches(t *testing.T) {
	for _, raw := range []string{`{}`, `{"Recover":true}`, `{"Create":true}`, `{"Targets":{"Binary":"a","Binary":"b"}}`, `{} {}`, strings.Repeat("x", 131073)} {
		var out bytes.Buffer
		if e := RunCandidateInstallStage(context.Background(), strings.NewReader(raw), &out); e == nil || out.Len() != 0 {
			t.Fatal("invalid staging accepted")
		}
	}
}

func TestCandidateInstallSnapshotShapeRefusesDuplicates(t *testing.T) {
	for _, raw := range []string{`{"TargetSnapshot":{"binary":"a","binary":"b"}}`, `{"TargetSnapshot":{"unknown":"a"}}`, `{"TargetSnapshot":null}`} {
		if candidateStrictInstallSnapshot([]byte(raw)) {
			t.Fatal("ambiguous snapshot accepted")
		}
	}
}
