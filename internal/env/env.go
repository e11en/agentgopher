// Package env resolves the environment injected into the sandbox.
package env

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ParseFile reads a dotenv file. A missing file yields an empty map.
func ParseFile(path string) (map[string]string, error) {
	vars := map[string]string{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return vars, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch {
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			value = unescape(value[1 : len(value)-1])
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			value = value[1 : len(value)-1]
		}
		vars[key] = value
	}
	return vars, nil
}

func unescape(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\\`, `\`)
	return r.Replace(s)
}

// Resolve reads <repoDir>/.agentgopher/.env. Only keys declared there are
// returned; an empty value falls back to the process environment, and a key
// with no value anywhere is dropped.
func Resolve(repoDir string) (map[string]string, error) {
	declared, err := ParseFile(filepath.Join(repoDir, ".agentgopher", ".env"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range declared {
		if v == "" {
			v = os.Getenv(k)
		}
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// MergeProviderEnv layers provider env over the resolved env. Agent and
// sandbox provider env must not share keys.
func MergeProviderEnv(resolved, agentEnv, sandboxEnv map[string]string) (map[string]string, error) {
	var overlap []string
	for k := range agentEnv {
		if _, ok := sandboxEnv[k]; ok {
			overlap = append(overlap, k)
		}
	}
	if len(overlap) > 0 {
		sort.Strings(overlap)
		return nil, fmt.Errorf("overlapping env keys between agent provider and sandbox provider: %s", strings.Join(overlap, ", "))
	}
	out := map[string]string{}
	for _, m := range []map[string]string{resolved, sandboxEnv, agentEnv} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out, nil
}
