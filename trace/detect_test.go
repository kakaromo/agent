package trace

import (
	"strings"
	"testing"
)

// TestDetectUpload — 포맷 자동 판별.
//
// ⚠ **틀린 판별은 조용히 0건이 된다.** trace_type 이 어긋나면 파서가 아무 행도
// 못 만들고 에러도 안 낸다("수집은 됐는데 비어 있다"). 그래서 못 정하면 추측하지
// 않고 실패시키는 것까지 테스트한다.
func TestDetectUpload(t *testing.T) {
	// 실제 수집 로그에서 가져온 형태.
	ftraceUFS := ` Aria-Stats-thre-19154   [000] ..... 3640320.211429: ufshcd_command: send_req: 1d84000.ufshc: tag: 40, DB: 0x0, size: 4096, IS: 0, LBA: 7058430, opcode: 0x2a (WRITE_10), group_id: 0x0, hwq_id: 0`
	ftraceBlk := `           <idle>-0     [003] d.h2. 1234.567890: block_rq_complete: 8,0 W () 123456 + 8 [0]`

	// fsio TSV — 17컬럼 TAB. layer 는 2번째.
	fsioCols := func(layer string) string {
		c := make([]string, 17)
		for i := range c {
			c[i] = "-"
		}
		c[0] = "3640320.211429"
		c[1] = layer
		return strings.Join(c, "\t")
	}

	tests := []struct {
		name     string
		body     string
		wantKind string
		wantType string
		wantErr  bool
	}{
		{"ftrace UFS", ftraceUFS, UploadKindTrace, "ufs", false},
		{"ftrace Block", ftraceBlk, UploadKindTrace, "block", false},
		{"ftrace 혼합 → both", ftraceUFS + "\n" + ftraceBlk, UploadKindTrace, "both", false},
		{"fsio UFS", fsioCols("UFS"), UploadKindTrace, "fsio_ufs", false},
		{"fsio Block", fsioCols("BLK"), UploadKindTrace, "fsio_block", false},
		{
			// VFS 행은 계열 판정에 표를 주지 않는다 — 두 계열 모두에 섞여 나오므로.
			"fsio UFS + VFS 행", fsioCols("UFS") + "\n" + fsioCols("VFS"),
			UploadKindTrace, "fsio_ufs", false,
		},
		{
			"벤치마크 JSON",
			`{"deviceId":"2-1.1.2","tool":"fio","metrics":{},"success":true}`,
			UploadKindBenchmark, "", false,
		},
		{"빈 파일", "", "", "", true},
		{"모르는 텍스트", "hello world\nsecond line", "", "", true},
		{
			// 시나리오 export 를 벤치마크로 받으면 화면이 이상해진다 — 거부해야 한다.
			"벤치마크 아닌 JSON", `{"name":"유튜브","steps":[]}`, "", "", true,
		},
		{
			// fsio 는 --only 로 한 계열만 받는다. 섞였으면 수집이 잘못된 것.
			"fsio UFS+BLK 혼합", fsioCols("UFS") + "\n" + fsioCols("BLK"), "", "", true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectUpload(strings.NewReader(tt.body), "upload.log")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("에러를 기대했는데 통과했다: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("판별 실패: %v", err)
			}
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.TraceType != tt.wantType {
				t.Errorf("TraceType = %q, want %q", got.TraceType, tt.wantType)
			}
			if got.Reason == "" {
				t.Error("Reason 이 비었다 — 사용자에게 근거를 보여줘야 한다")
			}
		})
	}
}

// TestDetectUploadRejectsTabNonFsio — TAB 이 있다고 fsio 로 오인하면 안 된다.
func TestDetectUploadRejectsTabNonFsio(t *testing.T) {
	// 17컬럼이지만 1번째가 실수가 아니고 layer 도 아니다.
	cols := make([]string, 17)
	for i := range cols {
		cols[i] = "col"
	}
	if _, err := DetectUpload(strings.NewReader(strings.Join(cols, "\t")), "x.tsv"); err == nil {
		t.Error("fsio 가 아닌 TSV 가 통과했다")
	}
}
