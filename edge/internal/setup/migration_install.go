package setup

import (
	"context"
	"encoding/json"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type migrationJournal struct {
	Previous  map[string][]byte `json:"previous"`
	Committed bool              `json:"committed"`
}

var migrationNames = []string{"identity.crt", "ca.pem", "device-ca.pem"}

func migrationWrite(path string, b []byte) error {
	f, e := os.OpenFile(path+".next", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(path+".next", path)
}
func MigrationPending(dir string) bool {
	_, e := os.Stat(filepath.Join(dir, "migration-install.json"))
	return !os.IsNotExist(e)
}
func recoverMigration(dir string) error {
	path := filepath.Join(dir, "migration-install.json")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var j migrationJournal
	if json.Unmarshal(b, &j) != nil || len(j.Previous) != len(migrationNames) {
		return fmt.Errorf("identity recovery journal invalid; keep service stopped")
	}
	for _, name := range migrationNames {
		if _, ok := j.Previous[name]; !ok {
			return fmt.Errorf("identity recovery journal incomplete")
		}
	}
	if !j.Committed {
		for _, name := range migrationNames {
			path := filepath.Join(dir, name)
			if j.Previous[name] == nil {
				if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
					return fmt.Errorf("identity recovery could not restore %s", name)
				}
			} else if e = migrationWrite(path, j.Previous[name]); e != nil {
				return fmt.Errorf("identity recovery could not restore %s", name)
			}
		}
	}
	return os.Remove(path)
}

// RecoverMigration is explicit recovery with a process-scoped OS lock. The
// caller must stop the service. Key and identity.json are never journaled.
func RecoverMigration(dir string) error {
	unlock, e := migrationLock(dir)
	if e != nil {
		return e
	}
	defer unlock()
	return recoverMigration(dir)
}

// InstallMigration validates both CAs separately, checks an authenticated MQTT
// connection before any certificate file changes, then commits a rollback journal.
// Only a caller-supplied diagnostic proof may publish. No subscription, device
// command, scan or source start occurs in this check.
func InstallMigration(ctx context.Context, dir, serial, gateway string, b MigrationBundle, prove func(context.Context, mqtt.Client) error) error {
	if prove == nil {
		return fmt.Errorf("fresh server-observed broker proof required; certificate files unchanged")
	}
	unlock, e := migrationLock(dir)
	if e != nil {
		return e
	}
	defer unlock()
	if e = recoverMigration(dir); e != nil {
		return e
	}
	tc, e := ValidateMigrationBundle(dir, serial, b)
	if e != nil {
		return e
	}
	timeout := 8 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return ctx.Err()
	}
	opts := mqtt.NewClientOptions().AddBroker("ssl://" + net.JoinHostPort(b.Host, strconv.Itoa(b.Port))).SetTLSConfig(tc).SetClientID("setup-check-" + gateway).SetConnectTimeout(timeout).SetAutoReconnect(false)
	client := mqtt.NewClient(opts)
	token := client.Connect()
	select {
	case <-token.Done():
		if token.Error() != nil {
			return fmt.Errorf("secure broker authentication failed; certificate files unchanged")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	defer client.Disconnect(50)
	if e = prove(ctx, client); e != nil {
		return e
	}
	j := migrationJournal{Previous: map[string][]byte{}}
	for _, name := range migrationNames {
		old, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil && !os.IsNotExist(e) {
			return fmt.Errorf("cannot back up existing %s", name)
		}
		j.Previous[name] = old
	}
	raw, _ := json.Marshal(j)
	journal := filepath.Join(dir, "migration-install.json")
	if e = migrationWrite(journal, raw); e != nil {
		return e
	}
	rollback := func(cause error) error {
		if e := recoverMigration(dir); e != nil {
			return fmt.Errorf("%v; rollback incomplete: %v; keep service stopped", cause, e)
		}
		return cause
	}
	for name, contents := range map[string]string{"identity.crt": b.ClientCert, "ca.pem": b.BrokerCA, "device-ca.pem": b.DeviceCA} {
		if e = migrationWrite(filepath.Join(dir, name), []byte(contents)); e != nil {
			return rollback(e)
		}
	}
	if e = ValidateIdentity(dir, serial); e != nil {
		return rollback(e)
	}
	j.Committed = true
	raw, _ = json.Marshal(j)
	if e = migrationWrite(journal, raw); e != nil {
		return rollback(e)
	}
	if e = os.Remove(journal); e != nil {
		return fmt.Errorf("certificate saved, committed journal cleanup pending: %w", e)
	}
	return nil
}
