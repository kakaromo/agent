package server

import (
	"io/fs"
	"net/http"
	"strings"

	"agent/adb"
	"agent/schedule"
	"agent/storage/sqlitedb"
)

// HTTPRouter 옵션
type HTTPRouterOptions struct {
	Agent         *DeviceAgentServer
	Manager       *adb.Manager
	ScreenHandler http.Handler
	UIFS          fs.FS
	EnableUI      bool

	// DB — standalone 모드에서만 nil 아님. portal-style CRUD endpoints 활성화 조건.
	DB *sqlitedb.DB
	// ScheduleRunner — DB 있을 때만 main.go 가 기동 후 전달. nil 이면 schedule endpoints 가 503.
	ScheduleRunner *schedule.Runner
	// ArchiveBase — standalone 의 로컬 archive 복사 경로. 빈 문자열이면 archive endpoints 비활성.
	ArchiveBase string
}

// NewHTTPRouter 는 cmux 의 HTTP 분기에 마운트할 단일 핸들러를 만든다.
// /api/*  → REST 어댑터 (server.DeviceAgentServer 메서드 직접 호출)
// /ws/*   → WebSocket (screen, job progress, monitor)
// /health → 헬스체크
// /       → SPA fallback (EnableUI=true 일 때만)
func NewHTTPRouter(opts HTTPRouterOptions) http.Handler {
	mux := http.NewServeMux()

	// /health — 기존 main.go 의 헬스체크 로직을 router 안으로 이전
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"devices": opts.Manager.Count(),
		})
	})

	// /ws/screen/* — 기존 scrcpy 스트리밍 (legacy path)
	mux.Handle("/ws/screen/", opts.ScreenHandler)
	// /api/agent/screen/* — portal frontend 호환 (getScreenWebSocketUrl).
	// screenHandler 가 path 에서 trim 하는 prefix 차이를 흡수하기 위해 StripPrefix 로 /ws/screen/ 모양으로 정규화.
	mux.Handle("/api/agent/screen/", http.StripPrefix("/api/agent/screen", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/ws/screen" + r.URL.Path
		opts.ScreenHandler.ServeHTTP(w, r2)
	})))

	// /ws/* — REST 호환 WebSocket (job progress, monitor)
	registerWSRoutes(mux, opts.Agent)

	// REST 어댑터
	registerRESTRoutes(mux, opts.Agent)

	// SSE 어댑터 — /api/agent/benchmark/progress, /api/agent/monitoring/stream, /api/agent/devices/stream
	registerSSERoutes(mux, opts.Agent)

	// Scenario REST — DB 가 있으면 macroId hydrate 가능 (standalone), 없으면 nil 전달
	registerScenarioRoutes(mux, opts.Agent, opts.DB)

	// DB 기반 portal-style CRUD (server, execution, preset, scenario template, macro, schedule).
	// DB nil(non-standalone) 일 땐 건너뛴다.
	if opts.DB != nil {
		registerServerRoutes(mux, opts.DB)
		registerExecutionRoutes(mux, opts.DB)
		registerMacroRoutes(mux, opts.Agent, opts.DB)
		registerPresetRoutes(mux, opts.DB)
		registerScheduleRoutes(mux, opts.DB, opts.ScheduleRunner)
		installJobExecutionHook(opts.Agent, opts.DB)
	}
	// Archive 업로드 (로컬 디스크 복사). archiveBase 가 비어있으면 등록 안 함.
	if opts.ArchiveBase != "" {
		registerArchiveRoutes(mux, opts.Agent, opts.ArchiveBase)
	}

	// SPA fallback
	if opts.EnableUI && opts.UIFS != nil {
		fileServer := http.FileServer(http.FS(opts.UIFS))
		mux.Handle("/", spaHandler(opts.UIFS, fileServer))
	}

	return mux
}

// spaHandler 는 정적 파일이 있으면 그대로 서빙, 없으면 index.html 로 fallback.
// SvelteKit adapter-static + fallback='index.html' 모드의 SPA 라우팅 대응.
func spaHandler(uiFS fs.FS, fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			fileServer.ServeHTTP(w, r) // root → http.FileServer 가 index.html 자동 서빙
			return
		}
		if _, err := fs.Stat(uiFS, p); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// 정적 파일 없으면 index.html 본문을 그대로 직접 쓴다 (SPA fallback).
		data, ferr := fs.ReadFile(uiFS, "index.html")
		if ferr != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
}
