//go:build !linux

package claim

import (
	"fmt"
	"io"
)

func candidateReviewPreparation(string, io.Writer) error {
	return fmt.Errorf("native preparation review unavailable")
}
