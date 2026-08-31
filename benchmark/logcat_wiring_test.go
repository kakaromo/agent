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

// ⚠⚠ startJobLogcat 은 job.Params 를 읽는데, RunScenario 가 그걸 채우는지는
// 아무도 확인하지 않았다. 기존 테스트는 &Job{Params: ...} 를 손으로 만들어
// 넣으므로 실제 경로의 단절을 못 잡는다 — 소스 수준에서 확인한다.
func TestRunScenarioPopulatesJobParams(t *testing.T) {
	src := readScenarioSource(t)
	i := strings.Index(src, "func (o *Orchestrator) RunScenario(")
	if i < 0 {
		t.Fatal("RunScenario 를 찾지 못했다 — 테스트가 낡았다")
	}
	body := src[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "Params:") && !strings.Contains(body, "job.Params") {
		t.Error("RunScenario 가 job.Params 를 채우지 않는다 — logcat 옵션(logcat=on)이 " +
			"영영 읽히지 않아 시나리오에서 수집이 안 켜진다")
	}
}

// ⚠⚠ marker 폴백도 **선형·DAG 두 루프 모두**에 배선돼야 한다.
// 한쪽만 넣으면 캔버스 시나리오에서 느린 기기의 구간이 조용히 사라진다
// (화면상 잡은 정상이라 안 걸린다). startJobLogcat 과 같은 함정.
func TestBothScenarioLoopsMarkStepBoundary(t *testing.T) {
	src := readScenarioSource(t)
	if n := strings.Count(src, "markStepBegin("); n < 3 {
		// 정의 1 + 호출 2
		t.Errorf("markStepBegin 이 %d곳 — 정의 1 + 선형/DAG 호출 2 여야 한다", n)
	}
	i := strings.Index(src, "func (o *Orchestrator) runScenarioOnDeviceDAG(")
	if i < 0 {
		t.Fatal("DAG 함수를 찾지 못했다 — 테스트가 낡았다")
	}
	if !strings.Contains(src[i:], "markStepBegin(") {
		t.Error("DAG 루프에 markStepBegin 호출이 없다")
	}
	j := strings.Index(src, "func (o *Orchestrator) runScenarioOnDevice(")
	if j < 0 {
		t.Fatal("선형 함수를 찾지 못했다 — 테스트가 낡았다")
	}
	if !strings.Contains(src[j:i], "markStepBegin(") {
		t.Error("선형 루프에 markStepBegin 호출이 없다")
	}
	// 양쪽 다 markEnd 도 불러야 한다 (begin 만 찍으면 반쪽이라 폴백이 안 걸린다).
	if strings.Count(src, ".markEnd(") < 2 {
		t.Error("markEnd 호출이 2곳 미만 — begin 만 찍힌 구간은 폴백에 쓰이지 않는다")
	}
}

// ⚠⚠ marker 쓰기에 **스텝 시점 게이트를 두면 안 된다.**
//
// 한때 `HostToDeviceMonotonic` 의 ok 로 걸렀는데 드리프트 케이스를 정확히 빗나갔다:
// ClockSync.Stop 은 StopTrace 에서야 채워져서 스텝 중에는 Usable() 이 항상 true 다.
// 그래서 marker 를 안 쓰는데, 종료 후 drift 로 usable=false 가 되면 UI 가 offset
// 구간을 거부한다 → **둘 다 없어 Behavior 가 통째로 사라진다.**
//
// 드리프트야말로 폴백이 가장 필요한 경우다(구간이 조용히 밀리는데 그래프는 정상).
func TestMarkStepBeginHasNoStepTimeGate(t *testing.T) {
	src := readScenarioSource(t)
	i := strings.Index(src, "func (o *Orchestrator) markStepBegin(")
	if i < 0 {
		t.Fatal("markStepBegin 을 찾지 못했다 — 테스트가 낡았다")
	}
	body := src[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	// 조기 반환 게이트가 다시 들어오면 드리프트 커버가 사라진다.
	if strings.Contains(body, "HostToDeviceMonotonic(") {
		t.Error("스텝 시점에 offset 가용성으로 게이트하고 있다 — 그 시점엔 drift 를 알 수 " +
			"없어(Stop==nil) 항상 usable 로 나오고, 정작 드리프트 잡에서 marker 가 안 남는다")
	}
}

// ⚠ marker 왕복은 **호스트 시각 창 밖**에 있어야 한다.
// stepStartedAt 을 markStepBegin 앞에서 잡으면 adb 왕복 지연이 구간 양끝에 더해져
// offset 경로로 표시되는 구간이 실제보다 길어진다 — 측정 도구가 자기 측정을 부풀린다.
func TestMarkerWritesOutsideTimingWindow(t *testing.T) {
	src := readScenarioSource(t)
	for _, c := range []struct{ fn, begin, start string }{
		{"func (o *Orchestrator) runScenarioOnDevice(", "mk := o.markStepBegin(", "stepStartedAt := time.Now()"},
		{"func (o *Orchestrator) runScenarioOnDeviceDAG(", "dagMk := o.markStepBegin(", "dagStepStartedAt := time.Now()"},
	} {
		i := strings.Index(src, c.fn)
		if i < 0 {
			t.Fatalf("%s 를 찾지 못했다 — 테스트가 낡았다", c.fn)
		}
		body := src[i:]
		if j := strings.Index(body[1:], "\nfunc (o *Orchestrator)"); j > 0 {
			body = body[:j]
		}
		bi, si := strings.Index(body, c.begin), strings.Index(body, c.start)
		if bi < 0 || si < 0 {
			t.Errorf("%s: marker/시각 호출을 못 찾았다", c.fn)
			continue
		}
		if bi > si {
			t.Errorf("%s: 시각을 marker 쓰기 **전에** 잡는다 — adb 왕복이 구간에 포함돼 "+
				"offset 경로 구간이 실제보다 길어진다", c.fn)
		}
	}
}
