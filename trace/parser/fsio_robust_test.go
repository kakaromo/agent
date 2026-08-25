package parser

import "testing"

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
