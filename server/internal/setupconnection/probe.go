// Package setupconnection checks one explicit PostgreSQL connection without
// migrating, writing application rows, refreshing credentials or authorizing
// maintenance. The host caller must bind the exact namespace and target.
package setupconnection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Request struct {
	Version   int    `json:"version"`
	Transport string `json:"transport"`
	Host      string `json:"host"`
	Port      uint16 `json:"port"`
	Database  string `json:"database"`
	User      string `json:"user"`
	Password  string `json:"password"`
}
type Result struct {
	Version  int    `json:"version"`
	Status   string `json:"status"`
	Database string `json:"database,omitempty"`
	User     string `json:"user,omitempty"`
	Server   string `json:"server,omitempty"`
	Client   string `json:"client,omitempty"`
}

func Read(input io.Reader) (Request, error) {
	var value Request
	raw, err := io.ReadAll(io.LimitReader(input, 8193))
	if err != nil || len(raw) > 8192 {
		return value, fmt.Errorf("bounded explicit connection request required")
	}
	// Reject duplicate fields before typed decoding. Decoder normally accepts
	// repeated keys, which would let reviewed and executed parameters differ.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return value, fmt.Errorf("explicit connection object required")
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return value, fmt.Errorf("invalid connection request")
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return value, fmt.Errorf("duplicate connection field")
		}
		seen[name] = true
		var field json.RawMessage
		if dec.Decode(&field) != nil {
			return value, fmt.Errorf("invalid connection request")
		}
	}
	if _, err = dec.Token(); err != nil {
		return value, fmt.Errorf("invalid connection request")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return value, fmt.Errorf("one connection request required")
	}
	dec = json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&value) != nil || len(seen) != 7 || (value.Transport != "private_tcp_plain" && value.Transport != "local_socket") || value.Version != 1 || value.Port == 0 || value.User == "" || value.Database == "" || len(value.Password) > 4096 {
		return value, fmt.Errorf("exact explicit connection fields required")
	}
	for _, text := range []string{value.User, value.Database, value.Password} {
		if strings.ContainsRune(text, 0) {
			return value, fmt.Errorf("invalid connection field")
		}
	}
	if value.Transport == "local_socket" {
		if !strings.HasPrefix(value.Host, "/") || path.Clean(value.Host) != value.Host || strings.ContainsAny(value.Host, ",\\\x00") || value.Password != "" {
			return value, fmt.Errorf("one canonical explicit local socket without password required")
		}
		return value, nil
	}
	ip := net.ParseIP(value.Host)
	if ip == nil || ip.To4() == nil || ip.String() != value.Host || !(ip.IsPrivate() || ip.IsLoopback()) {
		return value, fmt.Errorf("explicit private or loopback IPv4 PostgreSQL endpoint required")
	}
	return value, nil
}

// Probe supports explicit private plaintext TCP or an explicit local Unix socket.
// TLS routes need separate trust inputs and must not silently downgrade.
// No SQL text, password, upstream error body, DNS or session is returned.
func Probe(ctx context.Context, request Request) (Result, error) {
	value := Result{Version: 1}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PG") {
			return value, fmt.Errorf("ambient PostgreSQL settings refused")
		}
	}
	if _, err := Read(strings.NewReader(mustRequest(request))); err != nil {
		return value, err
	}
	address := net.JoinHostPort(request.Host, fmt.Sprint(request.Port))
	uri := url.URL{Scheme: "postgres", Host: address, Path: "/" + request.Database, User: url.UserPassword(request.User, request.Password)}
	q := url.Values{"sslmode": {"disable"}, "connect_timeout": {"5"}, "application_name": {"hexthings-connection-proof"}, "passfile": {"/dev/null"}}
	if request.Transport == "local_socket" {
		uri.Host = ""
		q.Set("host", request.Host)
		q.Set("port", fmt.Sprint(request.Port))
	}
	uri.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(uri.String())
	if err != nil {
		return value, fmt.Errorf("connection configuration unavailable")
	}
	// ParseConfig can consult PG* variables, passfiles and service files. Explicit
	// fields win and all fallback/lookup hooks are replaced for this fixed probe.
	cfg.Host = request.Host
	cfg.Port = request.Port
	cfg.Database = request.Database
	cfg.User = request.User
	cfg.Password = request.Password
	cfg.TLSConfig = nil
	cfg.Fallbacks = nil
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{"application_name": "hexthings-connection-proof", "default_transaction_read_only": "on", "statement_timeout": "5000"}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		if host != request.Host {
			return nil, fmt.Errorf("DNS disabled")
		}
		return []string{request.Host}, nil
	}
	timeout, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(timeout, cfg)
	if err != nil {
		value.Status = classify(err)
		return value, nil
	}
	defer conn.Close(context.Background())
	err = conn.QueryRow(timeout, "SELECT current_database(),current_user,coalesce(host(inet_server_addr()),''),coalesce(host(inet_client_addr()),'')").Scan(&value.Database, &value.User, &value.Server, &value.Client)
	if err != nil || value.Database != request.Database || value.User != request.User || (request.Transport == "local_socket" && (value.Server != "" || value.Client != "")) {
		return Result{Version: 1, Status: "unverified"}, nil
	}
	value.Status = "connected"
	return value, nil
}

func Run(ctx context.Context, input io.Reader, output io.Writer) error {
	request, err := Read(input)
	if err != nil {
		return err
	}
	value, err := Probe(ctx, request)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(value)
}

func mustRequest(request Request) string { raw, _ := json.Marshal(request); return string(raw) }

func classify(err error) string {
	var upstream *pgconn.PgError
	if errors.As(err, &upstream) {
		switch {
		case upstream.Code == "28000" && strings.HasPrefix(upstream.Message, "pg_hba.conf rejects connection"):
			return "hba_denied"
		case upstream.Code == "55000" && strings.Contains(upstream.Message, "not currently accepting connections"):
			return "database_closed"
		case upstream.Code == "28P01":
			return "authentication_failed"
		}
	}
	return "unverified"
}
