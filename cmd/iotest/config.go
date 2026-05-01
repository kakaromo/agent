package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
)

// Config is the top-level JSON input.
type Config struct {
	Threads         []ThreadDef `json:"threads"`
	DurationSeconds int         `json:"duration_seconds"` // 0 = run until all threads complete
	SyncStart       bool        `json:"sync_start"`       // true = barrier before start
}

// ThreadDef defines one thread's command sequence.
type ThreadDef struct {
	Name     string    `json:"name"`
	Commands []Command `json:"commands"`
}

// Command is a single operation.
type Command struct {
	Op string `json:"op"`

	// fd name — for multi-file-handle support
	// open: assigns this name (default: basename of path)
	// read/write/fsync/close/verify: selects which fd to use (default: last opened)
	Fd string `json:"fd,omitempty"`

	// open / unlink / stat / mkdir / truncate / sysfs_write / sysfs_read / rename
	Path    string `json:"path,omitempty"`
	NewPath string `json:"new_path,omitempty"` // rename target path
	Flags   string `json:"flags,omitempty"`    // "O_WRONLY|O_CREATE|O_DIRECT"
	Value   string `json:"value,omitempty"`    // sysfs_write value

	// read / write / verify
	Offset  FlexValue `json:"offset,omitempty"`  // supports "4k", "{{i*4096}}", "random:0-1m"
	BS      FlexValue `json:"bs,omitempty"`      // "4k", "random:4k,8k,16k"
	Count   int       `json:"count,omitempty"`   // number of I/O ops
	Pattern string    `json:"pattern,omitempty"` // "zero", "random", "byte:0xFF"

	// truncate / fallocate
	Size FlexValue `json:"size,omitempty"`

	// create_files / delete_pattern
	Dir    string `json:"dir,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	Rule   string `json:"rule,omitempty"`   // "odd", "even", "random_half", "all"
	Blocks int    `json:"blocks,omitempty"` // blocks per file for create_files

	// sleep
	Ms int `json:"ms,omitempty"`

	// shell
	Cmd string `json:"cmd,omitempty"`

	// loop
	LoopCount    int       `json:"loop_count,omitempty"`
	LoopDuration int       `json:"loop_duration,omitempty"` // seconds — loop for N seconds instead of count
	Commands     []Command `json:"commands,omitempty"`      // nested commands for loop/if
	Items        []string  `json:"items,omitempty"`         // for {{item}} in loop

	// if
	Condition string    `json:"condition,omitempty"`
	Then      []Command `json:"then,omitempty"`
	Else      []Command `json:"else,omitempty"`
}

// FlexValue handles values that can be numeric, string with unit suffix, or template expressions.
type FlexValue struct {
	Raw string // original string value
}

func (f *FlexValue) UnmarshalJSON(data []byte) error {
	// Try as number first
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		f.Raw = n.String()
		return nil
	}
	// Then as string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.Raw = s
		return nil
	}
	return fmt.Errorf("FlexValue: cannot parse %s", string(data))
}

func (f FlexValue) MarshalJSON() ([]byte, error) {
	if f.Raw == "" {
		return []byte("0"), nil
	}
	return json.Marshal(f.Raw)
}

// Resolve evaluates the FlexValue with the given loop variables.
// Supports:
//   - plain values: "4k", "100m", "4096"
//   - template: "{{i*4096}}"
//   - random list: "random:4k,8k,16k,64k" → picks one randomly
//   - random range: "random:0-1m" → picks random value in [0, 1m)
//   - seq list: "seq:4k,8k,16k" → picks by loop index (round-robin)
func (f FlexValue) Resolve(vars map[string]interface{}) int64 {
	if f.Raw == "" {
		return 0
	}
	s := resolveTemplate(f.Raw, vars)
	return resolveFlexString(s, vars)
}

// ResolveWithActual returns both the resolved value and a human-readable description of what was chosen.
func (f FlexValue) ResolveWithActual(vars map[string]interface{}) (int64, string) {
	if f.Raw == "" {
		return 0, "0"
	}
	s := resolveTemplate(f.Raw, vars)
	val := resolveFlexString(s, vars)
	return val, formatSize(val)
}

func resolveFlexString(s string, vars map[string]interface{}) int64 {
	// random:4k,8k,16k,64k → pick one randomly
	if strings.HasPrefix(s, "random:") {
		spec := strings.TrimPrefix(s, "random:")

		// Check if it's a range: "0-1m"
		if parts := strings.SplitN(spec, "-", 2); len(parts) == 2 && !strings.Contains(spec, ",") {
			lo := parseSize(parts[0])
			hi := parseSize(parts[1])
			if hi <= lo {
				return lo
			}
			return lo + rand.Int63n(hi-lo)
		}

		// Comma-separated list: "4k,8k,16k"
		choices := strings.Split(spec, ",")
		if len(choices) == 0 {
			return 0
		}
		pick := choices[rand.Intn(len(choices))]
		return parseSize(strings.TrimSpace(pick))
	}

	// seq:4k,8k,16k → pick by loop index (round-robin)
	if strings.HasPrefix(s, "seq:") {
		spec := strings.TrimPrefix(s, "seq:")
		choices := strings.Split(spec, ",")
		if len(choices) == 0 {
			return 0
		}
		i := 0
		if v, ok := vars["i"]; ok {
			switch vi := v.(type) {
			case int:
				i = vi
			case int64:
				i = int(vi)
			}
		}
		pick := choices[i%len(choices)]
		return parseSize(strings.TrimSpace(pick))
	}

	return parseSize(s)
}

func formatSize(bytes int64) string {
	if bytes == 0 {
		return "0"
	}
	if bytes >= 1024*1024*1024 && bytes%(1024*1024*1024) == 0 {
		return fmt.Sprintf("%dg", bytes/(1024*1024*1024))
	}
	if bytes >= 1024*1024 && bytes%(1024*1024) == 0 {
		return fmt.Sprintf("%dm", bytes/(1024*1024))
	}
	if bytes >= 1024 && bytes%1024 == 0 {
		return fmt.Sprintf("%dk", bytes/1024)
	}
	return strconv.FormatInt(bytes, 10)
}

// parseSize parses a size string with optional suffix (k, m, g).
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	multiplier := int64(1)
	lower := strings.ToLower(s)
	if strings.HasSuffix(lower, "k") {
		multiplier = 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(lower, "m") {
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(lower, "g") {
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	}

	// Try parsing as float to handle "1.5m" etc
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * float64(multiplier)))
}
