package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReparseFindsJobFolderTrace — agent 재시작 후 **잡 폴더 안**의 trace 를
// reparse 할 수 있는지.
//
// 시나리오로 수집한 trace 는 outputBase/<id> 가 아니라
// <archiveBase>/jobs/<이름>/trace/<id> 에 있다. 예전엔 ReparseTrace 가
// outputBase 만 봐서 재시작 뒤에는 늘 "trace.log not found" 로 실패했다.
// 조회(GetTraceJobInfo)는 findTraceDirByID 로 두 곳을 다 보기 때문에
// **화면엔 보이는데 재파싱만 안 되는** 상태가 됐다.
func TestReparseFindsJobFolderTrace(t *testing.T) {
	outBase := t.TempDir()
	archive := t.TempDir()

	const jobID = "job-abc"
	dir := filepath.Join(archive, "jobs", "20260828_scenario", "trace", jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 파싱 대상이 있어야 복원 분기를 통과한다.
	if err := os.WriteFile(filepath.Join(dir, "trace.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// trace_type 을 산출물 파일명에서 되찾는지도 함께 고정한다.
	if err := os.WriteFile(filepath.Join(dir, "result_fsio_ufs.parquet"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(nil, "", outBase)
	m.AddSearchRoot(archive)

	if err := m.ReparseTrace(jobID); err != nil {
		t.Fatalf("잡 폴더 안의 trace 를 못 찾았다: %v", err)
	}

	job, err := m.GetJob(jobID)
	if err != nil {
		t.Fatalf("복원된 잡이 없다: %v", err)
	}
	if job.OutputDir != dir {
		t.Errorf("OutputDir = %q, want %q", job.OutputDir, dir)
	}
	if job.TraceType != "fsio_ufs" {
		t.Errorf("TraceType = %q, want fsio_ufs (파일명에서 되찾아야 한다)", job.TraceType)
	}
}

// outputBase 쪽 경로는 그대로 동작해야 한다 (단독 trace 실행).
func TestReparseStillFindsOutputBase(t *testing.T) {
	outBase := t.TempDir()
	const jobID = "job-solo"
	dir := filepath.Join(outBase, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trace.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(nil, "", outBase)
	if err := m.ReparseTrace(jobID); err != nil {
		t.Fatalf("outputBase 경로가 깨졌다: %v", err)
	}
}
