package explorer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
	"github.com/google/uuid"
)

// Registry borrows owned drivers. Add does not open anything. Remove waits for
// an outstanding read before returning, so the owner may then close the driver.
// IDs name one driver lifetime, not a device name or an address for retargeting.
var ErrConnectionExpired = errors.New("connection expired; refresh connections and reconnect explicitly")

type Registry struct {
	mu               sync.RWMutex
	entries          map[string]entry
	temporaryEnabled bool
	factory          func(config.Device) (driver.Driver, error)
}
type entry struct {
	info   Connection
	reader driver.RangeReader
	temp   *temporary
}

func (r *Registry) Add(info Connection, reader driver.RangeReader) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]entry)
	}
	info.ID = uuid.NewString()
	r.entries[info.ID] = entry{info: info, reader: reader}
	return func() { r.mu.Lock(); defer r.mu.Unlock(); delete(r.entries, info.ID) }
}
func (r *Registry) Connections() []Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Connection, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *Registry) Read(ctx context.Context, req Range) ([]uint16, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if !r.mu.TryLock() {
		return nil, fmt.Errorf("device list is changing; refresh connections")
	}
	defer r.mu.Unlock()
	e, ok := r.entries[req.Connection]
	if !ok || e.reader == nil {
		return nil, ErrConnectionExpired
	}
	if e.temp != nil {
		now := time.Now()
		if expired(e.temp, now) {
			delete(r.entries, req.Connection)
			e.temp.close()
			return nil, ErrConnectionExpired
		}
		e.temp.last = now
	}
	return e.reader.ReadRange(ctx, req.Function, req.Offset, req.Count)
}
