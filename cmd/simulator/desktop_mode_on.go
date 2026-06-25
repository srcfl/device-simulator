//go:build desktop

package main

// defaultDesktopMode is the default for the --desktop flag.
//
// Desktop release builds are compiled with the "desktop" build tag (see the
// `-tags "desktop,production"` builds for macOS, Linux and Windows). For those
// builds the app should open the native Wails window when launched with no
// arguments, so double-clicking the executable "just works". Pass
// --desktop=false (or set WAILS_DESKTOP=0) to run those builds headless.
const defaultDesktopMode = true
