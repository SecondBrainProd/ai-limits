// Package config stores the helper's relay address and helper token.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// DefaultRelay is the public relay.
const DefaultRelay = "https://ailimits.second-brain.ru"

// Config is the helper's persistent state. HelperToken is a secret.
type Config struct {
	Relay       string `json:"relay"`
	HelperToken string `json:"helper_token"`
}

// Path returns the config file location: <user config dir>/ailimits/config.json.
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ailimits", "config.json")
}

// Load reads the config. A missing file yields the default relay and no token.
func Load() (Config, error) { return loadFrom(Path()) }

// Save writes the config atomically with mode 0600 in a 0700 directory.
func Save(c Config) error { return saveTo(Path(), c) }

func loadFrom(path string) (Config, error) {
	c := Config{Relay: DefaultRelay}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, errors.New("cannot read config file")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		// Drop err: it could quote file content (the helper token).
		return Config{}, errors.New("config file is not valid JSON")
	}
	if c.Relay == "" {
		c.Relay = DefaultRelay
	}
	return c, nil
}

func saveTo(path string, c Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create config directory")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return errors.New("cannot encode config")
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return errors.New("cannot write config file")
	}
	name := tmp.Name()
	fail := func() error {
		tmp.Close()
		os.Remove(name)
		return errors.New("cannot write config file")
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail()
	}
	if _, err := tmp.Write(raw); err != nil {
		return fail()
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return errors.New("cannot write config file")
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return errors.New("cannot write config file")
	}
	return nil
}
