package main

import (
	"context"
	"testing"
)

func TestRunScanRequestValidation(t *testing.T) {
	for name, p := range map[string]string{
		"junk":       `nope`,
		"no id":      `{"kind":"lan","cidr":"192.168.1.0/24"}`,
		"unknown":    `{"scan_id":"s","kind":"write-everything"}`,
		"public lan": `{"scan_id":"s","kind":"lan","cidr":"8.8.8.0/24"}`,
		"bad range":  `{"scan_id":"s","kind":"modbus-rtu","from":0,"to":5}`,
		"bad bacnet": `{"scan_id":"s","kind":"bacnet","broadcast":"nope"}`,
	} {
		if r := runScanRequest(context.Background(), []byte(p), nil); r.OK || r.Error == "" {
			t.Errorf("%s accepted: %+v", name, r)
		}
	}
}

func TestOneScanAtATime(t *testing.T) {
	scanMu.Lock()
	defer scanMu.Unlock()
	if r := runScanRequest(context.Background(), []byte(`{"scan_id":"s","kind":"lan","cidr":"192.168.1.0/24"}`), nil); r.Error != "another scan is already running on this gateway" {
		t.Errorf("%+v", r)
	}
}
