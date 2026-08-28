package benchmark

import (
	"strings"
	"testing"

	pb "agent/pb"
)

// TestTraceStopReportsActualTraceType — trace_stop 의 TRACE_STOP 라인이
// **실제로 돌고 있는 잡의 trace_type** 을 보고하는지.
//
// 예전엔 trace_stop 스텝의 params["trace_type"] 을 읽었다. 그 값은 trace_start 에서
// 고르는 것이라 stop 쪽엔 대개 비어 있고, 그러면 "ufs" 로 폴백했다.
// 프론트는 이 줄로 trace_type 을 정하므로(mappings 가 서버 응답보다 우선)
// fsio_ufs 로 수집한 트레이스가 ufs 로 읽혀 **Attribution 탭·mgmt 차트·fsio
// 컬럼이 통째로 사라졌다.** 통계는 parquet 스키마를 직접 봐서 멀쩡했고, 그래서
// "통계엔 mgmt 가 나오는데 차트엔 없다" 는 헷갈리는 상태가 됐다.
func TestTraceStopReportsActualTraceType(t *testing.T) {
	run := func(t *testing.T, jobType string, stepParams map[string]string, want string) {
		t.Helper()
		o := &Orchestrator{traceMgr: &fakeTraceCtl{traceType: jobType}}
		active := "job-1"
		es := expandedStep{
			step:        &pb.ScenarioStep{Type: "trace_stop", Params: stepParams},
			stepIndex:   2,
			loopIndex:   0,
			repeatIndex: 1,
		}
		out, _, err := o.executeStepInner(nil, nil, nil, es, 0, nil, "dev", &active)
		if err != nil {
			t.Fatalf("trace_stop 실패: %v", err)
		}
		if !strings.Contains(out, "trace_type="+want) {
			t.Errorf("trace_type 이 틀렸다\n  got:  %s\n  want: trace_type=%s", out, want)
		}
	}

	// 핵심 회귀 — stop 스텝엔 trace_type 이 없다(실제 시나리오에서 흔한 모양).
	t.Run("stop 에 값이 없어도 잡의 타입을 쓴다", func(t *testing.T) {
		run(t, "fsio_ufs", map[string]string{}, "fsio_ufs")
	})

	// 잡이 진실 소스 — 스텝에 옛 값이 남아 있어도 잡을 따른다.
	t.Run("스텝 값이 달라도 잡을 따른다", func(t *testing.T) {
		run(t, "fsio_block", map[string]string{"trace_type": "ufs"}, "fsio_block")
	})

	// 잡을 못 찾을 때만 스텝 값으로 폴백.
	t.Run("잡을 모르면 스텝 값", func(t *testing.T) {
		run(t, "", map[string]string{"trace_type": "block"}, "block")
	})
}
