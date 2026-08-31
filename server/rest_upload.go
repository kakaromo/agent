package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agent/trace"
)

// 파일 업로드 → 자동 판별 → 파싱/저장.
//
// **왜 있나.** 기기 없이도 남이 준 로그·과거 로그를 화면에서 볼 수 있어야 한다.
// 파싱과 조회는 원래 파일만 보므로(기기 무관), 잡 레코드만 만들면 기존
// Trace Analysis / Result 화면이 그대로 열린다.
//
// **포맷은 자동 판별한다.** 사용자가 trace_type 을 잘못 고르면 파서가 아무 행도
// 못 만들고 **에러 없이 0건**으로 끝나 원인 추적이 어렵다. 그래서 내용으로 정하고,
// 못 정하면 명확히 실패시킨다 (trace.DetectUpload).

// uploadMaxBytes — 업로드 상한.
//
// trace.log 는 수십 분 수집하면 수 GB 까지 간다. 무제한으로 두면 디스크가 차므로
// 상한을 두되, 실사용 로그가 걸리지 않을 만큼 넉넉히 잡는다.
const uploadMaxBytes = 4 << 30 // 4 GiB

// registerUploadRoutes — POST /api/agent/upload/file
//
// multipart/form-data:
//
//	file : 업로드 파일 (trace 로그 또는 벤치마크 결과 JSON)
//	name : (선택) 표시용 이름. 없으면 파일명.
//
// 응답:
//
//	trace     → {kind:"trace", jobId, traceType, reason}
//	benchmark → {kind:"benchmark", path, deviceId, tool, reason}
func registerUploadRoutes(mux *http.ServeMux, agent *DeviceAgentServer, archiveBase string) {
	mux.HandleFunc("/api/agent/upload/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, uploadMaxBytes)

		// 메모리 상한만 준다 — 넘는 부분은 임시파일로 spool 된다.
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "업로드 파싱 실패: "+err.Error())
			return
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()

		file, hdr, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file 필드가 필요합니다: "+err.Error())
			return
		}
		defer file.Close()

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = hdr.Filename
		}

		// 판별은 앞부분만 읽는다. 그 뒤 되감아 전체를 저장한다.
		det, err := trace.DetectUpload(file, hdr.Filename)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, "파일 되감기 실패: "+err.Error())
			return
		}

		switch det.Kind {
		case trace.UploadKindTrace:
			if agent == nil || agent.traceMgr == nil {
				writeError(w, http.StatusServiceUnavailable, "trace manager 를 쓸 수 없습니다")
				return
			}
			// 먼저 임시 파일로 받는다 — IngestUploadedLog 가 잡 폴더로 옮긴다.
			tmp, err := os.CreateTemp("", "upload-trace-*.log")
			if err != nil {
				writeError(w, http.StatusInternalServerError, "임시 파일 생성 실패: "+err.Error())
				return
			}
			tmpPath := tmp.Name()
			if _, err := io.Copy(tmp, file); err != nil {
				tmp.Close()
				_ = os.Remove(tmpPath)
				writeError(w, http.StatusInternalServerError, "업로드 저장 실패: "+err.Error())
				return
			}
			tmp.Close()

			jobID, err := agent.traceMgr.IngestUploadedLog(tmpPath, det.TraceType, name)
			if err != nil {
				_ = os.Remove(tmpPath)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"kind":      "trace",
				"jobId":     jobID,
				"traceType": det.TraceType,
				"reason":    det.Reason,
				"name":      name,
			})

		case trace.UploadKindBenchmark:
			path, meta, err := saveUploadedBenchmark(archiveBase, hdr.Filename, file)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"kind":     "benchmark",
				"path":     path,
				"deviceId": meta["deviceId"],
				"tool":     meta["tool"],
				"reason":   det.Reason,
				"name":     name,
			})

		default:
			writeError(w, http.StatusBadRequest, "알 수 없는 업로드 종류: "+det.Kind)
		}
	})
}

// saveUploadedBenchmark — 벤치마크 결과 JSON 을 archive 아래 uploads/ 로 저장한다.
//
// 잡으로 등록하지 않는다 — 벤치마크 결과는 이미 완결된 산출물이라 파싱할 것이
// 없고, 화면은 파일 내용을 그대로 보여주면 된다. 경로와 핵심 필드만 돌려준다.
func saveUploadedBenchmark(archiveBase, filename string, r io.Reader) (string, map[string]string, error) {
	if archiveBase == "" {
		return "", nil, fmt.Errorf("archive 경로가 설정되지 않았습니다 (standalone 모드가 아닌 듯)")
	}
	dir := filepath.Join(archiveBase, "uploads")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", nil, fmt.Errorf("uploads 디렉토리 생성 실패: %w", err)
	}

	// 파일명은 그대로 믿지 않는다 — 경로 구분자가 들어오면 상위로 새어나간다.
	base := filepath.Base(filepath.Clean(filename))
	if base == "." || base == string(filepath.Separator) || base == ".." {
		base = "result.json"
	}
	dest := filepath.Join(dir, base)

	body, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("업로드 읽기 실패: %w", err)
	}
	// 판별에서 한 번 봤지만 여기서 전체를 다시 검증한다 — 앞부분만 유효한 파일을 막는다.
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", nil, fmt.Errorf("JSON 파싱 실패: %w", err)
	}
	if err := os.WriteFile(dest, body, 0644); err != nil {
		return "", nil, fmt.Errorf("저장 실패: %w", err)
	}

	meta := map[string]string{}
	if v, ok := doc["deviceId"].(string); ok {
		meta["deviceId"] = v
	}
	if v, ok := doc["tool"].(string); ok {
		meta["tool"] = v
	}
	return dest, meta, nil
}
