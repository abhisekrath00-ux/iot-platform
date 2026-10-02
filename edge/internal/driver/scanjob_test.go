package driver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.bug.st/serial"
)

func TestSweepFindsSlaveAndRanksProfiles(t *testing.T) {
	open := func(string, *serial.Mode) (serial.Port, error) {
		return &onlyAddr{fakePort: &fakePort{regs: map[int]uint16{0: 230, 1: 50}}, addr: 5}, nil
	}
	req := SweepRequest{Port: "/dev/ttyX", Baud: 9600, DataBits: 8, StopBits: 1, Parity: "none", From: 1, To: 8, TimeoutMs: 100,
		Candidates: []Candidate{
			{ProfileID: "volts", Name: "Volt meter", Points: []CandPoint{{ID: "v", Register: 0, Func: 4, Type: "u16", Scale: 1, Min: 100, Max: 300}, {ID: "hz", Register: 1, Func: 4, Type: "u16", Scale: 1, Min: 45, Max: 65}}},
			{ProfileID: "half", Name: "Half match", Points: []CandPoint{{ID: "a", Register: 0, Func: 4, Type: "u16", Scale: 1, Min: 100, Max: 300}, {ID: "b", Register: 1, Func: 4, Type: "u16", Scale: 1, Min: 1000, Max: 2000}}},
			{ProfileID: "other", Name: "Other", Points: []CandPoint{{ID: "x", Register: 0, Func: 4, Type: "u16", Scale: 1, Min: 5000, Max: 6000}}},
		}}
	res := SweepModbusRTU(context.Background(), open, req)
	if res.Error != "" || len(res.Slaves) != 1 || res.Slaves[0].Address != 5 {
		t.Fatalf("%+v", res)
	}
	m := res.Slaves[0].Matches
	if len(m) != 2 || m[0].ProfileID != "volts" || m[0].Score != 1 || m[1].ProfileID != "half" || m[1].Score != 0.5 {
		t.Fatalf("matches %+v", m)
	}
}

func TestSweepReportsPortOpenFailureAndBadRange(t *testing.T) {
	res := SweepModbusRTU(context.Background(), func(string, *serial.Mode) (serial.Port, error) { return nil, errors.New("busy") },
		SweepRequest{Port: "/dev/ttyX", Baud: 9600, DataBits: 8, StopBits: 1, From: 1, To: 3})
	if !strings.Contains(res.Error, "cannot open") {
		t.Errorf("%+v", res)
	}
	if r := SweepModbusRTU(context.Background(), nil, SweepRequest{From: 0, To: 3}); r.Error == "" {
		t.Error("bad range accepted")
	}
}
