// aieval measures tool selection with a real local model. It sends each question with the tool
// registry and checks the model's first tool call against the expected one. It executes nothing and
// needs no database. Results are for the machine and model they ran on, nothing more.
//
//	aieval -url http://127.0.0.1:8090/v1 -model qwen3-1.7b -n 10 -offset 0
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/aitools"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8090/v1", "OpenAI-compatible base URL")
	model := flag.String("model", "qwen3-1.7b", "model name")
	key := flag.String("key", os.Getenv("AI_KEY"), "API key if any")
	n := flag.Int("n", 10, "cases to run (0 = all)")
	off := flag.Int("offset", 0, "first case")
	stride := flag.Int("stride", 1, "take every Nth case from offset (spreads a small run over all tools)")
	flag.Parse()
	cs := cases()
	cfg := llm.Config{BaseURL: *url, Model: *model, APIKey: *key, Timeout: 10 * time.Minute, NoThinking: true}
	sys := "You are the assistant inside an industrial IoT platform. Use the tools; never guess values. For \"something is wrong at X\", call investigate_scope. For how-to questions call search_docs."
	ok, ran, valid := 0, 0, 0
	for i := *off; i < len(cs); i += *stride {
		if *n > 0 && ran >= *n {
			break
		}
		c := cs[i]
		ran++
		start := time.Now()
		m, err := llm.Chat(context.Background(), cfg, []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: c.Q}}, aitools.Specs())
		if err != nil || len(m.ToolCalls) == 0 {
			fmt.Printf("MISS  #%d %q -> no tool call (%v)\n", i, c.Q, err)
			continue
		}
		tc := m.ToolCalls[0]
		hit := false
		for _, w := range c.Want {
			hit = hit || w == tc.Func.Name
		}
		args := tc.Func.Arguments
		if hit && c.Arg != "" && !strings.Contains(strings.ToLower(args), strings.ToLower(c.Arg)) {
			hit = false
		}
		if _, e := aitools.Resolve(tc.Func.Name, args); e == nil {
			valid++
		}
		if hit {
			ok++
		}
		b, _ := json.Marshal(args)
		fmt.Printf("%-5s #%d %q -> %s %s (%.0fs)\n", map[bool]string{true: "OK", false: "MISS"}[hit], i, c.Q, tc.Func.Name, string(b), time.Since(start).Seconds())
	}
	fmt.Printf("\nselected correctly: %d/%d, arguments valid: %d/%d (of %d cases defined)\n", ok, ran, valid, ran, len(cs))
}
