package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 배포되는 모든 사례가 실제 실행 계약을 따르는지 검사한다.
// trace를 반복에 포함하거나 알 수 없는 action을 저장하는 회귀를 막는다.
func TestYoutubeSuiteContracts(t *testing.T) {
	paths, err := filepath.Glob("../docs/examples/youtube-suite/*.scenario.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("시나리오 없음: %v", err)
	}
	names := map[string]bool{}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var s struct {
				Name  string `json:"name"`
				Steps []struct {
					Type   string            `json:"type"`
					Tool   string            `json:"tool"`
					Params map[string]string `json:"params"`
				} `json:"steps"`
				Loops []struct {
					Start int `json:"startStep"`
					End   int `json:"endStep"`
					Count int `json:"count"`
				} `json:"loops"`
			}
			if err = json.Unmarshal(data, &s); err != nil {
				t.Fatal(err)
			}
			if names[s.Name] {
				t.Fatal("중복 이름", s.Name)
			}
			names[s.Name] = true
			if len(s.Steps) < 3 || s.Steps[0].Type != "trace_start" || s.Steps[len(s.Steps)-1].Type != "trace_stop" {
				t.Fatal("trace 쌍 없음")
			}
			if s.Steps[0].Params["include_dram"] != "true" {
				t.Fatal("DRAM 누락")
			}
			for i, step := range s.Steps {
				if message := ValidateParams(step.Type, step.Tool, step.Params); message != "" {
					t.Errorf("step %d: %s", i, message)
				}
				if step.Params["label"] == "" {
					t.Errorf("step %d 구간 이름 없음", i)
				}
				if step.Type == "youtube" && step.Params["action"] == "watch" && step.Params["ad_timeout"] == "" {
					t.Errorf("step %d 광고 한도 없음", i)
				}
				if step.Type == "uninstall_apk" || step.Type == "cleanup" || step.Params["clear_mode"] == "clear" {
					t.Errorf("step %d 데이터 삭제 동작", i)
				}
			}
			for _, loop := range s.Loops {
				if loop.Start < 1 || loop.End >= len(s.Steps)-1 || loop.Start > loop.End || loop.Count < 1 {
					t.Fatalf("잘못된 반복: %+v", loop)
				}
				for _, step := range s.Steps[loop.Start : loop.End+1] {
					if step.Type == "trace_start" || step.Type == "trace_stop" {
						t.Fatal("반복에 trace 포함")
					}
				}
			}
		})
	}
}
