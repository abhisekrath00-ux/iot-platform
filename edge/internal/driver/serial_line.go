package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// serialLine reads text frames from a microcontroller UART / USB-CDC port
// (STM32, Arduino, ESP32, nRF, any firmware that prints readings). Three
// line formats are auto-detected per line:
//
//	{"temp":23.4,"hum":51}     JSON object, point.key = field name
//	temp=23.4,hum=51           key=value pairs (comma/space/semicolon)
//	23.4,51,1013               CSV, point.key = zero-based column index
//
// Poll drains the port and returns the latest value for every configured
// point seen since the previous poll, so firmware can stream at its own rate.
type serialLine struct {
	port serial.Port
	dev  config.Device
	rd   *bufio.Reader
}

func newSerialLine(port serial.Port, dev config.Device) *serialLine {
	port.SetReadTimeout(200 * time.Millisecond)
	return &serialLine{port: port, dev: dev, rd: bufio.NewReaderSize(port, 4096)}
}

// parseLine returns field values by key for one text line.
func parseLine(line string) map[string]float64 {
	line = strings.TrimSpace(line)
	out := map[string]float64{}
	if line == "" {
		return out
	}
	if strings.HasPrefix(line, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			for k, v := range m {
				switch x := v.(type) {
				case float64:
					out[k] = x
				case bool:
					if x {
						out[k] = 1
					} else {
						out[k] = 0
					}
				}
			}
		}
		return out
	}
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' })
	for i, f := range fields {
		if k, v, ok := strings.Cut(f, "="); ok {
			if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				out[strings.TrimSpace(k)] = n
			}
			continue
		}
		if n, err := strconv.ParseFloat(f, 64); err == nil {
			out[strconv.Itoa(i)] = n
		}
	}
	return out
}

func (s *serialLine) Poll(ctx context.Context) ([]Reading, error) {
	latest := map[string]float64{}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := s.rd.ReadString('\n')
		for k, v := range parseLine(line) {
			latest[k] = v
		}
		if err != nil || line == "" {
			break
		}
	}
	var out []Reading
	for _, p := range s.dev.Points {
		key := p.Key
		if key == "" {
			key = p.ID
		}
		v, ok := latest[key]
		if !ok {
			continue
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v *= scale
		if v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: s.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no frames matching configured points within poll window")
	}
	return out, nil
}

func (s *serialLine) Close() error { return s.port.Close() }
