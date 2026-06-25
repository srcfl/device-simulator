package matter

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// The Node bridge program (source) and its dependencies are embedded into the
// Go binary. Dependencies are shipped as a single compressed archive
// (node_modules.tgz, ~12MB) rather than ~23k loose files, which keeps build
// times and the binary reasonable. Regenerate the archive with `make
// vendor-matter`.
//
//go:embed bridge/index.mjs bridge/package.json bridge/lib bridge/node_modules.tgz
var bridgeFS embed.FS

var (
	bridgeOnce sync.Once
	bridgeDir  string
	bridgeErr  error
)

// ensureBridgeExtracted extracts the embedded bridge (source + node_modules) to
// a per-user cache directory exactly once, keyed by a content hash so a new
// build re-extracts but repeated launches do not. Returns the directory that
// contains index.mjs and node_modules.
func ensureBridgeExtracted() (string, error) {
	bridgeOnce.Do(func() {
		bridgeDir, bridgeErr = extractBridge()
	})
	return bridgeDir, bridgeErr
}

func extractBridge() (string, error) {
	hash, err := bridgeContentHash()
	if err != nil {
		return "", err
	}

	root := bridgeCacheRoot()
	dir := filepath.Join(root, "bridge-"+hash)
	marker := filepath.Join(dir, ".extracted")

	if _, err := os.Stat(marker); err == nil {
		return dir, nil // already extracted for this build
	}

	// Fresh extraction: remove any partial dir, then write source + deps.
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	if err := writeEmbeddedSource(dir); err != nil {
		return "", fmt.Errorf("write bridge source: %w", err)
	}
	if err := extractNodeModules(dir); err != nil {
		return "", fmt.Errorf("extract node_modules: %w", err)
	}

	if err := os.WriteFile(marker, []byte(hash), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// writeEmbeddedSource copies index.mjs, package.json and lib/** out of the
// embedded FS, preserving the directory layout (minus the leading "bridge/").
func writeEmbeddedSource(dir string) error {
	return fs.WalkDir(bridgeFS, "bridge", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "bridge" {
			return nil
		}
		rel, _ := filepath.Rel("bridge", path)
		// node_modules.tgz is handled separately by extractNodeModules.
		if rel == "node_modules.tgz" {
			return nil
		}
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := bridgeFS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// extractNodeModules untars the embedded node_modules.tgz into dir.
func extractNodeModules(dir string) error {
	f, err := bridgeFS.Open("bridge/node_modules.tgz")
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dir, filepath.Clean(hdr.Name))
		// Guard against path traversal in archive entries.
		if rel, err := filepath.Rel(dir, target); err != nil || rel == ".." || hasDotDotPrefix(rel) {
			return fmt.Errorf("invalid archive path: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 3 && rel[0] == '.' && rel[1] == '.' && (rel[2] == filepath.Separator || rel[2] == '/')
}

// bridgeContentHash hashes index.mjs and the dependency archive so the cache
// key changes whenever the bridge or its deps change.
func bridgeContentHash() (string, error) {
	h := sha256.New()
	for _, name := range []string{"bridge/index.mjs", "bridge/node_modules.tgz"} {
		f, err := bridgeFS.Open(name)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func bridgeCacheRoot() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "device-simulator", "matter")
	}
	return filepath.Join(os.TempDir(), "device-simulator-matter-bridge")
}
