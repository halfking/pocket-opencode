package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const pocketProviderID = "openai-compatible-pocket"

// MergeGatewayConfig preserves the existing OpenCode document and replaces
// only Pocket's provider entry. This keeps MCP, permissions, and unrelated
// providers intact across gateway updates.
func MergeGatewayConfig(existing []byte, cfg LLMGatewayConfig, defaultModel string) ([]byte, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("baseURL required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey required")
	}
	if defaultModel == "" && len(cfg.Models) > 0 {
		defaultModel = cfg.Models[0]
	}
	if defaultModel == "" {
		defaultModel = "gpt-4o"
	}

	doc := map[string]interface{}{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, fmt.Errorf("parse OpenCode config: %w", err)
		}
	}
	provider := map[string]interface{}{
		"name":    "Pocket LLM Gateway",
		"npm":     "@ai-sdk/openai-compatible",
		"options": map[string]interface{}{"baseURL": cfg.BaseURL, "apiKey": cfg.APIKey},
	}
	models := map[string]interface{}{}
	for _, model := range cfg.Models {
		if strings.TrimSpace(model) != "" {
			models[model] = map[string]interface{}{"name": model}
		}
	}
	provider["models"] = models
	providers, ok := doc["provider"].(map[string]interface{})
	if !ok {
		providers = map[string]interface{}{}
	}
	providers[pocketProviderID] = provider
	doc["provider"] = providers
	doc["model"] = fmt.Sprintf("%s/%s", pocketProviderID, defaultModel)

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal OpenCode config: %w", err)
	}
	out = append(out, '\n')
	return out, nil
}

// WriteGatewayConfig atomically updates path and keeps a mode-restricted
// backup. The old file is restored if the rename cannot complete.
func WriteGatewayConfig(path string, cfg LLMGatewayConfig, defaultModel string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("OpenCode config path required")
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read OpenCode config: %w", err)
	}
	merged, err := MergeGatewayConfig(old, cfg, defaultModel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create OpenCode config directory: %w", err)
	}
	backup := path + ".bak"
	if len(old) > 0 {
		if err := writeRestrictedFile(backup, old, 0600); err != nil {
			return fmt.Errorf("backup OpenCode config: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".opencode-config-*")
	if err != nil {
		return fmt.Errorf("create temporary OpenCode config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect temporary OpenCode config: %w", err)
	}
	if _, err := tmp.Write(merged); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary OpenCode config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary OpenCode config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary OpenCode config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace OpenCode config: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func writeRestrictedFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && err != syscall.EINVAL {
		return err
	}
	return nil
}
