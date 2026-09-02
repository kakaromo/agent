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

	// ⚠ 헤더는 **첫 바이트를 쓰기 전에** 정해야 한다. 스트리밍을 시작한 뒤에는 상태
	// 코드도 Content-Type 도 못 바꾼다. 그런데 분할 여부(=ZIP 여부)는 다 써 봐야 아는
	// 값이라, 먼저 행 수를 센다 — count(*) 는 parquet 메타만 읽어 싸다(95만 행 수 ms).
	total, err := trace.CountRawRows(infos, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	base := rawCSVBaseName(jobIDs)
	split := total > trace.ExcelMaxDataRows

	if split {
		// Excel 시트 한계를 넘는다 → _1, _2 … 로 나눠 ZIP 하나로.
		//
		// 파일을 여러 번 다운로드하지 않는 이유 — 브라우저가 **연속 다운로드를
		// 차단**할 수 있고, 그러면 사용자는 2번째부터 안 받아진 걸 모른 채 넘어간다.
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+base+".zip\"")
	} else {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+base+".csv\"")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 분할 정보를 헤더로도 알린다 — 프론트가 "N개 파일" 을 안내할 수 있게.
	w.Header().Set("X-Total-Rows", fmt.Sprint(total))
	w.WriteHeader(http.StatusOK)

	res, err := trace.ExportRawCSV(w, infos, filter, base, split)
	if err != nil {
		// 이미 200 + 일부 바이트를 보낸 뒤일 수 있다. 클라이언트에 에러를 알릴
		// 방법이 없으므로 로그에 남긴다 — 파일이 잘린 채 저장될 수 있다.
		slog.Error("raw CSV export 중단 — 파일이 잘린 채 저장될 수 있다",
			"error", err, "written_rows", res.Rows, "job_ids", jobIDs)
		return
	}
	slog.Info("raw CSV export 완료",
		"rows", res.Rows, "files", res.FileCount, "zipped", res.Zipped,
		"job_ids", jobIDs, "base", base)
}

// rawCSVBaseName — 확장자를 뺀 파일 이름. 분할 시 `<base>_1.csv` 로 쓰이고
// ZIP 이름도 `<base>.zip` 이 된다.
func rawCSVBaseName(jobIDs []string) string {
	ts := time.Now().Format("20060102-150405")
	if len(jobIDs) == 1 {
		return fmt.Sprintf("trace-raw_%s_%s", sanitizeFilename(jobIDs[0]), ts)
	}
	return fmt.Sprintf("trace-raw_%djobs_%s", len(jobIDs), ts)
}
