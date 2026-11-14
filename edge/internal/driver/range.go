package driver

import (
	"context"
	"encoding/binary"
	"fmt"
)

// RangeReader exposes only bounded read functions on an already-open driver.
// It never changes the unit, transport or connection settings, and never queues
// behind polling/writing. Busy means skip this sample, not retry the frame.
type RangeReader interface {
	ReadRange(context.Context, int, int, int) ([]uint16, error)
}

func (g *modbusGeneric) ReadRange(ctx context.Context, fn, offset, count int) ([]uint16, error) {
	if fn < 1 || fn > 4 || offset < 0 || offset > 65535 || count < 1 || count > 32 || offset+count > 65536 {
		return nil, fmt.Errorf("range outside read-only diagnostic envelope")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !g.mu.TryLock() {
		return nil, fmt.Errorf("connection busy; sample skipped without queuing")
	}
	defer g.mu.Unlock()
	if g.closed {
		return nil, fmt.Errorf("connection was closed; refresh devices")
	}
	if g.read == nil {
		g.read = g.readRegisters
	}
	data, err := g.read(fn, offset, count)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	need := count * 2
	if fn <= 2 {
		need = (count + 7) / 8
	}
	if len(data) != need {
		return nil, fmt.Errorf("incomplete or oversized range response")
	}
	words := make([]uint16, count)
	for i := range words {
		if fn <= 2 {
			words[i] = uint16((data[i/8] >> uint(i%8)) & 1)
		} else {
			words[i] = binary.BigEndian.Uint16(data[i*2:])
		}
	}
	return words, nil
}
