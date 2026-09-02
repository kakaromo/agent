package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	pb "agent/pb"
	"agent/trace"
)

// handleTraceRawCSV: POST /api/agent/trace/raw.csv  body { jobIds, filter? }
//
// Raw 이벤트를 **전체** CSV 로 스트리밍한다.
//
// ⚠ `/trace/raw`(JSON) 와 **데이터 범위가 다르다.** 저건 50만 건이 넘으면 샘플링하고,
// 그 샘플은 시간 버킷별 최소·최대 행을 일부러 끼워 넣어 **극단값 쪽으로 치우쳐 있다**
// (차트 외곽선 보존이 목적). 내보내기는 받는 쪽이 다시 집계하는 용도라 그걸 타면
// 평균·합계가 그럴듯하게 틀린다. 그래서 CSV 는 sampler 를 우회해 parquet 을 직접 읽는다.
//
// 응답을 메모리에 모으지 않고 흘려보낸다 — 35만 행이 JSON 으로 55MB 였다.
func handleTraceRawCSV(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer) {
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
		writeError(w, http.StatusBadRequest, "jobIds required")
		return
	}
	var filter *pb.TraceFilter
	if fmap, ok := body["filter"].(map[string]any); ok {
		filter = buildTraceFilter(fmap)
	}

	infos, err := agent.collectTraceJobInfos(jobIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// ⚠ 헤더는 **첫 바이트를 쓰기 전에** 정한다. 스트리밍을 시작한 뒤에는 상태 코드를
	// 바꿀 수 없어서, 중간에 에러가 나면 잘린 CSV 가 200 으로 나간다. 그래서 쿼리
	// 준비까지는 위에서 끝내고, 여기부터는 실패해도 로그로만 남긴다.
	filename := rawCSVFilename(jobIDs)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	// 브라우저가 진행률을 못 보여주더라도 스트리밍이 끊기지 않게.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	n, err := trace.ExportRawCSV(w, infos, filter)
	if err != nil {
		// 이미 200 + 일부 바이트를 보낸 뒤일 수 있다. 클라이언트에 에러를 알릴
		// 방법이 없으므로 로그에 남긴다 — 파일이 잘린 채 저장될 수 있다.
		slog.Error("raw CSV export 중단 — 파일이 잘린 채 저장될 수 있다",
			"error", err, "written_rows", n, "job_ids", jobIDs)
		return
	}
	slog.Info("raw CSV export 완료", "rows", n, "job_ids", jobIDs, "filename", filename)
}

// rawCSVFilename — 다운로드 파일명. jobId 가 하나면 그걸 쓰고, 여러 개면 개수를 적는다.
func rawCSVFilename(jobIDs []string) string {
	ts := time.Now().Format("20060102-150405")
	if len(jobIDs) == 1 {
		return fmt.Sprintf("trace-raw_%s_%s.csv", sanitizeFilename(jobIDs[0]), ts)
	}
	return fmt.Sprintf("trace-raw_%djobs_%s.csv", len(jobIDs), ts)
}
