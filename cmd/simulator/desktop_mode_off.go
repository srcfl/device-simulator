//go:build !desktop

package main

// defaultDesktopMode is the default for the --desktop flag.
//
// Server/Docker builds omit the "desktop" build tag (e.g. the Docker image and
// the `*-server` Makefile targets). They have no display and default to the
// headless HTTP-only server. Pass --desktop (or set WAILS_DESKTOP=1) to force
// the native window.
const defaultDesktopMode = false
