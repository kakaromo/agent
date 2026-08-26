package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	pb "agent/pb"
	"agent/trace"
)

// registerArchiveRoutes — portal /api/agent/upload/* 호환.
//
// standalone 은 MinIO 가 의미 없다 (출장 노트북 단독). 로컬 디스크에 복사:
//
//	~/.agent-standalone/archive/{remotePath}/{jobId}/...
//
// remotePath 가 비어있으면 jobId 만 사용. portal 응답 shape 그대로:
//
//	{success, message, uploadedFiles: [local paths]}
func registerArchiveRoutes(mux *http.ServeMux, agent *DeviceAgentServer, archiveBase string) {
	// POST /api/agent/upload/trace  { jobIds[], remotePath? }
	mux.HandleFunc("/api/agent/upload/trace", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		jobIDs := stringSlice(body["jobIds"])
		if len(jobIDs) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "no jobIds provided",
			})
			return
		}
		remotePath, _ := body["remotePath"].(string)

		var uploaded []string
		for _, jobID := range jobIDs {
			info, err := agent.traceMgr.GetTraceJobInfo(jobID)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": fmt.Sprintf("job %s: %v", jobID, err),
				})
				return
			}
			dst := filepath.Join(archiveBase, remotePath, jobID)
			files, err := copyParquetDir(info.Dir, dst)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": fmt.Sprintf("copy %s: %v", jobID, err),
				})
				return
			}
			uploaded = append(uploaded, files...)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":       true,
			"message":       fmt.Sprintf("archived %d files to %s", len(uploaded), archiveBase),
			"uploadedFiles": uploaded,
		})
	})

	// POST /api/agent/upload/benchmark  { jobId, remotePath? }
	mux.HandleFunc("/api/agent/upload/benchmark", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		jobID, _ := body["jobId"].(string)
		if jobID == "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "jobId required",
			})
			return
		}
		remotePath, _ := body["remotePath"].(string)

		resp, err := agent.GetBenchmarkResult(r.Context(), &pb.GetBenchmarkResultRequest{JobId: jobID})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": err.Error(),
			})
			return
		}

		var uploaded []string
		dstDir := filepath.Join(archiveBase, remotePath, jobID)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "mkdir: " + err.Error(),
			})
			return
		}
		for _, br := range resp.GetResults() {
			// portal 의 minio path 와 동일한 파일명 규칙 ({deviceId}_result.json)
			fileName := fmt.Sprintf("%s_result.json", sanitizeFileSegment(br.GetDeviceId()))
			fullPath := filepath.Join(dstDir, fileName)
			data, err := json.MarshalIndent(benchmarkResultToMap(br), "", "  ")
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "marshal: " + err.Error(),
				})
				return
			}
			if err := os.WriteFile(fullPath, data, 0o644); err != nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "write: " + err.Error(),
				})
				return
			}
			uploaded = append(uploaded, fullPath)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":       true,
			"message":       fmt.Sprintf("archived %d files to %s", len(uploaded), dstDir),
			"uploadedFiles": uploaded,
		})
	})
}

// copyParquetDir — srcDir 의 *.parquet (+ trace.log, clocksync.json) 를 dstDir 로
// 복사하고 결과 파일 경로 반환.
//
// clocksync.json 을 함께 옮기는 이유: 이 파일이 없으면 archive 쪽 parquet 은 스텝
// 구간으로 나눌 수 없다 (호스트 시각 ↔ 기기 monotonic 변환에 필요). parquet 만 옮기면
// **데이터는 갔는데 구간 분할만 조용히 안 되는** 상태가 된다.
func copyParquetDir(srcDir, dstDir string) ([]string, error) {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// parquet + raw trace.log + clocksync.json 만 옮긴다 (다른 부산물은 제외)
		ext := filepath.Ext(name)
		if ext != ".parquet" && name != "trace.log" && name != trace.ClockSyncFileName {
			continue
		}
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)
		if err := copyFile(src, dst); err != nil {
			return out, err
		}
		out = append(out, dst)
	}
	return out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	// flush + atime 일치를 위해 utime 동기화 (단순화: 현재시간)
	now := time.Now()
	_ = os.Chtimes(dst, now, now)
	return nil
}

// sanitizeFileSegment — deviceId 등 ADB 경로/특수문자를 file-safe 변환.
func sanitizeFileSegment(s string) string {
	if s == "" {
		return "unknown"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
