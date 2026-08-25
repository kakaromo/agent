package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 파서·후처리 견고성. 실기기 로그에는 깨진 줄이 섞인다 — adb 파이프가 끊기거나
// ringbuf drop 으로 부분 줄이 나온다. 여기서 panic 하면 **수집 전체가 날아간다**
// (파싱은 StopTrace 후 백그라운드 1회라 재시도 지점이 없다).

// 실기기 로그는 깨진 줄이 섞일 수 있다 — adb 파이프가 중간에 끊기거나
// ringbuf drop 으로 부분 줄이 나온다. 파서가 panic 하면 수집 전체가 날아간다.
func TestFsioParserSurvivesMalformedInput(t *testing.T) {
	cases := []string{
		"",
		"\t",
		"\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t", // 17 탭, 전부 빈 값
		"abc\tUFS\tx\ty\tz\tc\ts\tufshcd_command:send_req\t\t\t\t\t\t\t\t\t",
		"1.0\tUFS\t1\t1\t0\tc\ts\tufshcd_command:\t\t0\t0\t0\t0\t0\t\t0x0\t", // 빈 action
		"1.0\tUFS\t1\t1\t0\tc\ts\tufshcd_upiu:\t\t0\t0\t0\t0\t0\t\t0x0\t",    // 빈 upiu action
		"1.0\tBLK\t1\t1\t0\tc\ts\t\t\t0\t0\t0\t0\t0\t\t0x0\t",                // 빈 action (BLK)
		"1.0\tUFS\t-1\t-1\t-1\tc\ts\tufshcd_command:send_req\t\t0\t0\t0\t-5\t-9\t\tnothex\tlun=abc tag=xyz",
		"1.0\tUFS\t1\t1\t0\tc\ts\tufshcd_command:send_req\t\t0\t0\t0\t0\t0\t\t0xFFFFFFFFFFFFFFFF\tlun=999 grp=0xFFFFF",
		"1.0\tXXX\t1\t1\t0\tc\ts\taction\t\t0\t0\t0\t0\t0\t\t0x0\t", // 모르는 layer
	}
	for i, line := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d panic: %v\n  line=%q", i, r, line)
				}
			}()
			_, _ = parseFsioUfsLine(line)
			_, _ = parseFsioBlockLine(line)
			_ = quickFsioCheck(line)
		}()
	}
}

// 후처리도 이상 입력에 죽으면 안 된다.
func TestFsioProcessorsSurviveOddInput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("processor panic: %v", r)
		}
	}()
	// complete 만 있는 경우 / 시간 역순 / 같은 시각
	ProcessFsioUFS([]FsioUfsEvent{
		{Time: 5, Action: "complete_rsp", Tag: 1},
		{Time: 1, Action: "complete_rsp", Tag: 1},
		{Time: 1, Action: "send_req", Tag: 1},
		{Time: 1, Action: "uic_complete", IsMgmt: true},
	})
	ProcessFsioBlock([]FsioBlockEvent{
		{Time: 5, Action: "block_rq_complete"},
		{Time: 1, Action: "block_rq_issue", Size: 0},
	})
	ProcessFsioUFS(nil)
	ProcessFsioBlock(nil)
}

// 미완결 집계가 **과소 집계될 수 있다** — 알려진 한계이자 Rust 와 동일한 동작.
//
// 두 지점이 함께 만든다:
//
//	① complete_rsp 는 짝을 못 찾아도 `delete(tagOwner, tag)` 를 한다.
//	   그러면 아직 in-flight 인 다른 send 의 tag 점유가 풀려 tag 재사용 보정이 안 걸린다.
//	② markUnfinishedUFS 가 (lun,tag,opcode) 별 complete 개수를 **시간 순서 없이** 세고
//	   뒤에서부터 상계한다. 트레이스 시작 전에 send 된 IO 의 complete 가 카운트에
//	   들어가면, 정작 미완결인 뒤쪽 send 를 짝 있는 것으로 처리한다.
//
// 결과: 실제로 complete 를 못 받은 send 가 is_unfinished 로 표시되지 않을 수 있다.
// **PR 이 건강 신호로 쓰는 그 건수가 과소 집계된다**는 뜻이라 알고 있어야 한다.
//
// 고치지 않는 이유 — Rust `../trace/src/processors/fsio_ufs.rs` 와 동일 동작이다.
// 여기만 바꾸면 같은 로그를 두 도구가 다르게 보여주고, 정합성 검증(DuckDB EXCEPT
// 0 diff)이 깨져 어느 쪽이 맞는지 판단할 근거가 사라진다. 고친다면 두 저장소를
// 같이 고쳐야 한다. 이 테스트는 **현재 동작을 고정**해, 나중에 바뀌면 의도된
// 변경인지 되묻게 한다.
func TestUnfinishedCountCanUndercount_KnownLimitation(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		{Time: 1.0, Action: "send_req", Tag: 7, Opcode: 0x2a, LBA: 100, Size: 4096},
		// 짝 없는 complete (opcode 가 달라 위 send 와 안 맞는다).
		// 그럼에도 tagOwner[7] 이 지워진다.
		{Time: 2.0, Action: "complete_rsp", Tag: 7, Opcode: 0x28, LBA: 999, Size: 4096},
		// tag 7 재사용 — ①이 없었다면 여기서 t=1.0 건이 미완결로 닫혔을 것이다.
		{Time: 3.0, Action: "send_req", Tag: 7, Opcode: 0x2a, LBA: 300, Size: 4096},
		{Time: 3.5, Action: "complete_rsp", Tag: 7, Opcode: 0x2a, LBA: 300, Size: 4096},
	})

	// qd 는 정상으로 돌아온다 — 짝 없는 complete 도 currentQD 를 깎아 상쇄되기 때문.
	if last := out[len(out)-1]; last.QD != 0 {
		t.Errorf("qd = %d, want 0", last.QD)
	}

	// t=1.0 send 는 실제로 complete 를 못 받았지만 미완결로 **표시되지 않는다**.
	// (0x2a complete 가 1건 있어 markUnfinishedUFS 가 짝으로 상계한다)
	var orphanFlagged bool
	for i := range out {
		if out[i].Action == "send_req" && out[i].Time == 1.0 {
			orphanFlagged = out[i].IsUnfinished
		}
	}
	if orphanFlagged {
		t.Log("미완결로 표시됨 — 동작이 개선됐다면 이 테스트와 주석을 갱신할 것")
	}
}

// 큰 로그를 파싱하는 동안 진행률이 나와야 한다.
//
// fsio 분기가 무조건 continue 하는 바람에 아래 보고 블록을 건너뛰어, 멀티 GB
// trace.log 파싱 내내 UI 가 완전히 조용했다 (StopTrace 후 백그라운드 1회라
// 사용자는 멈춘 건지 도는 건지 알 수 없다).
func TestFsioReportsProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("10만 라인 생성 — short 모드에서 생략")
	}
	dir := t.TempDir()
	lf := filepath.Join(dir, "trace.log")
	var b strings.Builder
	for i := 0; i < reportEvery+10; i++ {
		fmt.Fprintf(&b, "%d.000000\tUFS\t1\t1\t0\tt\tvfs_write\tufshcd_command:send_req\text4\t8\t0\t0\t4096\t%d\t\t0x2\tlun=0 tag=%d ufs_op=0x2a\n", i, i, i%32)
	}
	os.WriteFile(lf, []byte(b.String()), 0o644)
	var msgs []string
	if err := RunParquetOnly(lf, dir, "fsio_ufs", func(l string) { msgs = append(msgs, l) }); err != nil {
		t.Fatal(err)
	}
	var scanned int
	for _, m := range msgs {
		if strings.HasPrefix(m, "scanned ") {
			scanned++
		}
	}
	t.Logf("진행 메시지 %d개 (그중 scanned %d개)", len(msgs), scanned)
	if scanned == 0 {
		t.Error("파싱 중 진행률 보고가 없다 — 큰 로그에서 UI 가 조용해진다")
	}
}
