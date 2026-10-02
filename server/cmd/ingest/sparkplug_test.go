package main

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/sparkplug"
)

func spVar(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func spMetric(name string, alias uint64, ts uint64, f float32) []byte {
	var m []byte
	if name != "" {
		m = append(append(spVar(1<<3|2), spVar(uint64(len(name)))...), name...)
	}
	if alias != 0 {
		m = append(append(m, spVar(2<<3)...), spVar(alias)...)
	}
	m = append(append(m, spVar(3<<3)...), spVar(ts)...)
	m = append(append(m, spVar(4<<3)...), spVar(9)...)
	fb := make([]byte, 4)
	binary.LittleEndian.PutUint32(fb, math.Float32bits(f))
	m = append(append(m, spVar(12<<3|5)...), fb...)
	return append(append(spVar(2<<3|2), spVar(uint64(len(m)))...), m...)
}

func TestSparkplugReadings(t *testing.T) {
	now := time.UnixMilli(1700000000000).Add(time.Minute)
	al := &sparkplug.Aliases{}
	birth := append(spMetric("temp", 1, 1700000000000, 20), spMetric("rpm", 2, 1700000000000, 1000)...)
	dev, rs, err := sparkplugReadings("spBv1.0/plant/DBIRTH/edge1/pump3", birth, al, now)
	if err != nil || dev != "edge1:pump3" || len(rs) != 2 {
		t.Fatalf("birth: %v %s %v", err, dev, rs)
	}
	// alias-only DATA resolves through the birth table
	_, rs, err = sparkplugReadings("spBv1.0/plant/DDATA/edge1/pump3", spMetric("", 2, 1700000030000, 1500), al, now)
	if err != nil || len(rs) != 1 || rs[0].PointID != "rpm" || rs[0].Value != 1500 || !rs[0].At.Equal(time.UnixMilli(1700000030000).UTC()) {
		t.Fatalf("data: %v %v", err, rs)
	}
	// deterministic event id: the same message twice produces the same id (idempotent insert)
	_, rs2, _ := sparkplugReadings("spBv1.0/plant/DDATA/edge1/pump3", spMetric("", 2, 1700000030000, 1500), al, now)
	if rs2[0].EventID != rs[0].EventID {
		t.Fatal("event id not deterministic")
	}
	// node-level metrics map to the bare edge node id
	dev, _, _ = sparkplugReadings("spBv1.0/plant/NDATA/edge1", spMetric("cpu", 0, 1700000000000, 5), al, now)
	if dev != "edge1" {
		t.Fatalf("node dev %q", dev)
	}
	// implausible timestamps and bad input are dropped / rejected
	_, rs, _ = sparkplugReadings("spBv1.0/plant/NDATA/edge1", spMetric("old", 0, 1000, 1), al, now)
	if len(rs) != 0 {
		t.Fatal("ancient timestamp accepted")
	}
	_, rs, _ = sparkplugReadings("spBv1.0/plant/NDATA/edge1", spMetric("future", 0, uint64(now.Add(time.Hour).UnixMilli()), 1), al, now)
	if len(rs) != 0 {
		t.Fatal("future timestamp accepted")
	}
	if _, _, err := sparkplugReadings("t/x/g/y/telemetry", birth, al, now); err == nil {
		t.Fatal("non-sparkplug topic accepted")
	}
	if _, _, err := sparkplugReadings("spBv1.0/plant/NDATA/edge1", []byte{0xFF, 0xFF}, al, now); err == nil {
		t.Fatal("garbage payload accepted")
	}
}
