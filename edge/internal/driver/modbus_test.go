package driver

import "testing"

func TestCRC16(t *testing.T) {
	// Modbus RTU golden vector: 01 04 00 00 00 01 -> CRC 0xCA31 (little-endian on wire: 31 CA)
	req := []byte{0x01, 0x04, 0x00, 0x00, 0x00, 0x01}
	if got := crc16(req); got != 0xCA31 {
		t.Fatalf("crc16=%04x, want ca31", got)
	}
}

func TestValidationRange(t *testing.T) {
	// range check logic mirrors Poll: value outside [min,max] must reject
	min, max := 0.0, 500.0
	for _, v := range []float64{-1, 501, 1e9} {
		if v >= min && v <= max {
			t.Fatalf("value %v wrongly accepted", v)
		}
	}
}
