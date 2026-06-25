package matter

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// minNodeMajor is the minimum Node.js major version matter.js 0.16 supports.
// 18 is the floor; 20+ LTS is recommended.
const minNodeMajor = 18

// NodeInfo describes the detected Node.js runtime.
type NodeInfo struct {
	Path      string
	Version   string // raw, e.g. "v22.23.1"
	Major     int
	Available bool
	Reason    string // human-readable explanation when Available is false
}

// Indirection points for testing without a real Node install.
var (
	lookPath   = exec.LookPath
	runVersion = func(path string) (string, error) {
		out, err := exec.Command(path, "--version").Output()
		return string(out), err
	}
)

var (
	detectOnce sync.Once
	detected   NodeInfo
)

// DetectNode resolves and caches the Node.js runtime once per process.
func DetectNode() NodeInfo {
	detectOnce.Do(func() { detected = detectNode() })
	return detected
}

// detectNode performs the (uncached) detection. Kept separate so tests can
// exercise it directly via the lookPath/runVersion indirection.
func detectNode() NodeInfo {
	path := os.Getenv("MATTER_NODE_PATH")
	if path == "" {
		var err error
		path, err = lookPath("node")
		if err != nil {
			return NodeInfo{
				Available: false,
				Reason:    "Node.js not found on PATH — install Node.js v20+ to use Matter",
			}
		}
	}

	verStr, err := runVersion(path)
	if err != nil {
		return NodeInfo{
			Path:      path,
			Available: false,
			Reason:    "failed to run node --version: " + err.Error(),
		}
	}

	ver := strings.TrimSpace(verStr)
	major := parseNodeMajor(ver)
	if major < minNodeMajor {
		return NodeInfo{
			Path:      path,
			Version:   ver,
			Major:     major,
			Available: false,
			Reason:    fmt.Sprintf("Node.js %s is too old — install v20+ to use Matter", ver),
		}
	}

	return NodeInfo{Path: path, Version: ver, Major: major, Available: true}
}

// parseNodeMajor extracts the major version from strings like "v22.23.1" or
// "22.23.1". Returns 0 when it cannot be parsed.
func parseNodeMajor(ver string) int {
	v := strings.TrimSpace(ver)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}
