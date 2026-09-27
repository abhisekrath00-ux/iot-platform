// mcpeval replays the golden MCP eval set (golden.json) against a live
// gateway and reports pass/fail per case. It checks the tool contract an AI
// client depends on: allowlist shape, tenant-scoped answers, clamped ranges,
// clean errors. Run it against any deployed stack:
//
//	go run ./cmd/mcpeval -url http://localhost:8100 -token "$JWT"
//
// Exit code 1 means at least one case failed.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed golden.json
var golden []byte

type evalCase struct {
	Name     string `json:"name"`
	Question string `json:"question"`
	Call     struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	} `json:"call"`
	Expect struct {
		ResultContains    []string `json:"result_contains"`
		ResultNotContains []string `json:"result_not_contains"`
		NoError           bool     `json:"no_error"`
		ExpectError       bool     `json:"expect_error"`
	} `json:"expect"`
}

type suite struct {
	Cases []evalCase `json:"cases"`
}

func main() {
	base := flag.String("url", "http://localhost:8100", "MCP gateway base URL")
	token := flag.String("token", "", "delegated JWT (tenant identity)")
	flag.Parse()
	if *token == "" {
		fmt.Println("mcpeval: -token is required (delegated JWT)")
		os.Exit(2)
	}
	var s suite
	if err := json.Unmarshal(golden, &s); err != nil {
		fmt.Println("golden.json:", err)
		os.Exit(2)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	failed := 0
	for i, c := range s.Cases {
		err := runCase(client, *base, *token, i+1, c)
		if err != nil {
			fmt.Printf("FAIL %-32s %v\n", c.Name, err)
			failed++
		} else {
			fmt.Printf("pass %-32s (%s)\n", c.Name, c.Question)
		}
	}
	fmt.Printf("\n%d/%d cases passed\n", len(s.Cases)-failed, len(s.Cases))
	if failed > 0 {
		os.Exit(1)
	}
}

func runCase(client *http.Client, base, token string, id int, c evalCase) error {
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": c.Call.Method, "params": c.Call.Params,
	})
	req, _ := http.NewRequest("POST", strings.TrimSuffix(base, "/")+"/mcp", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("response not JSON-RPC: %s", body)
	}
	hasErr := len(r.Error) > 0 && string(r.Error) != "null"
	if c.Expect.ExpectError && !hasErr {
		return fmt.Errorf("expected an error, got result %s", r.Result)
	}
	if (c.Expect.NoError || len(c.Expect.ResultContains) > 0) && hasErr {
		return fmt.Errorf("unexpected error %s", r.Error)
	}
	res := string(r.Result)
	for _, want := range c.Expect.ResultContains {
		if !strings.Contains(res, want) {
			return fmt.Errorf("result missing %q (got %.200s)", want, res)
		}
	}
	for _, ban := range c.Expect.ResultNotContains {
		if strings.Contains(res, ban) {
			return fmt.Errorf("result contains banned %q (got %.200s)", ban, res)
		}
	}
	return nil
}
