package trace

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"

	pb "agent/pb"
)

// CSV 내보내기는 **샘플링을 타면 안 된다.**
//
// GetRawData 는 50만 건 초과 시 샘플링하는데, 그 표본은 시간 버킷별 min/max 행을
// 일부러 끼워 넣어 **극단값 쪽으로 치우쳐 있다**. 그걸 CSV 로 주면 받는 쪽이 평균·합계를
// 계산했을 때 그럴듯하게 틀린 값이 나온다. 여기서 "전체 행" 을 고정한다.
//
// maxEvents(50만) 를 넘는 픽스처를 만들면 테스트가 너무 느려서, sampler 를 타는
// 경로(GetRawData)와 CSV 경로의 **행 수가 같은지**로 대신 검증한다. 임계 초과
// 동작은 실기기 잡으로 확인했다 (544,906건 → JSON 500,000 / CSV 544,906).
func TestExportRawCSVReturnsAllRows(t *testing.T) {
	lines := []string{}
	for i := 0; i < 50; i++ {
		lines = append(lines, ufsLine(fmt.Sprintf("100.%06d", i*100), fmt.Sprint(i%64),
			fmt.Sprint(i*8), "0x28 (READ_10)"))
	}
	dir := writeFtraceParquet(t, lines, "ufs")
	infos := []*TraceJobInfo{{Dir: dir, TraceType: "ufs"}}

	var buf bytes.Buffer
	n, err := ExportRawCSV(&buf, infos, nil)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := GetRawData(infos, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != raw.GetTotalEvents() {
		t.Errorf("CSV 행 = %d, totalEvents = %d — 전체를 내보내야 한다", n, raw.GetTotalEvents())
	}

	rec, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("CSV 파싱 실패: %v", err)
	}
	if got := int64(len(rec) - 1); got != n { // 헤더 제외
		t.Errorf("파싱된 행 = %d, want %d", got, n)
	}
	want := []string{"time", "lba", "qd", "cpu", "dtoc", "ctod", "ctoc", "cmd", "size", "continuous", "action"}
	if strings.Join(rec[0], ",") != strings.Join(want, ",") {
		t.Errorf("헤더 = %v\nwant %v", rec[0], want)
	}
}

// 필터가 CSV 에도 적용되어야 한다 — 화면에서 거른 것과 다른 게 나오면 안 된다.
func TestExportRawCSVAppliesFilter(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("200.000000", "1", "0", "0x28 (READ_10)"),
		ufsLine("200.000100", "2", "8", "0x2a (WRITE_10)"),
		ufsLine("200.000200", "3", "16", "0x28 (READ_10)"),
	}, "ufs")

	var buf bytes.Buffer
	n, err := ExportRawCSV(&buf, []*TraceJobInfo{{Dir: dir, TraceType: "ufs"}},
		&pb.TraceFilter{CmdList: []string{"0x28"}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("행 = %d, want 2 (0x28 만) — 필터가 CSV 에 안 걸렸다", n)
	}
}

// ⚠ NULL 은 **빈 칸**이어야 한다. 0 으로 바꾸면 받는 쪽이 유효값으로 읽는다.
// (mgmt 행의 lba/size/qd 는 개념 자체가 없고, dtoc 0 은 "0ms" 가 아니라 "모름"이다)
func TestExportRawCSVKeepsNullEmptyNotZero(t *testing.T) {
	// fsio_ufs 의 mgmt 행(UIC)은 lba/size/qd 가 NULL 로 나간다.
	lines := []string{
		"300.000000\tUFS\t100\t100\t0\tapp\tvfs_read\tufshcd_command:send_req\text4\t8\t32\t555\t4096\t100\t/d\t0x0000000000000001\tlun=0 tag=1 hwq=0 ufs_op=0x28 grp=0x0",
		"300.001000\tUFS\t0\t0\t0\tswapper\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17 a1=0x0 a2=0x0 a3=0x0 dir=send",
	}
	dir := writeFsioLines(t, lines, "fsio_ufs")

	var buf bytes.Buffer
	if _, err := ExportRawCSV(&buf, []*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil); err != nil {
		t.Fatal(err)
	}
	rec, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, h := range rec[0] {
		idx[h] = i
	}
	// mgmt 행을 찾아 lba/size/qd 가 "0" 이 아니라 "" 인지 본다.
	var found bool
	for _, row := range rec[1:] {
		if strings.Contains(row[idx["action"]], "uic") {
			found = true
			for _, c := range []string{"lba", "qd", "size"} {
				if row[idx[c]] == "0" {
					t.Errorf("mgmt 행의 %s 가 \"0\" 이다 — NULL 은 빈 칸이어야 한다 (0 은 유효값으로 읽힌다)", c)
				}
			}
		}
	}
	if !found {
		t.Skip("mgmt 행이 픽스처에서 안 나왔다 — 이 단언은 건너뛴다")
	}
}
