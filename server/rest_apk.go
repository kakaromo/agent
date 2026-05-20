// rest_apk.go — APK 관리 REST endpoints (gRPC ListBundledApks/InstallApk/UninstallApk 위임).
//
//	GET    /api/agent/apks                          — tools/apks 폴더의 .apk 목록
//	POST   /api/agent/apks/install                  — body: {deviceId, apkFilename, grantPermissions?}
//	POST   /api/agent/apks/uninstall                — body: {deviceId, packageName, keepData?}
//
// DB 의존성 없이 동작 (사무실/standalone 동일).
package server

import (
	"net/http"

	pb "agent/pb"
)

func registerApkRoutes(mux *http.ServeMux, agent *DeviceAgentServer) {
	mux.HandleFunc("/api/agent/apks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		resp, err := agent.ListBundledApks(r.Context(), &pb.ListBundledApksRequest{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]map[string]any, 0, len(resp.Apks))
		for _, a := range resp.Apks {
			out = append(out, map[string]any{
				"filename":   a.Filename,
				"sizeBytes":  a.SizeBytes,
				"modifiedAt": a.ModifiedAt,
			})
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("/api/agent/apks/install", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		req := &pb.InstallApkRequest{
			DeviceId:                stringField(body, "deviceId"),
			ApkFilename:             stringField(body, "apkFilename"),
			Reinstall:               true,
			GrantRuntimePermissions: boolField(body, "grantPermissions"),
		}
		resp, err := agent.InstallApk(r.Context(), req)
		if err != nil {
			// resp 도 함께 직렬화해 호출자가 stdout/stderr 확인 가능하도록.
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"success":     false,
				"message":     errMessage(err, resp),
				"packageName": "",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":     resp.Success,
			"message":     resp.Message,
			"packageName": resp.PackageName,
		})
	})

	mux.HandleFunc("/api/agent/apks/uninstall", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		req := &pb.UninstallApkRequest{
			DeviceId:    stringField(body, "deviceId"),
			PackageName: stringField(body, "packageName"),
			KeepData:    boolField(body, "keepData"),
		}
		resp, err := agent.UninstallApk(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": errMessage(err, resp),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": resp.Success,
			"message": resp.Message,
		})
	})
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolField(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// errMessage prefers the gRPC resp.Message (which contains adb stderr) when available,
// falling back to the raw error. protobuf-generated GetMessage 는 nil receiver-safe.
func errMessage(err error, resp interface{ GetMessage() string }) string {
	if resp != nil {
		if m := resp.GetMessage(); m != "" {
			return m
		}
	}
	return err.Error()
}
