package claim

import (
	"bytes"
	"strings"
	"testing"
)

func TestCandidateReviewCommandRejectsMutationAndUnboundedInput(t *testing.T) {
	for _, input := range []string{`{}`, `{"Code":"synthetic"}`, `{"Recover":true}`, `{"Dir":"x"} {}`, strings.Repeat("x", 131073)} {
		var out bytes.Buffer
		if e := RunCandidateReview(strings.NewReader(input), &out); e == nil || out.Len() != 0 {
			t.Fatal("invalid review input accepted")
		}
	}
}
