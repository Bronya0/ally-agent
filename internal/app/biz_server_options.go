package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// ServerConfig configures one headless process. CLI overrides the optional JSON
// file; api.json supplies the existing token without changing desktop preferences.
type ServerConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Token     string `json:"token"`
	TokenFile string `json:"tokenFile"`
	Workspace string `json:"workspace"`
	DataDir   string `json:"dataDir"`
}

func loadServerOptions(args []string, output io.Writer) (ServerConfig, error) {
	cfg := ServerConfig{Host: "127.0.0.1", Port: apiDefaultPort}
	if host := os.Getenv("WAILS_SERVER_HOST"); host != "" {
		cfg.Host = host
	}
	if port := os.Getenv("WAILS_SERVER_PORT"); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			return cfg, fmt.Errorf("WAILS_SERVER_PORT: %w", err)
		}
		cfg.Port = n
	}

	fs := flag.NewFlagSet("ally-server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fs.SetOutput(output); fs.PrintDefaults() }
	var configPath string
	fs.StringVar(&configPath, "config", "", "Optional server JSON configuration file")
	fs.StringVar(&cfg.Host, "host", cfg.Host, "HTTP listen host")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "Fixed HTTP port (1-65535)")
	fs.StringVar(&cfg.Token, "token", "", "Bearer token (prefer --token-file)")
	fs.StringVar(&cfg.TokenFile, "token-file", "", "Read bearer token from a file")
	fs.StringVar(&cfg.Workspace, "workspace", "", "Default server-side workspace (optional; sessions may override)")
	fs.StringVar(&cfg.DataDir, "data-dir", "", "Application data directory (default ~/.ally_agent)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	explicitToken, explicitFile := false, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "token" {
			explicitToken = true
		}
		if f.Name == "token-file" {
			explicitFile = true
		}
	})
	if explicitToken && explicitFile {
		return cfg, errors.New("use only one of --token and --token-file")
	}
	cli := cfg
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return cfg, err
		}
		if !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
			return cfg, errors.New("server config must be a JSON object")
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("server config: %w", err)
		}
		var extra any
		if err = decoder.Decode(&extra); err != io.EOF {
			return cfg, errors.New("server config must contain one JSON object")
		}
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			cfg.Host = cli.Host
		case "port":
			cfg.Port = cli.Port
		case "token":
			cfg.Token = cli.Token
			cfg.TokenFile = ""
		case "token-file":
			cfg.TokenFile = cli.TokenFile
			cfg.Token = ""
		case "workspace":
			cfg.Workspace = cli.Workspace
		case "data-dir":
			cfg.DataDir = cli.DataDir
		}
	})
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, `/\`) || strings.IndexFunc(cfg.Host, unicode.IsSpace) >= 0 ||
		(strings.Contains(cfg.Host, ":") && net.ParseIP(cfg.Host) == nil) {
		return cfg, errors.New("host must be an IP address or hostname without a port")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, errors.New("port must be between 1 and 65535")
	}
	if cfg.Token != "" && cfg.TokenFile != "" {
		return cfg, errors.New("configure only one of token and tokenFile")
	}
	if cfg.DataDir != "" {
		dir, err := filepath.Abs(cfg.DataDir)
		if err != nil {
			return cfg, err
		}
		cfg.DataDir = dir
	}
	if cfg.Workspace != "" {
		root, err := workspaceRoot(ConfigState{Workspace: cfg.Workspace})
		if err != nil {
			return cfg, err
		}
		cfg.Workspace = root
	}
	if cfg.TokenFile != "" {
		data, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return cfg, fmt.Errorf("token file: %w", err)
		}
		cfg.Token = strings.TrimSpace(string(data))
		if cfg.Token == "" {
			return cfg, errors.New("token file is empty")
		}
	}
	cfg.Token = strings.TrimSpace(cfg.Token)
	return cfg, nil
}

func (cfg ServerConfig) address() string { return net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)) }
