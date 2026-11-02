package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
)

func runtimeTestRequest(t *testing.T, s *Server, path string) modelRuntimeRecord {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: HTTP %d: %s", path, w.Code, w.Body.String())
	}
	var rec modelRuntimeRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestAPI_Models_RuntimeFromConfigForUnloadedModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("small model"), 0600); err != nil {
		t.Fatal(err)
	}
	mc := config.ModelConfig{
		Cmd: "llama-server --model " + strconvQuoteRuntime(path) + " --ctx-size 32768 --n-gpu-layers all --cache-type-k q8_0 --cache-type-v q8_0 --parallel 1 --batch-size 1024 --ubatch-size 256 --fit off --fit-target 4096",
	}
	s := newTestServerWithConfig(config.Config{Models: map[string]config.ModelConfig{"m": mc}}, newStubRouter(nil, ""), newStubRouter(nil, ""))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var response struct { Models []modelRuntimeRecord `json:"models"` }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil { t.Fatal(err) }
	if len(response.Models) != 1 { t.Fatalf("got %d models", len(response.Models)) }
	rec := response.Models[0]
	if rec.State != "stopped" || rec.Runtime.Source != "cmd" ||
		rec.Runtime.ModelPath != path || rec.Runtime.FileExists == nil || !*rec.Runtime.FileExists ||
		rec.Runtime.FileSize == nil || *rec.Runtime.FileSize != int64(len("small model")) ||
		rec.Runtime.FileMtime == "" || rec.Runtime.CtxSize == nil || *rec.Runtime.CtxSize != 32768 ||
		rec.Runtime.GPULayers != "all" || rec.Runtime.CacheTypeK != "q8_0" ||
		rec.Runtime.CacheTypeV != "q8_0" || rec.Runtime.Parallel == nil || *rec.Runtime.Parallel != 1 ||
		rec.Runtime.BatchSize == nil || *rec.Runtime.BatchSize != 1024 ||
		rec.Runtime.UBatchSize == nil || *rec.Runtime.UBatchSize != 256 ||
		rec.Runtime.Fit != "off" || rec.Runtime.FitTarget == nil || *rec.Runtime.FitTarget != 4096 {
		t.Fatalf("unexpected model: %+v", rec)
	}
	if rec.ContextLength != nil { t.Fatalf("invented context length: %+v", rec) }
}

func strconvQuoteRuntime(s string) string {
	if runtime.GOOS == "windows" { return """ + s + """ }
	return "'" + s + "'"
}

func TestAPI_Models_RuntimeEqualsRunningModel(t *testing.T) {
	mc := config.ModelConfig{Cmd: "llama-server -m sample.gguf -c 1234"}
	local := newStubRouter(nil, "")
	s := newTestServerWithConfig(config.Config{Models: map[string]config.ModelConfig{"m": mc}}, local, newStubRouter(nil, ""))
	unloaded := runtimeTestRequest(t, s, "/api/models/m")
	local.running = map[string]process.ProcessState{"m": process.StateReady}
	loaded := runtimeTestRequest(t, s, "/api/models/m")
	if unloaded.State != "stopped" || loaded.State != "ready" ||
		unloaded.Runtime.ModelPath != loaded.Runtime.ModelPath ||
		*unloaded.Runtime.CtxSize != *loaded.Runtime.CtxSize {
		t.Fatalf("runtime mismatch: %+v / %+v", unloaded, loaded)
	}
}

func TestAPI_Models_RuntimeShortFlags(t *testing.T) {
	mc := config.ModelConfig{Cmd: "llama-server -m file.gguf -c 100 -ngl auto -np 2 -b 32 -ub 16 -ctk q4_0 -ctv q8_0"}
	r := parseModelRuntime(mc)
	if r.CtxSize == nil || *r.CtxSize != 100 || r.GPULayers != "auto" ||
		r.Parallel == nil || *r.Parallel != 2 || r.BatchSize == nil || *r.BatchSize != 32 ||
		r.UBatchSize == nil || *r.UBatchSize != 16 || r.CacheTypeK != "q4_0" || r.CacheTypeV != "q8_0" {
		t.Fatalf("short flags: %+v", r)
	}
}

func TestAPI_Models_RuntimeProxyModel(t *testing.T) {
	r := parseModelRuntime(config.ModelConfig{Proxy:"https://example.com"})
	if r.Source != "proxy" || r.ModelPath != "" || r.FileExists != nil || r.CtxSize != nil {
		t.Fatalf("proxy: %+v", r)
	}
}

func TestAPI_Models_RuntimeHidesSecrets(t *testing.T) {
	mc := config.ModelConfig{Cmd: "llama-server --api-key SECRET_123 --hf-token=SECRET_456 -m safe.gguf --ctx-size 512", Env: []string{"TOKEN=SECRET_789"}}
	s := newTestServerWithConfig(config.Config{Models: map[string]config.ModelConfig{"m": mc}}, newStubRouter(nil, ""), newStubRouter(nil, ""))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/models", nil))
	body := w.Body.String()
	if strings.Contains(body, "SECRET_") || strings.Contains(body, "--api-key") || strings.Contains(body, "TOKEN=") || strings.Contains(body, "llama-server") {
		t.Fatalf("secret leaked: %s", body)
	}
}

func TestAPI_Models_RuntimeMissingFile(t *testing.T) {
	r := parseModelRuntime(config.ModelConfig{Cmd:"llama-server --model does-not-exist.gguf"})
	if r.FileExists == nil || *r.FileExists || r.FileSize != nil || r.FileMtime != "" {
		t.Fatalf("missing: %+v", r)
	}
}

func TestAPI_Models_RuntimeFlagEqualsForm(t *testing.T) {
	r := parseModelRuntime(config.ModelConfig{Cmd:"llama-server --ctx-size=32768 --model=missing.gguf --parallel=0 --ctx-size=0"})
	if r.CtxSize == nil || *r.CtxSize != 0 || r.Parallel == nil || *r.Parallel != 0 || r.ModelPath == "" {
		t.Fatalf("equals/zero/last flag: %+v", r)
	}
}

func TestAPI_ModelByID_ResolvesAlias(t *testing.T) {
	cfg, err := config.LoadConfigFromReader(strings.NewReader("models:
  main:
    cmd: llama-server -m m.gguf
    aliases: [alias]
  author/model:
    cmd: llama-server -m m.gguf
    unlisted: true
"))
	if err != nil { t.Fatal(err) }
	s := newTestServerWithConfig(cfg, newStubRouter(nil, ""), newStubRouter(nil, ""))
	if r := runtimeTestRequest(t, s, "/api/models/alias"); r.ID != "main" { t.Fatalf("alias: %+v", r) }
	if r := runtimeTestRequest(t, s, "/api/models/author/model"); r.ID != "author/model" { t.Fatalf("slash: %+v", r) }
}

func TestAPI_ModelByID_NotFound(t *testing.T) {
	s := newTestServerWithConfig(config.Config{Models: map[string]config.ModelConfig{}}, newStubRouter(nil, ""), newStubRouter(nil, ""))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/models/no-such-model", nil))
	if w.Code != http.StatusNotFound { t.Fatalf("status: %d", w.Code) }
}

func TestAPI_Models_ContextAndConfigChange(t *testing.T) {
	mc := config.ModelConfig{Cmd:"llama-server -m old.gguf -c 8192", Capabilities: config.ModelCapConfig{Context:16384}}
	s := newTestServerWithConfig(config.Config{Models: map[string]config.ModelConfig{"m":mc}},newStubRouter(nil,""),newStubRouter(nil,""))
	first := runtimeTestRequest(t,s,"/api/models/m")
	if first.ContextLength == nil || *first.ContextLength != 16384 { t.Fatal("configured context missing") }
	mc.Cmd = "llama-server -m new.gguf -c 4096"
	s.cfg.Models["m"] = mc
	second := runtimeTestRequest(t,s,"/api/models/m")
	if strings.Contains(second.Runtime.ModelPath,"old.gguf") || second.Runtime.CtxSize == nil || *second.Runtime.CtxSize != 4096 { t.Fatalf("stale: %+v",second) }
}
