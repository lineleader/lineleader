package ledgerclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the resolved server URL and bearer token for a Client.
type Config struct {
	ServerURL string
	Token     string
}

// fileConfig is the shape of ~/.config/lineleader/client.json.
type fileConfig struct {
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
}

// ConfigPath returns the platform-appropriate path for the client config
// file, mirroring dvc.DefaultConfigPath (internal/dvc/config.go): under
// os.UserConfigDir(), falling back to $HOME if that's unavailable.
func ConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "lineleader", "client.json")
}

// ResolveConfig determines the server URL and token to use, in this
// precedence order (highest first):
//
//  1. flagServer / flagToken — the --server/--token CLI flags, when non-empty
//  2. LINELEADER_SERVER / LINELEADER_TOKEN environment variables
//  3. ~/.config/lineleader/client.json — {"server_url": "...", "token": "..."}
//
// Each of ServerURL and Token is resolved independently (e.g. --server with
// no --token still falls through to LINELEADER_TOKEN or the config file for
// the token). A missing config file is not an error — it's simply skipped,
// falling through to whatever's already been resolved. A missing server URL
// after all three sources is an error naming exactly what to set.
func ResolveConfig(flagServer, flagToken string) (Config, error) {
	var fc fileConfig
	if data, err := os.ReadFile(ConfigPath()); err == nil {
		// Best-effort: a malformed config file is treated the same as a
		// missing one (falls through to env/flags, or the "no server
		// configured" error below) rather than failing the whole command.
		_ = json.Unmarshal(data, &fc)
	}

	cfg := Config{
		ServerURL: firstNonEmpty(flagServer, os.Getenv("LINELEADER_SERVER"), fc.ServerURL),
		Token:     firstNonEmpty(flagToken, os.Getenv("LINELEADER_TOKEN"), fc.Token),
	}

	if cfg.ServerURL == "" {
		return Config{}, fmt.Errorf(
			"no lineleader server configured: set --server, the LINELEADER_SERVER environment variable, or \"server_url\" in %s",
			ConfigPath())
	}
	cfg.ServerURL = strings.TrimSuffix(cfg.ServerURL, "/")
	return cfg, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
