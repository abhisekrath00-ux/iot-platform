//go:build windows

package claim

import "fmt"

type candidateStore struct{}

func candidateOpenStore(string, bool) (*candidateStore, error) {
	return nil, fmt.Errorf("candidate Windows pinned private store unavailable")
}
func (*candidateStore) Close() error { return nil }
func (*candidateStore) Read(string) ([]byte, error) {
	return nil, fmt.Errorf("candidate Windows pinned private reads unavailable")
}
func (*candidateStore) Write(string, []byte) error {
	return fmt.Errorf("candidate Windows pinned private writes unavailable")
}
