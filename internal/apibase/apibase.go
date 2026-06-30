// Package apibase is the single source of truth for XiT's backend API base
// URL, shared by the version-check and telemetry clients.
//
// Resolution order (highest priority first):
//
//  1. XIT_API_BASE environment variable (dev/test/self-host override)
//  2. Default (the production domain compiled into release builds)
//
// The release build ships with the production API base below. XIT_API_BASE
// remains the highest-priority override for dev/test/self-hosted endpoints.
// When Default is empty in a dev build and XIT_API_BASE is unset, Resolve()
// returns "" and both clients no-op (fail-open).
package apibase

import (
	"os"
	"strings"
)

// Default is the production API base for release builds.
var Default = "https://xit-api.stephenwilson.dev"

// Resolve returns the effective API base (no trailing slash), honoring the
// XIT_API_BASE override before falling back to Default. Empty means "no
// endpoint configured" — callers must treat that as a silent no-op.
func Resolve() string {
	if v := strings.TrimSpace(os.Getenv("XIT_API_BASE")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(strings.TrimSpace(Default), "/")
}

// Source reports where the resolved base came from, for `xit telemetry status`
// / `xit update-check` diagnostics. Never returns the URL itself here.
func Source() string {
	if strings.TrimSpace(os.Getenv("XIT_API_BASE")) != "" {
		return "XIT_API_BASE env"
	}
	if strings.TrimSpace(Default) != "" {
		return "built-in default"
	}
	return "unset (no endpoint configured)"
}
