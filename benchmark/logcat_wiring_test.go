package benchmark

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeLogcatCtl — 호출을 기록하는 최소 구현.
type fakeLogcatCtl struct {
	mu       sync.Mutex
	started  []string // deviceID
	tags     [][]string
	outDirs  []string
	stopped  []string
	startErr error
}

func (f *fakeLogcatCtl) StartLogcatForJob(_ context.Context, deviceID string,
	tags []string, outputDir string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return "", f.startErr
	}
	f.started = append(f.started, deviceID)
	f.tags = append(f.tags, tags)
	f.outDirs = append(f.outDirs, outputDir)
	return "logcat-" + deviceID, nil
}

func (f *fakeLogcatCtl) StopLogcat(jobID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, jobID)
	return nil
}

func TestLogcatOptionsFromParams(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]string
		enabled bool
		tags    []string
	}{
		{"미설정", nil, false, nil},
		{"빈 map", map[string]string{}, false, nil},
		{"off", map[string]string{"logcat": "off"}, false, nil},
		{"on — 태그 없음(explore)", map[string]string{"logcat": "on"}, true, nil},
		{"true 도 허용", map[string]string{"logcat": "true"}, true, nil},
		{"대문자 ON", map[string]string{"logcat": "ON"}, true, nil},
		{"태그 지정", map[string]string{"logcat": "on", "logcat_tags": "Genie,QnnHtp"},
			true, []string{"Genie", "QnnHtp"}},
		{"태그 공백 정리", map[string]string{"logcat": "on", "logcat_tags": " A , ,B "},
			true, []string{"A", "B"}},
		// ⚠ logcat=on 없이 태그만 있으면 켜지지 않아야 한다.
		{"태그만 있고 on 없음", map[string]string{"logcat_tags": "Genie"}, false, nil},
	}
	for _, tc := range cases {
		tags, enabled := logcatOptionsFromParams(tc.params)
		if enabled != tc.enabled {
			t.Errorf("%s: enabled = %v, 기대 %v", tc.name, enabled, tc.enabled)
		}
		if strings.Join(tags, ",") != strings.Join(tc.tags, ",") {
			t.Errorf("%s: tags = %v, 기대 %v", tc.name, tags, tc.tags)
		}
	}
}

func TestStartJobLogcat(t *testing.T) {
	newJob := func(params map[string]string) *Job {
		return &Job{ID: "job1", Params: params}
	}

	t.Run("컨트롤러가 없으면 아무 일도 없다", func(t *testing.T) {
		o := &Orchestrator{}
		if stop := o.startJobLogcat(context.Background(),
			newJob(map[string]string{"logcat": "on"}), "dev1"); stop != nil {
			t.Error("컨트롤러가 nil 인데 stop 함수가 반환됐다")
		}
	})

	t.Run("옵션이 꺼져 있으면 시작하지 않는다", func(t *testing.T) {
		f := &fakeLogcatCtl{}
		o := &Orchestrator{logcatMgr: f}
		if stop := o.startJobLogcat(context.Background(), newJob(nil), "dev1"); stop != nil {
			t.Error("옵션이 없는데 시작됐다")
		}
		if len(f.started) != 0 {
			t.Errorf("시작 호출 = %v", f.started)
		}
	})

	t.Run("옵션이 켜지면 시작하고 job 에 등록한다", func(t *testing.T) {
		f := &fakeLogcatCtl{}
		o := &Orchestrator{logcatMgr: f}
		job := newJob(map[string]string{"logcat": "on", "logcat_tags": "Genie"})
		stop := o.startJobLogcat(context.Background(), job, "dev1")
		if stop == nil {
			t.Fatal("stop 함수가 없다")
		}
		if len(f.started) != 1 || f.started[0] != "dev1" {
			t.Errorf("started = %v", f.started)
		}
		if strings.Join(f.tags[0], ",") != "Genie" {
			t.Errorf("tags = %v", f.tags[0])
		}
		// 취소 경로가 찾을 수 있게 job 에 등록돼야 한다.
		if got := job.getActiveLogcatIDs()["dev1"]; got != "logcat-dev1" {
			t.Errorf("job 에 등록되지 않았다: %q", got)
		}
		stop()
		if len(f.stopped) != 1 {
			t.Errorf("stop 이 호출되지 않았다: %v", f.stopped)
		}
		// 정리 후에는 등록이 지워져야 한다 — 남으면 CancelJob 이 이미 끝난 것을
		// 다시 멈추려 한다.
		if _, ok := job.getActiveLogcatIDs()["dev1"]; ok {
			t.Error("stop 후에도 job 에 등록이 남아 있다")
		}
	})

	t.Run("시작 실패는 시나리오를 막지 않는다", func(t *testing.T) {
		f := &fakeLogcatCtl{startErr: context.DeadlineExceeded}
		o := &Orchestrator{logcatMgr: f}
		job := newJob(map[string]string{"logcat": "on"})
		if stop := o.startJobLogcat(context.Background(), job, "dev1"); stop != nil {
			t.Error("실패했는데 stop 함수가 반환됐다")
		}
		if len(job.getActiveLogcatIDs()) != 0 {
			t.Error("실패했는데 job 에 등록됐다")
		}
	})
}

// ⚠ 이 테스트가 이 파일의 핵심이다.
// 실행 루프가 선형/DAG 두 벌인데 한쪽만 배선하면 캔버스 시나리오에서 조용히
// logcat 이 안 켜진다 — 화면상으론 잡이 정상이라 안 걸린다.
// 두 루프가 같은 헬퍼(startJobLogcat)를 부르는지 소스로 확인한다.
func TestBothScenarioLoopsStartLogcat(t *testing.T) {
	src := readScenarioSource(t)
	// `o.startJobLogcat(` 는 **호출**만 잡는다 (정의는 `func (o *Orchestrator) startJobLogcat(`).
	n := strings.Count(src, "o.startJobLogcat(")
	if n < 2 {
		t.Errorf("startJobLogcat 호출이 %d곳 — 선형/DAG 두 루프 모두에 있어야 한다 "+
			"(한쪽만 넣으면 캔버스 시나리오에서 조용히 안 켜진다)", n)
	}
	// DAG 함수 본문 안에 호출이 있는지 확인
	i := strings.Index(src, "func (o *Orchestrator) runScenarioOnDeviceDAG(")
	if i < 0 {
		t.Fatal("DAG 함수를 찾지 못했다 — 테스트가 낡았다")
	}
	if !strings.Contains(src[i:], "o.startJobLogcat(") {
		t.Error("DAG 루프에 startJobLogcat 호출이 없다")
	}
	j := strings.Index(src, "func (o *Orchestrator) runScenarioOnDevice(")
	if j < 0 {
		t.Fatal("선형 함수를 찾지 못했다 — 테스트가 낡았다")
	}
	// 선형 함수는 DAG 보다 앞에 있으므로 그 사이 구간만 본다.
	if !strings.Contains(src[j:i], "o.startJobLogcat(") {
		t.Error("선형 루프에 startJobLogcat 호출이 없다")
	}
}

// readScenarioSource — scenario.go 원문을 읽는다.
// 배선 누락은 런타임 테스트로 잡기 어려워(디바이스가 필요하다) 소스 수준에서 본다.
func readScenarioSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("scenario.go")
	if err != nil {
		t.Fatalf("scenario.go 읽기 실패: %v", err)
	}
	return string(b)
}
