package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/plugin"
)

// mcpServerSpec is one entry of the client configuration of a daemon.
type mcpServerSpec struct {
	id    string
	url   string
	token string
}

// configureMCPClient points a daemon at the given remote MCP servers.
func configureMCPClient(t *testing.T, baseURL, apiToken string, servers []mcpServerSpec) {
	t.Helper()
	parts := make([]string, 0, len(servers))
	for _, server := range servers {
		parts = append(parts, fmt.Sprintf(
			`{"id":%s,"name":%s,"transport":"http","url":%s,"headers":{"Authorization":%s}}`,
			quoteJSON(server.id), quoteJSON(server.id), quoteJSON(server.url), quoteJSON("Bearer "+server.token)))
	}
	resp, err := apiPost(t, baseURL+"/api/plugins/mcp/servers", apiToken, `{"servers":[`+strings.Join(parts, ",")+`]}`)
	if err != nil {
		t.Fatalf("configure the client: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("configuring the client returned %d: %s", resp.StatusCode, body)
	}
}

// waitForTask polls a remote task, answers the questions it asks, and returns
// the final report. The answer count is reported through answered.
func waitForTask(t *testing.T, d *Daemon, taskTool, taskID string, answered *int) string {
	t.Helper()
	answerTool := strings.Replace(taskTool, "task_get", "task_answer", 1)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := d.registry.CallTool(context.Background(), taskTool, map[string]any{"task_id": taskID})
		if err != nil {
			t.Fatalf("reading %s failed: %v", taskID, err)
		}
		report := fmt.Sprint(out)
		if strings.Contains(report, ": needs_input") {
			fields := strings.Fields(report)
			questionID := ""
			for i := 0; i+1 < len(fields); i++ {
				if fields[i] == "question" {
					questionID = fields[i+1]
					break
				}
			}
			if questionID == "" {
				t.Fatalf("no question id in the report: %s", report)
			}
			if _, err := d.registry.CallTool(context.Background(), answerTool, map[string]any{
				"task_id":     taskID,
				"question_id": questionID,
				"choice":      "once",
			}); err != nil {
				t.Fatalf("answering %s failed: %v", questionID, err)
			}
			if answered != nil {
				*answered++
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if strings.Contains(report, ": finished") || strings.Contains(report, ": failed") || strings.Contains(report, ": cancelled") {
			return report
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish in time", taskID)
	return ""
}

// TestMCPCascadeThreeDaemons checks a chain of three daemons: a client calls a
// server that is itself a client of a third daemon. The middle daemon runs the
// task with its own model, so the last hop is a tool call made by that model.
func TestMCPCascadeThreeDaemons(t *testing.T) {
	_, base3, api3 := startMCPServerTest(t)
	server2, base2, api2 := startMCPServerTest(t)
	client1, base1, api1 := startMCPServerTest(t)

	root2 := t.TempDir()
	root3 := t.TempDir()
	oracle := filepath.Join(root3, "oracle.txt")
	marker := "cascade-marker-" + filepath.Base(root3)
	if err := os.WriteFile(oracle, []byte(marker), 0o644); err != nil {
		t.Fatalf("write the oracle file: %v", err)
	}

	// cdx3 serves the oracle file to cdx2.
	enableMCPServer(t, base3, api3, "")
	allowLocalClass(t, base3, api3, `{"read_policy":"allow"}`)
	token3 := issueMCPClient(t, base3, api3, "cdx2", `{"read":"allow"}`)

	// cdx2 is both a server for cdx1 and a client of cdx3.
	enableMCPServer(t, base2, api2, `"remote_root":`+quoteJSON(root2))
	allowLocalClass(t, base2, api2, `{"read_policy":"allow"}`)
	token2 := issueMCPClient(t, base2, api2, "cdx1", `{"read":"allow","task":"allow"}`)
	configureMCPClient(t, base2, api2, []mcpServerSpec{{id: "c", url: base3 + "/mcp", token: token3}})

	if _, ok := server2.registry.GetTool("mcp_c_cdx_read_file"); !ok {
		t.Fatalf("the middle daemon did not connect to the third one: %v", toolsOn(server2, "mcp_"))
	}

	// The model of cdx2 reads the oracle through cdx3 and reports what it saw.
	step := 0
	server2.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "cascade-model",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "cascade-model/chat", Name: "cascade-model/chat", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			step++
			if step == 1 {
				return &plugin.CompleteResponse{
					Content: "reading the oracle",
					ToolCalls: []map[string]any{{
						"id":        "call_c",
						"name":      "mcp_c_cdx_read_file",
						"arguments": map[string]any{"path": oracle},
					}},
				}, nil
			}
			for i := len(req.Messages) - 1; i >= 0; i-- {
				message := req.Messages[i]
				if message["role"] == "tool" {
					return &plugin.CompleteResponse{Content: "oracle said: " + fmt.Sprint(message["content"])}, nil
				}
			}
			return &plugin.CompleteResponse{Content: "the model saw no tool result"}, nil
		},
	})

	// cdx1 talks to cdx2 only.
	configureMCPClient(t, base1, api1, []mcpServerSpec{{id: "b", url: base2 + "/mcp", token: token2}})

	if _, ok := client1.registry.GetTool("mcp_b_cdx_task_start"); !ok {
		t.Fatalf("cdx1 does not see the task tool of cdx2: %v", toolsOn(client1, "mcp_"))
	}
	// The middle daemon does not pass the tools of the third one through, so the
	// chain stays a chain and not a proxy catalog.
	if _, ok := client1.registry.GetTool("mcp_b_mcp_c_cdx_read_file"); ok {
		t.Fatalf("cdx2 exposed a proxy tool to cdx1: %v", toolsOn(client1, "mcp_"))
	}

	start, err := client1.registry.CallTool(context.Background(), "mcp_b_cdx_task_start", map[string]any{
		"prompt":    "read the oracle file and report what it says",
		"timeout_s": 60,
		"max_steps": 4,
	})
	if err != nil {
		t.Fatalf("starting the cascade task failed: %v", err)
	}
	fields := strings.Fields(fmt.Sprint(start))
	var taskID string
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "task" && strings.HasPrefix(fields[i+1], "task_") {
			taskID = fields[i+1]
			break
		}
	}
	if taskID == "" {
		t.Fatalf("no task id in the start result: %s", start)
	}

	answered := 0
	report := waitForTask(t, client1, "mcp_b_cdx_task_get", taskID, &answered)
	if !strings.Contains(report, ": finished") {
		t.Fatalf("the cascade task did not finish: %s", report)
	}
	if !strings.Contains(report, marker) {
		t.Fatalf("the answer did not carry the content of the third daemon: %s", report)
	}
	// The oracle file lies outside the task working folder, so the daemon asks
	// before reading it and the question travels up to the first daemon.
	if answered == 0 {
		t.Fatal("the cascade task never asked for the outside-folder approval")
	}
}

// TestMCPThreeServersOneClient connects one client to three servers, calls all
// of them at once, and checks isolation: each answer stays on its own server,
// and one server going away leaves the others working.
func TestMCPThreeServersOneClient(t *testing.T) {
	_, baseA, apiA := startMCPServerTest(t)
	_, baseB, apiB := startMCPServerTest(t)
	serverC, baseC, apiC := startMCPServerTest(t)
	client, baseD, apiD := startMCPServerTest(t)

	roots := make([]string, 3)
	marks := []string{"mark-a", "mark-b", "mark-c"}
	bases := []string{baseA, baseB, baseC}
	apis := []string{apiA, apiB, apiC}
	names := []string{"a", "b", "c"}
	for i := range roots {
		roots[i] = t.TempDir()
		if err := os.WriteFile(filepath.Join(roots[i], marks[i]+".txt"), []byte(marks[i]), 0o644); err != nil {
			t.Fatalf("write project file: %v", err)
		}
		enableMCPServer(t, bases[i], apis[i], "")
		allowLocalClass(t, bases[i], apis[i], `{"read_policy":"allow"}`)
	}

	specs := make([]mcpServerSpec, 0, 3)
	for i := range bases {
		token := issueMCPClient(t, bases[i], apis[i], "three-"+names[i], `{"read":"allow"}`)
		specs = append(specs, mcpServerSpec{id: names[i], url: bases[i] + "/mcp", token: token})
	}
	configureMCPClient(t, baseD, apiD, specs)

	for _, name := range names {
		if _, ok := client.registry.GetTool("mcp_" + name + "_cdx_read_file"); !ok {
			t.Fatalf("the client is missing server %s: %v", name, toolsOn(client, "mcp_"))
		}
	}

	var wg sync.WaitGroup
	results := make([]string, 3)
	errs := make([]error, 3)
	for i := range names {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := client.registry.CallTool(context.Background(), "mcp_"+names[i]+"_cdx_list_dir", map[string]any{"path": roots[i]})
			results[i], errs[i] = fmt.Sprint(out), err
		}()
	}
	wg.Wait()
	for i := range names {
		if errs[i] != nil {
			t.Fatalf("call on %s failed: %v", names[i], errs[i])
		}
		for j := range marks {
			has := strings.Contains(results[i], marks[j]+".txt")
			if i == j && !has {
				t.Fatalf("server %s returned the wrong listing: %s", names[i], results[i])
			}
			if i != j && has {
				t.Fatalf("server %s leaked the listing of %s: %s", names[i], names[j], results[i])
			}
		}
	}

	// The third server goes away: the first two keep working.
	serverC.Stop()
	if _, err := client.registry.CallTool(context.Background(), "mcp_c_cdx_list_dir", map[string]any{"path": roots[2]}); err == nil {
		t.Fatal("a call to the stopped server succeeded")
	}
	for i := 0; i < 2; i++ {
		if _, err := client.registry.CallTool(context.Background(), "mcp_"+names[i]+"_cdx_list_dir", map[string]any{"path": roots[i]}); err != nil {
			t.Fatalf("server %s broke when the third one stopped: %v", names[i], err)
		}
	}
}
