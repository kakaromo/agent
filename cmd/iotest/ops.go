package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// fileHandle tracks an open file descriptor with its name.
type fileHandle struct {
	fd   *os.File
	path string
}

// threadState holds runtime state for a thread.
type threadState struct {
	threadName string
	fds        map[string]*fileHandle // named file handles (multi-fd support)
	activeFd   string                 // name of the currently active fd
	stepNum    int                    // current step number (1-based)
	vars       map[string]interface{} // loop variables, last_error, etc.
	result     *ThreadResult
}

func newThreadState(name string) *threadState {
	return &threadState{
		threadName: name,
		fds:        make(map[string]*fileHandle),
		vars:       make(map[string]interface{}),
		result: &ThreadResult{
			Name: name,
		},
	}
}

// getFd returns the file handle by name. If name is empty, returns the active fd.
func (ts *threadState) getFd(name string) *fileHandle {
	if name == "" {
		name = ts.activeFd
	}
	return ts.fds[name]
}

// executeCommand dispatches to the appropriate op handler.
func (ts *threadState) executeCommand(cmd Command) OpResult {
	ts.stepNum++
	start := time.Now()

	var opResult OpResult
	opResult.Op = cmd.Op

	switch cmd.Op {
	case "open":
		opResult = ts.execOpen(cmd)
	case "close":
		opResult = ts.execClose(cmd)
	case "read":
		opResult = ts.execRead(cmd)
	case "write":
		opResult = ts.execWrite(cmd)
	case "fsync":
		opResult = ts.execFsync(cmd)
	case "fdatasync":
		opResult = ts.execFdatasync(cmd)
	case "stat":
		opResult = ts.execStat(cmd)
	case "truncate":
		opResult = ts.execTruncate(cmd)
	case "unlink":
		opResult = ts.execUnlink(cmd)
	case "mkdir":
		opResult = ts.execMkdir(cmd)
	case "create_files":
		opResult = ts.execCreateFiles(cmd)
	case "delete_pattern":
		opResult = ts.execDeletePattern(cmd)
	case "verify":
		opResult = ts.execVerify(cmd)
	case "rename":
		opResult = ts.execRename(cmd)
	case "fallocate":
		opResult = ts.execFallocate(cmd)
	case "sysfs_write":
		opResult = ts.execSysfsWrite(cmd)
	case "sysfs_read":
		opResult = ts.execSysfsRead(cmd)
	case "shell":
		opResult = ts.execShell(cmd)
	case "sleep":
		opResult = ts.execSleep(cmd)
	case "loop":
		opResult = ts.execLoop(cmd)
	case "if":
		opResult = ts.execIf(cmd)
	default:
		opResult.Status = "error"
		opResult.Error = fmt.Sprintf("unknown op: %s", cmd.Op)
	}

	if opResult.Duration == 0 {
		opResult.Duration = time.Since(start).Nanoseconds()
	}

	// Update last_error
	if opResult.Status == "error" {
		ts.vars["last_error"] = opResult.Error
	} else {
		ts.vars["last_error"] = ""
	}

	// Emit progress
	evt := ProgressEvent{
		Thread:   ts.threadName,
		Step:     ts.stepNum,
		Op:       cmd.Op,
		Status:   opResult.Status,
		Duration: opResult.Duration,
	}
	if opResult.Error != "" {
		evt.Error = opResult.Error
	}
	if opResult.Bytes > 0 {
		evt.Bytes = opResult.Bytes
	}
	if opResult.Path != "" {
		evt.Path = opResult.Path
	}
	// Don't emit for loop/if (they emit their own sub-progress)
	if cmd.Op != "loop" && cmd.Op != "if" {
		emitProgress(evt)
	}

	// Update thread result
	ts.result.TotalOps++
	ts.result.TotalBytes += opResult.Bytes
	if opResult.Status == "error" {
		ts.result.Errors++
	}

	return opResult
}

func (ts *threadState) execOpen(cmd Command) OpResult {
	path := resolveTemplate(cmd.Path, ts.vars)
	flags := parseOpenFlags(cmd.Flags)

	fd, err := os.OpenFile(path, flags, 0666)
	if err != nil {
		return OpResult{Op: "open", Status: "error", Error: err.Error(), Path: path}
	}

	// Assign fd name: explicit "fd" field, or basename of path
	name := cmd.Fd
	if name == "" {
		name = filepath.Base(path)
	}
	ts.fds[name] = &fileHandle{fd: fd, path: path}
	ts.activeFd = name
	return OpResult{Op: "open", Status: "ok", Path: path, Value: name}
}

func (ts *threadState) execClose(cmd Command) OpResult {
	fh := ts.getFd(cmd.Fd)
	if fh == nil {
		return OpResult{Op: "close", Status: "error", Error: "no open file: " + cmd.Fd}
	}
	path := fh.path
	name := cmd.Fd
	if name == "" {
		name = ts.activeFd
	}
	err := fh.fd.Close()
	delete(ts.fds, name)
	// Set activeFd to another open fd if available
	if name == ts.activeFd {
		ts.activeFd = ""
		for k := range ts.fds {
			ts.activeFd = k
			break
		}
	}
	if err != nil {
		return OpResult{Op: "close", Status: "error", Error: err.Error(), Path: path}
	}
	return OpResult{Op: "close", Status: "ok", Path: path}
}

func (ts *threadState) execRead(cmd Command) OpResult {
	fh := ts.getFd(cmd.Fd)
	if fh == nil {
		return OpResult{Op: "read", Status: "error", Error: "no open file: " + cmd.Fd}
	}
	offset, offActual := cmd.Offset.ResolveWithActual(ts.vars)
	bs, bsActual := cmd.BS.ResolveWithActual(ts.vars)
	if bs <= 0 {
		bs = 4096
		bsActual = "4k"
	}
	count := cmd.Count
	if count <= 0 {
		count = 1
	}

	// Build actual string when random/seq was used
	actual := buildActual(cmd, offActual, bsActual, count)

	buf := make([]byte, bs)
	var totalBytes int64
	start := time.Now()

	for c := 0; c < count; c++ {
		n, err := fh.fd.ReadAt(buf, offset+int64(c)*bs)
		totalBytes += int64(n)
		if err != nil && err.Error() != "EOF" {
			return OpResult{Op: "read", Status: "error", Error: err.Error(),
				Offset: offset, BS: bs, Bytes: totalBytes, Duration: time.Since(start).Nanoseconds(), Actual: actual}
		}
	}
	return OpResult{Op: "read", Status: "ok", Offset: offset, BS: bs, Bytes: totalBytes,
		Duration: time.Since(start).Nanoseconds(), Actual: actual}
}

func (ts *threadState) execWrite(cmd Command) OpResult {
	fh := ts.getFd(cmd.Fd)
	if fh == nil {
		return OpResult{Op: "write", Status: "error", Error: "no open file: " + cmd.Fd}
	}
	offset, offActual := cmd.Offset.ResolveWithActual(ts.vars)
	bs, bsActual := cmd.BS.ResolveWithActual(ts.vars)
	if bs <= 0 {
		bs = 4096
		bsActual = "4k"
	}
	count := cmd.Count
	if count <= 0 {
		count = 1
	}

	actual := buildActual(cmd, offActual, bsActual, count)

	buf := makePattern(cmd.Pattern, int(bs))
	var totalBytes int64
	start := time.Now()

	for c := 0; c < count; c++ {
		n, err := fh.fd.WriteAt(buf, offset+int64(c)*bs)
		totalBytes += int64(n)
		if err != nil {
			return OpResult{Op: "write", Status: "error", Error: err.Error(),
				Offset: offset, BS: bs, Bytes: totalBytes, Duration: time.Since(start).Nanoseconds(),
				Pattern: cmd.Pattern, Actual: actual}
		}
	}
	return OpResult{Op: "write", Status: "ok", Offset: offset, BS: bs, Bytes: totalBytes,
		Duration: time.Since(start).Nanoseconds(), Pattern: cmd.Pattern, Actual: actual}
}

func (ts *threadState) execFsync(cmd Command) OpResult {
	fh := ts.getFd(cmd.Fd)
	if fh == nil {
		return OpResult{Op: "fsync", Status: "error", Error: "no open file: " + cmd.Fd}
	}
	err := fh.fd.Sync()
	if err != nil {
		return OpResult{Op: "fsync", Status: "error", Error: err.Error()}
	}
	return OpResult{Op: "fsync", Status: "ok"}
}

func (ts *threadState) execFdatasync(cmd Command) OpResult {
	fh := ts.getFd(cmd.Fd)
	if fh == nil {
		return OpResult{Op: "fdatasync", Status: "error", Error: "no open file: " + cmd.Fd}
	}
	err := fh.fd.Sync()
	if err != nil {
		return OpResult{Op: "fdatasync", Status: "error", Error: err.Error()}
	}
	return OpResult{Op: "fdatasync", Status: "ok"}
}

func (ts *threadState) execStat(cmd Command) OpResult {
	path := resolveTemplate(cmd.Path, ts.vars)
	info, err := os.Stat(path)
	if err != nil {
		return OpResult{Op: "stat", Status: "error", Error: err.Error(), Path: path}
	}
	return OpResult{Op: "stat", Status: "ok", Path: path,
		Value: fmt.Sprintf("size=%d,mode=%s,mtime=%s", info.Size(), info.Mode(), info.ModTime().Format(time.RFC3339))}
}

func (ts *threadState) execTruncate(cmd Command) OpResult {
	path := resolveTemplate(cmd.Path, ts.vars)
	size := cmd.Size.Resolve(ts.vars)
	err := os.Truncate(path, size)
	if err != nil {
		return OpResult{Op: "truncate", Status: "error", Error: err.Error(), Path: path}
	}
	return OpResult{Op: "truncate", Status: "ok", Path: path}
}

func (ts *threadState) execUnlink(cmd Command) OpResult {
	path := resolveTemplate(cmd.Path, ts.vars)
	err := os.Remove(path)
	if err != nil {
		return OpResult{Op: "unlink", Status: "error", Error: err.Error(), Path: path}
	}
	return OpResult{Op: "unlink", Status: "ok", Path: path}
}

func (ts *threadState) execMkdir(cmd Command) OpResult {
	path := resolveTemplate(cmd.Path, ts.vars)
	err := os.MkdirAll(path, 0755)
	if err != nil {
		return OpResult{Op: "mkdir", Status: "error", Error: err.Error(), Path: path}
	}
	return OpResult{Op: "mkdir", Status: "ok", Path: path}
}

func (ts *threadState) execCreateFiles(cmd Command) OpResult {
	dir := resolveTemplate(cmd.Dir, ts.vars)
	prefix := cmd.Prefix
	if prefix == "" {
		prefix = "file_"
	}
	count := cmd.Count
	if count <= 0 {
		count = 10
	}
	bs := cmd.BS.Resolve(ts.vars)
	if bs <= 0 {
		bs = 4096
	}
	blocks := cmd.Blocks
	if blocks <= 0 {
		blocks = 1
	}

	_ = os.MkdirAll(dir, 0755)
	buf := make([]byte, bs)
	var totalBytes int64
	start := time.Now()

	for i := 1; i <= count; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%s%d", prefix, i))
		f, err := os.Create(path)
		if err != nil {
			return OpResult{Op: "create_files", Status: "error", Error: err.Error(),
				Bytes: totalBytes, Duration: time.Since(start).Nanoseconds()}
		}
		for b := 0; b < blocks; b++ {
			n, _ := f.Write(buf)
			totalBytes += int64(n)
		}
		f.Close()

		// Emit periodic progress
		if i%10 == 0 || i == count {
			emitProgress(ProgressEvent{
				Thread: ts.threadName, Step: ts.stepNum, Op: "create_files",
				Status: "running", Progress: i, Total: count,
			})
		}
	}

	return OpResult{Op: "create_files", Status: "ok", Path: dir,
		Bytes: totalBytes, Duration: time.Since(start).Nanoseconds(),
		Count: count}
}

func (ts *threadState) execDeletePattern(cmd Command) OpResult {
	dir := resolveTemplate(cmd.Dir, ts.vars)
	prefix := cmd.Prefix
	if prefix == "" {
		prefix = "file_"
	}
	count := cmd.Count
	if count <= 0 {
		count = 10
	}
	rule := cmd.Rule
	if rule == "" {
		rule = "odd"
	}

	var deleted int
	start := time.Now()

	for i := 1; i <= count; i++ {
		shouldDelete := false
		switch rule {
		case "odd":
			shouldDelete = i%2 == 1
		case "even":
			shouldDelete = i%2 == 0
		case "all":
			shouldDelete = true
		case "random_half":
			n, _ := rand.Int(rand.Reader, big.NewInt(2))
			shouldDelete = n.Int64() == 1
		}

		if shouldDelete {
			path := filepath.Join(dir, fmt.Sprintf("%s%d", prefix, i))
			if os.Remove(path) == nil {
				deleted++
			}
		}

		if i%10 == 0 || i == count {
			emitProgress(ProgressEvent{
				Thread: ts.threadName, Step: ts.stepNum, Op: "delete_pattern",
				Status: "running", Progress: i, Total: count,
			})
		}
	}

	return OpResult{Op: "delete_pattern", Status: "ok", Path: dir,
		Duration: time.Since(start).Nanoseconds(),
		Value: fmt.Sprintf("deleted=%d,rule=%s", deleted, rule),
		Count: deleted}
}

// verify reads data and compares against expected pattern.
// Fails if any byte mismatch. Reports mismatch offset in error.
func (ts *threadState) execVerify(cmd Command) OpResult {
	fh := ts.getFd(cmd.Fd)
	if fh == nil {
		return OpResult{Op: "verify", Status: "error", Error: "no open file: " + cmd.Fd}
	}
	offset, offActual := cmd.Offset.ResolveWithActual(ts.vars)
	bs, bsActual := cmd.BS.ResolveWithActual(ts.vars)
	if bs <= 0 {
		bs = 4096
		bsActual = "4k"
	}
	count := cmd.Count
	if count <= 0 {
		count = 1
	}

	actual := buildActual(cmd, offActual, bsActual, count)
	expected := makePattern(cmd.Pattern, int(bs))
	buf := make([]byte, bs)
	var totalBytes int64
	start := time.Now()

	for c := 0; c < count; c++ {
		curOff := offset + int64(c)*bs
		n, err := fh.fd.ReadAt(buf[:bs], curOff)
		totalBytes += int64(n)
		if err != nil && err.Error() != "EOF" {
			return OpResult{Op: "verify", Status: "error", Error: fmt.Sprintf("read error at offset %d: %s", curOff, err),
				Offset: offset, BS: bs, Bytes: totalBytes, Duration: time.Since(start).Nanoseconds(), Actual: actual}
		}
		// Compare
		for j := 0; j < n; j++ {
			if buf[j] != expected[j] {
				return OpResult{Op: "verify", Status: "error",
					Error: fmt.Sprintf("mismatch at offset %d+%d: expected 0x%02X got 0x%02X", curOff, j, expected[j], buf[j]),
					Offset: offset, BS: bs, Bytes: totalBytes, Duration: time.Since(start).Nanoseconds(),
					Pattern: cmd.Pattern, Actual: actual}
			}
		}
	}
	return OpResult{Op: "verify", Status: "ok", Offset: offset, BS: bs, Bytes: totalBytes,
		Duration: time.Since(start).Nanoseconds(), Pattern: cmd.Pattern, Actual: actual,
		Value: fmt.Sprintf("verified %d blocks", count)}
}

func (ts *threadState) execRename(cmd Command) OpResult {
	oldPath := resolveTemplate(cmd.Path, ts.vars)
	newPath := resolveTemplate(cmd.NewPath, ts.vars)
	if newPath == "" {
		return OpResult{Op: "rename", Status: "error", Error: "new_path required", Path: oldPath}
	}
	err := os.Rename(oldPath, newPath)
	if err != nil {
		return OpResult{Op: "rename", Status: "error", Error: err.Error(), Path: oldPath}
	}
	return OpResult{Op: "rename", Status: "ok", Path: oldPath, Value: newPath}
}

func (ts *threadState) execFallocate(cmd Command) OpResult {
	fh := ts.getFd(cmd.Fd)
	if fh == nil {
		// If no fd, use path to create file with preallocated size
		path := resolveTemplate(cmd.Path, ts.vars)
		size := cmd.Size.Resolve(ts.vars)
		f, err := os.Create(path)
		if err != nil {
			return OpResult{Op: "fallocate", Status: "error", Error: err.Error(), Path: path}
		}
		if err := f.Truncate(size); err != nil {
			f.Close()
			return OpResult{Op: "fallocate", Status: "error", Error: err.Error(), Path: path}
		}
		f.Close()
		return OpResult{Op: "fallocate", Status: "ok", Path: path, Bytes: size}
	}
	size := cmd.Size.Resolve(ts.vars)
	if err := fh.fd.Truncate(size); err != nil {
		return OpResult{Op: "fallocate", Status: "error", Error: err.Error(), Path: fh.path}
	}
	return OpResult{Op: "fallocate", Status: "ok", Path: fh.path, Bytes: size}
}

func (ts *threadState) execSysfsWrite(cmd Command) OpResult {
	path := resolveTemplate(cmd.Path, ts.vars)
	value := resolveTemplate(cmd.Value, ts.vars)
	return writeSysfs(path, value)
}

func (ts *threadState) execSysfsRead(cmd Command) OpResult {
	path := resolveTemplate(cmd.Path, ts.vars)
	return OpResult(readSysfsOp(path))
}

func (ts *threadState) execShell(cmd Command) OpResult {
	cmdStr := resolveTemplate(cmd.Cmd, ts.vars)
	out, err := exec.Command("sh", "-c", cmdStr).CombinedOutput()
	if err != nil {
		return OpResult{Op: "shell", Status: "error", Error: err.Error(), Value: string(out)}
	}
	return OpResult{Op: "shell", Status: "ok", Value: strings.TrimSpace(string(out))}
}

func (ts *threadState) execSleep(cmd Command) OpResult {
	ms := cmd.Ms
	if ms <= 0 {
		ms = 1000
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return OpResult{Op: "sleep", Status: "ok"}
}

func (ts *threadState) execLoop(cmd Command) OpResult {
	useDuration := cmd.LoopDuration > 0
	count := cmd.LoopCount
	if count <= 0 {
		count = cmd.Count
	}
	if !useDuration && count <= 0 {
		count = 1
	}

	start := time.Now()
	var deadline time.Time
	if useDuration {
		deadline = start.Add(time.Duration(cmd.LoopDuration) * time.Second)
	}

	var loopOps []OpResult
	var totalBytes int64
	var minNs, maxNs int64
	minNs = int64(^uint64(0) >> 1)
	var totalNs int64

	savedStepNum := ts.stepNum
	loopStep := savedStepNum
	i := 0

	for {
		// Check termination condition
		if useDuration {
			if time.Now().After(deadline) {
				break
			}
		} else {
			if i >= count {
				break
			}
		}

		ts.vars["i"] = i
		if i < len(cmd.Items) {
			ts.vars["item"] = cmd.Items[i]
		}

		for _, subcmd := range cmd.Commands {
			// Check deadline mid-loop for duration mode
			if useDuration && time.Now().After(deadline) {
				break
			}
			ts.stepNum = loopStep
			iterStart := time.Now()
			opResult := ts.executeCommand(subcmd)
			iterDur := time.Since(iterStart).Nanoseconds()

			loopOps = append(loopOps, OpResult{
				Op: opResult.Op, Status: opResult.Status, Error: opResult.Error,
				Offset: opResult.Offset, BS: opResult.BS, Bytes: opResult.Bytes,
				Duration: iterDur, Pattern: opResult.Pattern, Actual: opResult.Actual,
			})
			totalBytes += opResult.Bytes
			totalNs += iterDur
			if iterDur < minNs {
				minNs = iterDur
			}
			if iterDur > maxNs {
				maxNs = iterDur
			}
		}

		// Emit loop progress periodically
		total := count
		if useDuration {
			total = 0 // unknown total for duration mode
		}
		if i%10 == 0 || (!useDuration && i == count-1) {
			emitProgress(ProgressEvent{
				Thread: ts.threadName, Step: loopStep, Op: "loop",
				Iter: i + 1, Total: total, OpInner: func() string {
					if len(cmd.Commands) > 0 {
						return cmd.Commands[0].Op
					}
					return ""
				}(),
				Status: "running",
			})
		}
		i++
	}

	ts.stepNum = savedStepNum
	dur := time.Since(start).Nanoseconds()
	avgNs := int64(0)
	if len(loopOps) > 0 {
		avgNs = totalNs / int64(len(loopOps))
	}
	if minNs == int64(^uint64(0)>>1) {
		minNs = 0
	}

	actualCount := i
	return OpResult{
		Op: "loop", Status: "ok", Count: actualCount,
		Duration: dur, LoopBytes: totalBytes,
		AvgNs: avgNs, MinNs: minNs, MaxNs: maxNs,
		LoopOps: loopOps,
	}
}

func (ts *threadState) execIf(cmd Command) OpResult {
	condStr := resolveTemplate(cmd.Condition, ts.vars)
	result := evaluateCondition(condStr, ts.vars)

	var branch []Command
	branchName := "else"
	if result {
		branch = cmd.Then
		branchName = "then"
	} else {
		branch = cmd.Else
	}

	if len(branch) == 0 {
		return OpResult{Op: "if", Status: "skipped", Value: branchName}
	}

	savedStep := ts.stepNum
	for _, subcmd := range branch {
		ts.executeCommand(subcmd)
	}
	ts.stepNum = savedStep

	return OpResult{Op: "if", Status: "ok", Value: branchName}
}

// Helper functions

// buildActual creates a human-readable string of resolved values when random/seq was used.
// Only includes fields that had random/seq specification.
func buildActual(cmd Command, offActual, bsActual string, count int) string {
	hasRandom := strings.HasPrefix(cmd.Offset.Raw, "random:") || strings.HasPrefix(cmd.Offset.Raw, "seq:") ||
		strings.HasPrefix(cmd.BS.Raw, "random:") || strings.HasPrefix(cmd.BS.Raw, "seq:")
	if !hasRandom {
		return ""
	}
	parts := []string{}
	if strings.HasPrefix(cmd.Offset.Raw, "random:") || strings.HasPrefix(cmd.Offset.Raw, "seq:") {
		parts = append(parts, "offset="+offActual)
	}
	if strings.HasPrefix(cmd.BS.Raw, "random:") || strings.HasPrefix(cmd.BS.Raw, "seq:") {
		parts = append(parts, "bs="+bsActual)
	}
	return strings.Join(parts, ",")
}

func parseOpenFlags(flagStr string) int {
	if flagStr == "" {
		return os.O_RDONLY
	}
	flags := 0
	parts := strings.Split(strings.ToUpper(flagStr), "|")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch p {
		case "O_RDONLY":
			flags |= os.O_RDONLY
		case "O_WRONLY":
			flags |= os.O_WRONLY
		case "O_RDWR":
			flags |= os.O_RDWR
		case "O_CREATE":
			flags |= os.O_CREATE
		case "O_TRUNC":
			flags |= os.O_TRUNC
		case "O_APPEND":
			flags |= os.O_APPEND
		case "O_SYNC":
			flags |= os.O_SYNC
		case "O_DIRECT":
			// O_DIRECT = 0x4000 on Linux
			flags |= 0x4000
		}
	}
	return flags
}

func makePattern(pattern string, size int) []byte {
	buf := make([]byte, size)
	switch {
	case pattern == "" || pattern == "zero":
		// already zeroed
	case pattern == "random":
		rand.Read(buf)
	case strings.HasPrefix(pattern, "byte:"):
		valStr := strings.TrimPrefix(pattern, "byte:")
		val, err := strconv.ParseUint(strings.TrimPrefix(valStr, "0x"), 16, 8)
		if err != nil {
			val, _ = strconv.ParseUint(valStr, 10, 8)
		}
		for i := range buf {
			buf[i] = byte(val)
		}
	}
	return buf
}

func writeSysfs(path, value string) OpResult {
	err := os.WriteFile(path, []byte(value+"\n"), 0644)
	if err != nil {
		return OpResult{Op: "sysfs_write", Status: "error", Error: err.Error(), Path: path, Value: value}
	}
	return OpResult{Op: "sysfs_write", Status: "ok", Path: path, Value: value}
}

func readSysfs(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readSysfsOp(path string) OpResult {
	data, err := readSysfs(path)
	if err != nil {
		return OpResult{Op: "sysfs_read", Status: "error", Error: err.Error(), Path: path}
	}
	return OpResult{Op: "sysfs_read", Status: "ok", Path: path, Value: data}
}
