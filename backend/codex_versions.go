package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"slices"
	"strings"
	"sync"
	"time"
)

type CodexVersions struct {
	Latest    string        `json:"latest"`
	Versions  []string      `json:"versions"`
	Installed []string      `json:"installed"`
	System    []SystemCodex `json:"system"`
	Error     string        `json:"error,omitempty"`
}

type codexCatalogCache struct {
	mu       sync.Mutex
	registry string
	until    time.Time
	latest   string
	versions []string
}

func codexRegistry() string {
	return envOr("CODEX_NPM_REGISTRY", "https://registry.npmjs.org/")
}

func fetchCodexVersions(ctx context.Context, registry string) (string, []string, error) {
	endpoint := strings.TrimRight(registry, "/") + "/@openai%2fcodex"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", nil, err
	}
	// npm's abbreviated packument includes the bin field without every release's README.
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("npm registry: HTTP %d", resp.StatusCode)
	}
	var packument struct {
		Tags struct {
			Latest string `json:"latest"`
		} `json:"dist-tags"`
		Versions map[string]struct {
			Bin map[string]string `json:"bin"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&packument); err != nil {
		return "", nil, fmt.Errorf("invalid npm version metadata: %w", err)
	}
	versions := make([]string, 0)
	latest := ""
	for version, info := range packument.Versions {
		// The same package name also publishes per-platform binaries without a CLI launcher.
		if info.Bin["codex"] == "" || !validCodexReleaseVersion(version) {
			continue
		}
		versions = append(versions, version)
		if version == packument.Tags.Latest {
			latest = version
		}
	}
	if len(versions) == 0 {
		return "", nil, errors.New("npm registry returned no Codex CLI releases")
	}
	return latest, versions, nil
}

// Compare dot-separated SemVer identifiers. Comparing numeric lengths also
// works for historical timestamp versions with more digits than a 32-bit int.
func compareVersionIdentifier(a, b string) int {
	an := strings.Trim(a, "0123456789") == ""
	bn := strings.Trim(b, "0123456789") == ""
	switch {
	case an && bn && len(a) != len(b):
		return cmp.Compare(len(a), len(b))
	case an && !bn:
		return -1
	case bn && !an:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func compareCodexVersions(a, b string) int {
	v1, _, _ := strings.Cut(a, "+")
	v2, _, _ := strings.Cut(b, "+")
	core1, pre1, _ := strings.Cut(v1, "-")
	core2, pre2, _ := strings.Cut(v2, "-")
	if diff := slices.CompareFunc(strings.Split(core1, "."), strings.Split(core2, "."), compareVersionIdentifier); diff != 0 {
		return diff
	}
	if pre1 == "" && pre2 != "" {
		return 1
	}
	if pre2 == "" && pre1 != "" {
		return -1
	}
	if diff := slices.CompareFunc(strings.Split(pre1, "."), strings.Split(pre2, "."), compareVersionIdentifier); diff != 0 {
		return diff
	}
	return strings.Compare(a, b) // Stable tie-break for distinct build metadata.
}

// Re-read the local list for every request. Failed refreshes retain the last
// successful remote list for this registry and can be retried immediately.
func (m *CodexManager) Versions(ctx context.Context) CodexVersions {
	registry := codexRegistry()
	c := &m.catalog
	remoteCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	c.mu.Lock()
	if c.registry != registry {
		c.registry, c.latest, c.versions, c.until = registry, "", nil, time.Time{}
	}
	var remoteErr error
	if time.Now().After(c.until) {
		latest, versions, err := fetchCodexVersions(remoteCtx, registry)
		if err != nil {
			remoteErr = err
		} else {
			c.latest, c.versions, c.until = latest, versions, time.Now().Add(10*time.Minute)
		}
	}
	result := CodexVersions{Latest: c.latest, Versions: append([]string{}, c.versions...), Installed: []string{}}
	c.mu.Unlock()

	local := m.LocalVersions(ctx, false)
	result.System, result.Installed = local.System, local.Installed
	var localErr error
	if local.Error != "" {
		localErr = errors.New(local.Error)
	}
	result.Versions = append(result.Versions, result.Installed...)
	slices.SortFunc(result.Versions, func(a, b string) int { return compareCodexVersions(b, a) })
	result.Versions = slices.Compact(result.Versions)
	if err := errors.Join(remoteErr, localErr); err != nil {
		result.Error = err.Error()
	}
	return result
}
