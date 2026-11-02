package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// Management-only model information. Never serialize the command or environment.
type modelRuntimeRecord struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	State         string            `json:"state"`
	Aliases       []string          `json:"aliases"`
	ContextLength *int              `json:"context_length,omitempty"`
	Runtime       modelRuntimeInfo  `json:"runtime"`
}

type modelRuntimeInfo struct {
	Source      string `json:"source"`
	ModelPath   string `json:"model_path,omitempty"`
	CtxSize     *int   `json:"ctx_size,omitempty"`
	GPULayers   string `json:"gpu_layers,omitempty"`
	CacheTypeK  string `json:"cache_type_k,omitempty"`
	CacheTypeV  string `json:"cache_type_v,omitempty"`
	Parallel    *int   `json:"parallel,omitempty"`
	BatchSize   *int   `json:"batch_size,omitempty"`
	UBatchSize  *int   `json:"ubatch_size,omitempty"`
	Fit         string `json:"fit,omitempty"`
	FitTarget   *int   `json:"fit_target,omitempty"`
	FileExists  *bool  `json:"file_exists,omitempty"`
	FileSize    *int64 `json:"file_size,omitempty"`
	FileMtime   string `json:"file_mtime,omitempty"`
}

// Parse only recognized runtime flags. The launcher uses the same command
// tokenizer; tokenizing once avoids breaking quoted Windows paths.
func parseModelRuntime(mc config.ModelConfig) modelRuntimeInfo {
	if strings.TrimSpace(mc.Cmd) == "" {
		if mc.Proxy != "" {
			return modelRuntimeInfo{Source: "proxy"}
		}
		return modelRuntimeInfo{Source: "unknown"}
	}
	out := modelRuntimeInfo{Source: "cmd"}
	args, err := mc.SanitizedCommand()
	if err != nil {
		return out
	}
	flags := map[string]string{
		"--model": "model", "-m": "model",
		"--ctx-size": "ctx", "-c": "ctx",
		"--n-gpu-layers": "gpu", "--gpu-layers": "gpu", "-ngl": "gpu",
		"--cache-type-k": "ctk", "-ctk": "ctk",
		"--cache-type-v": "ctv", "-ctv": "ctv",
		"--parallel": "parallel", "-np": "parallel",
		"--batch-size": "batch", "-b": "batch",
		"--ubatch-size": "ubatch", "-ub": "ubatch",
		"--fit": "fit", "--fit-target": "fit_target",
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		key, value, hasEquals := strings.Cut(arg, "=")
		if key == "--" {
			break
		}
		// These known credential flags must not be interpreted as runtime options.
		if key == "--api-key" || key == "--hf-token" {
			if !hasEquals && i+1 < len(args) {
				i++
			}
			continue
		}
		field, recognized := flags[key]
		if !recognized {
			continue
		}
		if !hasEquals {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				continue
			}
			i++
			value = args[i]
		}
		switch field {
		case "model":
			out.ModelPath = value
		case "ctx":
			out.CtxSize = runtimeInt(value)
		case "gpu":
			out.GPULayers = value
		case "ctk":
			out.CacheTypeK = value
		case "ctv":
			out.CacheTypeV = value
		case "parallel":
			out.Parallel = runtimeInt(value)
		case "batch":
			out.BatchSize = runtimeInt(value)
		case "ubatch":
			out.UBatchSize = runtimeInt(value)
		case "fit":
			out.Fit = value
		case "fit_target":
			out.FitTarget = runtimeInt(value)
		}
	}
	if out.ModelPath != "" {
		path := out.ModelPath
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
			out.ModelPath = absolute
		}
		exists := false
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			exists = true
			size := info.Size()
			out.FileSize = &size
			out.FileMtime = info.ModTime().UTC().Format(time.RFC3339)
		}
		out.FileExists = &exists
	}
	return out
}

func runtimeInt(value string) *int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &n
}

func (s *Server) managementModelRecord(id string, running map[string]process.Status) modelRuntimeRecord {
	mc := s.cfg.Models[id]
	state := "stopped"
	if st, ok := running[id]; ok {
		state = string(st.State)
	}
	aliases := append([]string{}, mc.Aliases...)
	caps := s.resolveCapabilities(s.shutdownCtx, id, mc)
	var contextLength *int
	if caps.Context > 0 {
		contextLength = &caps.Context
	}
	return modelRuntimeRecord{
		ID: id, Name: mc.Name, Description: mc.Description,
		State: state, Aliases: aliases, ContextLength: contextLength,
		Runtime: parseModelRuntime(mc),
	}
}

func (s *Server) handleManagementModels(w http.ResponseWriter, r *http.Request) {
	running := s.local.RunningStatus()
	ids := make([]string, 0, len(s.cfg.Models))
	for id := range s.cfg.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	models := make([]modelRuntimeRecord, 0, len(ids))
	for _, id := range ids {
		models = append(models, s.managementModelRecord(id, running))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Models []modelRuntimeRecord `json:"models"`
	}{Models: models})
}

func (s *Server) handleManagementModel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.cfg.RealModelName(r.PathValue("id"))
	if !ok {
		swaputil.SendResponse(w, r, http.StatusNotFound, "model not found")
		return
	}
	record := s.managementModelRecord(id, s.local.RunningStatus())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}
