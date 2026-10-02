package trace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"agent/adb"
	pb "agent/pb"
)

// TestWaitCollectorNoKillBeforeStop — 정지 요청 전에는 상한이 지나도 수집 프로세스를
// 죽이지 않는다. 예전엔 시작 시점부터 타이머가 돌아 모든 trace 가 30초에 잘렸다.
func TestWaitCollectorNoKillBeforeStop(t *testing.T) {
	old := collectorKillGrace
	collectorKillGrace = 50 * time.Millisecond
	defer func() { collectorKillGrace = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fd, err := os.Create(filepath.Join(t.TempDir(), "trace.log"))
	if err != nil {
		t.Fatal(err)
	}

	finished := make(chan struct{})
	go func() {
		waitCollector(ctx, cmd, fd, "test")
		close(finished)
	}()

	// 상한(50ms)의 몇 배를 기다려도 살아 있어야 한다.
	select {
	case <-finished:
		t.Fatal("정지 요청 전에 수집 프로세스가 종료됐다")
	case <-time.After(500 * time.Millisecond):
	}

	// 정지 요청 후에는 끝나야 한다.
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("정지 요청 후에도 끝나지 않았다")
	}
}

// TestStopTraceFsioWaitsDrain — fsio 정지 시 수집 프로세스가 스스로 끝나기 전에
// adb 를 cancel(SIGKILL) 하지 않는다. 먼저 죽이면 fsiotrace 가 ringbuf 를 배수하던
// 마지막 이벤트가 잘린다.
func TestStopTraceFsioWaitsDrain(t *testing.T) {
	m := NewManager(adb.NewManager(), "", t.TempDir())
	done := make(chan struct{})
	var cancelledEarly atomic.Bool
	job := &TraceJob{
		ID:        "j1",
		DeviceID:  "gone", // 기기 없음 → pkill 은 건너뛰고 대기·cancel 경로만 탄다
		TraceType: "fsio_ufs",
		State:     pb.JobState_JOB_STATE_RUNNING,
		LogFile:   filepath.Join(t.TempDir(), "trace.log"),
		adbCancel: func() {
			select {
			case <-done:
			default:
				cancelledEarly.Store(true)
			}
		},
		collectorDone: done,
	}
	m.jobs[job.ID] = job

	// fsiotrace 가 배수를 마치고 스스로 끝나는 데 걸리는 시간.
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(done)
	}()
	if err := m.StopTrace(job.ID); err != nil {
		t.Fatal(err)
	}
	if cancelledEarly.Load() {
		t.Fatal("수집 프로세스가 끝나기 전에 adb 를 cancel 했다")
	}
}
