package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent/storage/sqlitedb"
)

// registerServerRoutes — portal AgentController 의 /servers, /servers/{id}/* 엔드포인트.
//
//	GET    /api/agent/servers
//	POST   /api/agent/servers
//	PUT    /api/agent/servers/{id}
//	DELETE /api/agent/servers/{id}
//	POST   /api/agent/servers/{id}/test     (자기 자신 외 host 면 TCP dial 시도)
//	POST   /api/agent/servers/test           (host:port body 로 dial 시도)
//	GET    /api/agent/servers/{id}/status
//	POST   /api/agent/servers/{id}/reconnect
func registerServerRoutes(mux *http.ServeMux, db *sqlitedb.DB) {
	mux.HandleFunc("/api/agent/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			servers, err := db.ListServers(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]map[string]any, 0, len(servers))
			for _, s := range servers {
				out = append(out, serverToMap(s))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			s := buildServerFromBody(body)
			saved, err := db.CreateServer(r.Context(), s)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, serverToMap(saved))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/agent/servers/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/agent/servers/")
		parts := strings.Split(rest, "/")

		// POST /api/agent/servers/test (body 기반 host/port 테스트)
		if len(parts) == 1 && parts[0] == "test" && r.Method == http.MethodPost {
			handleConnectionTestByBody(w, r)
			return
		}

		// /api/agent/servers/{id}/...
		idStr := parts[0]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id: "+idStr)
			return
		}

		// /api/agent/servers/{id}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodPut:
				body, err := readJSONBody(r)
				if err != nil {
					writeError(w, http.StatusBadRequest, "decode: "+err.Error())
					return
				}
				s := buildServerFromBody(body)
				updated, err := db.UpdateServer(r.Context(), id, s)
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "server not found")
					return
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, serverToMap(updated))
			case http.MethodDelete:
				if err := db.DeleteServer(r.Context(), id); err != nil {
					if errors.Is(err, sqlitedb.ErrNotFound) {
						writeError(w, http.StatusNotFound, "server not found")
						return
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"success": true})
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		// /api/agent/servers/{id}/{action}
		if len(parts) == 2 {
			s, err := db.FindServer(r.Context(), id)
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "server not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			switch parts[1] {
			case "test":
				if r.Method != http.MethodPost {
					writeError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				success := tcpReachable(s.Host, s.Port)
				writeJSON(w, http.StatusOK, map[string]any{
					"success": success,
					"host":    s.Host,
					"port":    s.Port,
					"message": connMessage(success),
				})
			case "status":
				if r.Method != http.MethodGet {
					writeError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				connected := tcpReachable(s.Host, s.Port)
				state := "IDLE"
				if connected {
					state = "READY"
				} else {
					state = "TRANSIENT_FAILURE"
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"serverId":  id,
					"state":     state,
					"connected": connected,
					"host":      s.Host,
					"port":      s.Port,
				})
			case "reconnect":
				if r.Method != http.MethodPost {
					writeError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				// standalone 에서는 별도 connection pool 이 없다 — 단순 TCP test.
				success := tcpReachable(s.Host, s.Port)
				state := "TRANSIENT_FAILURE"
				if success {
					state = "READY"
				}
				message := "재연결 실패"
				if success {
					message = "재연결 성공"
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"success": success,
					"state":   state,
					"message": message,
				})
			default:
				writeError(w, http.StatusNotFound, "unknown action: "+parts[1])
			}
			return
		}

		writeError(w, http.StatusNotFound, "not found")
	})
}

// handleConnectionTestByBody — POST /api/agent/servers/test  {host, port}
func handleConnectionTestByBody(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	host, _ := body["host"].(string)
	port := 50051
	if v, ok := numberOf(body["port"]); ok {
		port = int(v)
	}
	if host == "" {
		writeError(w, http.StatusBadRequest, "host required")
		return
	}
	success := tcpReachable(host, port)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": success,
		"host":    host,
		"port":    port,
		"message": connMessage(success),
	})
}

func buildServerFromBody(body map[string]any) *sqlitedb.AgentServer {
	s := &sqlitedb.AgentServer{
		Enabled: true,
	}
	if v, ok := body["name"].(string); ok {
		s.Name = v
	}
	if v, ok := body["host"].(string); ok {
		s.Host = v
	}
	if v, ok := numberOf(body["port"]); ok {
		s.Port = int(v)
	}
	if s.Port == 0 {
		s.Port = 50051
	}
	if v, ok := body["enabled"].(bool); ok {
		s.Enabled = v
	}
	if v, ok := body["description"].(string); ok {
		s.Description = v
	}
	return s
}

func serverToMap(s *sqlitedb.AgentServer) map[string]any {
	return map[string]any{
		"id":          s.ID,
		"name":        s.Name,
		"host":        s.Host,
		"port":        s.Port,
		"enabled":     s.Enabled,
		"description": s.Description,
		"createdAt":   s.CreatedAt.Format(time.RFC3339Nano),
		"updatedAt":   s.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func connMessage(success bool) string {
	if success {
		return "연결 성공"
	}
	return "연결 실패"
}

// tcpReachable — 짧은 timeout TCP dial.
func tcpReachable(host string, port int) bool {
	dialer := tcpDialer()
	conn, err := dialer.Dial("tcp", host+":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
