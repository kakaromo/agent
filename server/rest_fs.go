package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// registerFSRoutes — 로컬 파일 탐색기로 폴더 열기. standalone 전용.
//
//   POST /api/agent/fs/open  body: {target: "archive"} | {target: "trace", jobId: "..."} | {target: "archive-job", jobId: "..."}
//
// 보안 정책: 임의 path 가 아닌 미리 정의된 target enum 만 허용. path traversal 방어.
//
// OS 별 명령:
//   macOS:   open <path>
//   Linux:   xdg-open <path>
//   Windows: explorer <path>
func registerFSRoutes(mux *http.ServeMux, archiveBase, traceBase string) {
	mux.HandleFunc("/api/agent/fs/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		target, _ := body["target"].(string)
		jobID, _ := body["jobId"].(string)

		path, err := resolveFSTarget(target, jobID, archiveBase, traceBase)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := os.Stat(path); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"path":    path,
				"message": fmt.Sprintf("folder not found: %s", path),
			})
			return
		}
		if err := openInFileExplorer(path); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"path":    path,
				"message": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"path":    path,
			"message": "opened",
		})
	})
}

// resolveFSTarget — target 별로 안전한 절대 경로를 만들어 반환.
// 임의 사용자 입력을 path 로 받지 않고, 미리 정의된 base 디렉토리 + jobId 검증.
func resolveFSTarget(target, jobID, archiveBase, traceBase string) (string, error) {
	switch target {
	case "archive":
		if archiveBase == "" {
			return "", errors.New("archive base not configured")
		}
		return archiveBase, nil

	case "trace":
		// $HOME/agent_trace/{jobId}/
		if jobID == "" {
			return "", errors.New("jobId required for target=trace")
		}
		if !isSafeJobID(jobID) {
			return "", errors.New("invalid jobId")
		}
		if traceBase == "" {
			return "", errors.New("trace base not configured")
		}
		return filepath.Join(traceBase, jobID), nil

	case "archive-job":
		// archive/{remotePath}/{jobId}/ — remotePath 를 사용자가 자유 입력할 수 있어서
		// 정확한 경로를 모름. archiveBase 안에서 jobId 디렉토리를 walk 검색.
		if archiveBase == "" {
			return "", errors.New("archive base not configured")
		}
		if jobID == "" || !isSafeJobID(jobID) {
			return "", errors.New("invalid jobId")
		}
		if found := findJobDirUnder(archiveBase, jobID, 3); found != "" {
			return found, nil
		}
		// 못 찾으면 archive base 만이라도 열기
		return archiveBase, nil

	default:
		return "", fmt.Errorf("unknown target: %s (use 'archive' | 'trace' | 'archive-job')", target)
	}
}

// isSafeJobID — UUID 또는 hex 비슷한 형태만 허용. path traversal 위험 차단.
// agent 의 잡 ID 는 uuid (e.g. "33e9833f-a798-4f25-a039-7cad4745fc47") 형식이라
// 영숫자 + 하이픈만 허용.
func isSafeJobID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// findJobDirUnder — base 디렉토리 아래 maxDepth 까지 walk 하며 이름이 jobID 와 같은 디렉토리를 찾는다.
// 처음 만나는 후보만 반환 (archive 의 remotePath 가 한 단계 또는 여러 단계 nested 일 수 있어 walk).
func findJobDirUnder(base, jobID string, maxDepth int) string {
	var found string
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 권한 등 무시
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		depth := 0
		if rel != "." {
			depth = 1 + strings.Count(rel, string(filepath.Separator))
		}
		if depth > maxDepth {
			return filepath.SkipDir
		}
		if d.Name() == jobID {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// openInFileExplorer — OS 별 file explorer 실행. 결과를 기다리지 않음 (Start, not Run).
func openInFileExplorer(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		// linux / freebsd 등
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
