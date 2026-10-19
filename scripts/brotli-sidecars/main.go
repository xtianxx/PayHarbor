// Command brotli-sidecars regenerates and verifies the pre-compressed
// `.fiber.br` asset sidecars that ship next to `views/` and `static/` assets.
//
// The gateway serves those sidecars in non-development environments, and
// TestFiberBrotliSidecarsMatchSources enforces that every sidecar decompresses
// to exactly its source file. Whenever an asset changes (for example after
// `npm run build:css`), regenerate its sidecar with this command.
//
// Usage:
//
//	go run ./scripts/brotli-sidecars --verify            # check every sidecar
//	go run ./scripts/brotli-sidecars views/assets/tailwind.css
//	go run ./scripts/brotli-sidecars --all               # regenerate everything
//
// Quality 11 matches the committed sidecars' size profile. The exact bytes can
// differ from a sidecar produced by another brotli implementation while decoding
// to the same source, so regenerate only the assets you actually changed
// (`TestFiberBrotliSidecarsMatchSources` checks decode equality, not byte equality).
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andybalholm/brotli"
)

const (
	sidecarSuffix = ".fiber.br"
	quality       = 11
	assetRoots    = "views,static"
)

func compress(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := brotli.NewWriterLevel(&buffer, quality)
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func decompress(data []byte) ([]byte, error) {
	return io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
}

func assetsRoots() []string {
	return strings.Split(assetRoots, ",")
}

// collectSidecars walks the asset roots and returns every sidecar path.
func collectSidecars() ([]string, error) {
	var paths []string
	for _, root := range assetsRoots() {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !info.IsDir() && strings.HasSuffix(path, sidecarSuffix) {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func regenerate(sourcePath string) error {
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	compressed, err := compress(source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(sourcePath+sidecarSuffix, compressed, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s%s source=%d compressed=%d quality=%d\n",
		sourcePath, sidecarSuffix, len(source), len(compressed), quality)
	return nil
}

func verify(sidecarPath string) error {
	sourcePath := strings.TrimSuffix(sidecarPath, sidecarSuffix)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	compressed, err := os.ReadFile(sidecarPath)
	if err != nil {
		return err
	}
	decoded, err := decompress(compressed)
	if err != nil {
		return fmt.Errorf("%s: %w", sidecarPath, err)
	}
	if !bytes.Equal(decoded, source) {
		return fmt.Errorf("%s does not decompress to %s", sidecarPath, sourcePath)
	}
	return nil
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: brotli-sidecars --verify | --all | <asset files...>")
		os.Exit(2)
	}

	switch args[0] {
	case "--verify":
		sidecars, err := collectSidecars()
		if err != nil {
			fmt.Fprintln(os.Stderr, "walk assets:", err)
			os.Exit(1)
		}
		if len(sidecars) == 0 {
			fmt.Fprintln(os.Stderr, "no .fiber.br sidecars found")
			os.Exit(1)
		}
		failures := 0
		for _, sidecar := range sidecars {
			if err := verify(sidecar); err != nil {
				fmt.Fprintln(os.Stderr, "MISMATCH:", err)
				failures++
			}
		}
		fmt.Printf("verified %d sidecars, mismatches=%d\n", len(sidecars), failures)
		if failures > 0 {
			os.Exit(1)
		}
	case "--all":
		sidecars, err := collectSidecars()
		if err != nil {
			fmt.Fprintln(os.Stderr, "walk assets:", err)
			os.Exit(1)
		}
		failures := 0
		for _, sidecar := range sidecars {
			sourcePath := strings.TrimSuffix(sidecar, sidecarSuffix)
			if err := regenerate(sourcePath); err != nil {
				fmt.Fprintln(os.Stderr, "regenerate:", err)
				failures++
				continue
			}
			if err := verify(sourcePath + sidecarSuffix); err != nil {
				fmt.Fprintln(os.Stderr, "verify:", err)
				failures++
			}
		}
		if failures > 0 {
			fmt.Fprintf(os.Stderr, "%d assets failed\n", failures)
			os.Exit(1)
		}
	default:
		failures := 0
		for _, sourcePath := range args {
			if _, err := os.Stat(sourcePath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintln(os.Stderr, "missing asset:", sourcePath)
				} else {
					fmt.Fprintln(os.Stderr, "stat:", err)
				}
				failures++
				continue
			}
			if err := regenerate(sourcePath); err != nil {
				fmt.Fprintln(os.Stderr, "regenerate:", err)
				failures++
				continue
			}
			if err := verify(sourcePath + sidecarSuffix); err != nil {
				fmt.Fprintln(os.Stderr, "verify:", err)
				failures++
			}
		}
		if failures > 0 {
			os.Exit(1)
		}
	}
}
