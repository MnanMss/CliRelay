package codexmanager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func readJSONFile(path string, target any) (bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return false, fmt.Errorf("state file path is required")
	}
	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true, nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return true, err
	}
	return true, nil
}

func writeJSONFile(path string, payload any) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("state file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := trimmedPath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, trimmedPath); err == nil {
		return nil
	}
	_ = os.Remove(trimmedPath)
	if err := os.Rename(tmpPath, trimmedPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
