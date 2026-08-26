package trace

import (
	"strings"
	"testing"

	pb "agent/pb"
)

// TestBuildFilterWhereTimeColumn — 시간 조건이 **감지된 컬럼명**을 쓰는지.
//
// 예전엔 `time` 을 리터럴로 박아서, `start_time` 만 있는 스키마(UFSCUSTOM 등)에
// 시간 필터를 걸면 DuckDB Binder Error 로 조회가 통째로 깨졌다. 스텝 구간 분할은
// 이 경로를 상시 사용하므로 회귀하면 바로 드러난다.
func TestBuildFilterWhereTimeColumn(t *testing.T) {
	f := &pb.TraceFilter{StartTime: 100.5, EndTime: 200.5}

	tests := []struct {
		name    string
		timeCol string
		want    string // 이 문자열이 들어가야 한다
		absent  string // 이 문자열은 없어야 한다 ("" 면 검사 안 함)
	}{
		{"time 스키마", "time", "time >= 100.500000", ""},
		// absent 는 "WHERE time >= " 로 본다 — 그냥 "time >= " 는 "start_time >= " 의
		// 부분 문자열이라 정상 출력에도 걸린다.
		{"start_time 스키마", "start_time", "start_time >= 100.500000", "WHERE time >= "},
		{"둘 다 있는 스키마", "COALESCE(time, start_time)", "COALESCE(time, start_time) >= 100.500000", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFilterWhereCols(f, "lba", "opcode", tt.timeCol, nil)
			if !strings.Contains(got, tt.want) {
				t.Errorf("WHERE 에 %q 가 없다:\n%s", tt.want, got)
			}
			if tt.absent != "" && strings.Contains(got, tt.absent) {
				t.Errorf("WHERE 에 %q 가 남아 있다 (없는 컬럼 참조):\n%s", tt.absent, got)
			}
		})
	}

	t.Run("시간 컬럼이 없으면 조건을 아예 안 건다", func(t *testing.T) {
		// 넣으면 쿼리가 깨진다 — skip 이 조용한 오답보다 낫다는 기존 계약과 같다.
		got := buildFilterWhereCols(f, "lba", "opcode", "", nil)
		if strings.Contains(got, "100.5") || strings.Contains(got, "200.5") {
			t.Errorf("시간 컬럼이 없는데 조건이 들어갔다:\n%s", got)
		}
	})

	t.Run("다른 조건은 시간 컬럼 유무와 무관하게 유지", func(t *testing.T) {
		f2 := &pb.TraceFilter{StartTime: 10, MinQd: 4}
		got := buildFilterWhereCols(f2, "lba", "opcode", "", nil)
		if !strings.Contains(got, "qd >= 4") {
			t.Errorf("qd 조건이 사라졌다:\n%s", got)
		}
	})
}
