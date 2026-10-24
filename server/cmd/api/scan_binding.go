package main

import (
	"encoding/json"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Bind Add to the observed transport, not just an address that exists on some
// other bus. Legacy serial scans without recorded settings must be rerun.
func boundScanConnection(kind, driver string, params, result []byte, c map[string]any) bool {
	str := func(k string) string { v, _ := c[k].(string); return v }
	num := func(k string, def float64) (float64, bool) {
		v, exists := c[k]
		if !exists {
			return def, true
		}
		n, ok := v.(float64)
		return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n)
	}
	switch kind {
	case "modbus-rtu":
		if driver != "modbus-generic" && driver != "modbus-energy-meter" {
			return false
		}
		var p scanParams
		if json.Unmarshal(params, &p) != nil || validateScan(kind, &p) != nil {
			return false
		}
		if str("port") != p.Port {
			return false
		}
		for _, x := range []struct {
			k         string
			def, want float64
		}{{"baud", 9600, float64(p.Baud)}, {"data_bits", 8, float64(p.DataBits)}, {"stop_bits", 1, float64(p.StopBits)}} {
			n, ok := num(x.k, x.def)
			if !ok || n != x.want {
				return false
			}
		}
		parity := str("parity")
		if parity == "" {
			parity = "none"
		}
		if parity != p.Parity {
			return false
		}
		if _, ok := num("address", -1); !ok {
			return false
		}
	case "lan":
		if driverKinds[driver] != "net" {
			return false
		}
		port, ok := num("net_port", 0)
		if !ok || port < 1 || port > 65535 {
			return false
		}
		if driver == "opcua" && str("endpoint") != "" {
			u, e := url.Parse(str("endpoint"))
			if e != nil || u.Scheme != "opc.tcp" || u.User != nil {
				return false
			}
			h, p, e := net.SplitHostPort(u.Host)
			if e != nil {
				return false
			}
			n, e := strconv.Atoi(p)
			if e != nil || h != str("host") || float64(n) != port || u.RawQuery != "" || u.Fragment != "" {
				return false
			}
		} else if str("endpoint") != "" {
			return false
		}
	case "bacnet":
		if driver != "bacnet" || str("endpoint") != "" {
			return false
		}
	default:
		return false
	}
	// Unrelated transport fields must not smuggle an alternate polling target.
	if kind != "modbus-rtu" && (str("port") != "" || str("parity") != "") {
		return false
	}
	if kind == "modbus-rtu" && (str("host") != "" || str("endpoint") != "") {
		return false
	}
	return connectionFromScan(kind, result, c) && !strings.ContainsRune(str("host"), '\x00')
}
