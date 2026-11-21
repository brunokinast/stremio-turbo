// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

// Command rivulet-engine is a lightweight, IPv6-capable drop-in replacement for
// Stremio's closed-source streaming server (server.js), built on
// anacrolix/torrent. It serves the enginefs HTTP API that stremio-web expects.
//
// All bootstrap logic (env parsing, subsystem wiring, HTTP/HTTPS listeners,
// graceful shutdown) lives in internal/app so it can be shared with
// cmd/librivulet-engine, the c-shared library used on SELinux-enforcing Android
// where exec() of a bundled binary is blocked. main here keeps only what is
// specific to running as a standalone OS process: the version subcommand,
// OS signal handling, and process exit codes.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/rivulet-kodi/rivulet-engine/internal/app"
)

// version is overridable at build time with -ldflags "-X main.version=...".
// It is reported as settings.serverVersion; keep it aligned with a real Stremio
// server version so stremio-web does not gate features.
var version = app.DefaultVersion

// Build metadata, injected at release time via
// -ldflags "-X main.buildVersion=... -X main.buildCommit=... -X main.buildDate=...".
// These are distinct from `version` (the Stremio-compatible serverVersion).
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-version", "--version":
			fmt.Printf("rivulet-engine %s (server-api %s)\n", buildVersion, version)
			if buildCommit != "" {
				fmt.Printf("commit %s, built %s\n", buildCommit, buildDate)
			}
			return
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logw := io.Writer(os.Stderr)
	if path := os.Getenv("RIVULET_LOG_FILE"); path != "" {
		f, err := openLogFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rivulet-engine: RIVULET_LOG_FILE:", err)
		} else {
			defer f.Close() //nolint:errcheck // process exit
			logw = f
		}
	}

	err := app.Run(ctx, app.Config{Lookup: app.OSLookup, Version: version}, logw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rivulet-engine:", err)
		os.Exit(1)
	}
}

// openLogFile starts a new log at path, keeping the previous run's (a server
// that exited or crashed) as <name>.prev.log. An app that bundles the server
// sets RIVULET_LOG_FILE: the server can outlive the app's output pipe while
// it finishes downloads.
func openLogFile(path string) (*os.File, error) {
	if _, err := os.Stat(path); err == nil {
		prev := strings.TrimSuffix(path, ".log") + ".prev.log"
		_ = os.Rename(path, prev)
	}
	return os.Create(path)
}
