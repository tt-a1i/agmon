package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

type webOptions struct {
	port          string
	token         string
	noAuth        bool
	generateToken bool
}

func parseWebOptions(args []string) (webOptions, error) {
	opts := webOptions{port: "8370"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--port requires a value")
			}
			opts.port = args[i]
		case "--token":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--token requires a value")
			}
			opts.token = strings.TrimSpace(args[i])
			if opts.token == "" {
				return opts, fmt.Errorf("--token must not be empty")
			}
		case "--no-auth":
			opts.noAuth = true
		case "--generate-token":
			opts.generateToken = true
		default:
			return opts, fmt.Errorf("unknown web argument: %s", args[i])
		}
	}
	return opts, nil
}

func resolveWebAuthToken(opts webOptions) (string, error) {
	if opts.noAuth {
		return "", nil
	}
	if opts.token != "" {
		return opts.token, nil
	}
	data, err := os.ReadFile(webTokenPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read web-token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("web-token file exists but empty")
	}
	return token, nil
}

func writeGeneratedWebToken() (string, string, error) {
	token, err := generateWebToken()
	if err != nil {
		return "", "", err
	}
	path := webTokenPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", "", err
	}
	return token, path, nil
}

func generateWebToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func webTokenPath() string {
	return appdir.Path("web-token")
}
