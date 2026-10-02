// Package secrets stores tenant secrets encrypted at rest. The master key
// comes from SECRETS_KEY (base64, 32 bytes) and is never written to the
// database. Each value is sealed with AES-256-GCM using the tenant and name as
// additional data, so a ciphertext copied to another row or tenant fails to
// open. Values are write-only through the API; only server-side connectors
// call Get.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNoKey    = errors.New("SECRETS_KEY is not configured")
	ErrNotFound = errors.New("secret not found")
	ErrBadName  = errors.New("name must be 1-64 chars of a-z 0-9 _ -")
	ErrBadValue = errors.New("value must be 1 to 8192 bytes")
)

var nameRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// KeyFromEnv decodes a base64 32-byte key. An empty string means "not configured".
func KeyFromEnv(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	k, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(k) != 32 {
		return nil, errors.New("SECRETS_KEY must be base64 of exactly 32 bytes")
	}
	return k, nil
}

type Store struct {
	Pool *pgxpool.Pool
	Key  []byte // nil disables the store
}

func aad(tenant, name string) []byte { return []byte(tenant + "\x00" + name) }

// Seal encrypts value; the output is nonce || ciphertext.
func Seal(key []byte, tenant, name string, value []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, value, aad(tenant, name)), nil
}

func Open(key []byte, tenant, name string, blob []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < g.NonceSize() {
		return nil, errors.New("secret ciphertext too short")
	}
	return g.Open(nil, blob[:g.NonceSize()], blob[g.NonceSize():], aad(tenant, name))
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrNoKey
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func (s *Store) Put(ctx context.Context, tenant, name, by string, value []byte) error {
	if !nameRe.MatchString(name) {
		return ErrBadName
	}
	if len(value) == 0 || len(value) > 8192 {
		return ErrBadValue
	}
	blob, err := Seal(s.Key, tenant, name, value)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO secrets(tenant_id,name,ciphertext,created_by) VALUES($1,$2,$3,$4)
		 ON CONFLICT (tenant_id,name) DO UPDATE SET ciphertext=$3, created_by=$4, updated_at=now()`,
		tenant, name, blob, by)
	return err
}

func (s *Store) Get(ctx context.Context, tenant, name string) ([]byte, error) {
	var blob []byte
	err := s.Pool.QueryRow(ctx, `SELECT ciphertext FROM secrets WHERE tenant_id=$1 AND name=$2`, tenant, name).Scan(&blob)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v, err := Open(s.Key, tenant, name, blob)
	if err != nil {
		return nil, fmt.Errorf("secret %q cannot be decrypted (wrong SECRETS_KEY?)", name)
	}
	return v, nil
}

type Info struct {
	Name      string `json:"name"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

// List returns names and metadata only, never values.
func (s *Store) List(ctx context.Context, tenant string) ([]Info, error) {
	rows, err := s.Pool.Query(ctx, `SELECT name, created_by, to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM secrets WHERE tenant_id=$1 ORDER BY name`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Info{}
	for rows.Next() {
		var i Info
		if err := rows.Scan(&i.Name, &i.UpdatedBy, &i.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, tenant, name string) (bool, error) {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM secrets WHERE tenant_id=$1 AND name=$2`, tenant, name)
	return ct.RowsAffected() > 0, err
}
