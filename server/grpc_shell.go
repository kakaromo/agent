package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "agent/pb"
)

// Shell — 양방향 streaming gRPC. xterm.js (WebSocket bridge 또는 portal Spring 의 직접 호출)
// 으로부터 키 입력 / resize 를 받고, PTY stdout 을 client 로 push 한다.
//
// 프로토콜:
//  1. 첫 메시지는 반드시 ShellClientMsg.start (device_id, cols, rows). 그 외는 InvalidArgument.
//  2. 이후 ShellClientMsg.input / .resize 가 임의 순서로 흘러옴.
//  3. server 는 ShellOutput 을 청크 단위로 push. 자식 종료 시 ShellExit 한 번 보낸 뒤 stream close.
//
// 동시성:
//  - recv goroutine: stream.Recv → pty.Write / pty.Resize
//  - send goroutine: pty.Read → stream.Send (Output)
//  - 두 goroutine WaitGroup 추적, 어느 한쪽 종료 시 pty.Close 로 상대를 깨움.
func (s *DeviceAgentServer) Shell(stream grpc.BidiStreamingServer[pb.ShellClientMsg, pb.ShellServerMsg]) error {
	// 1. 첫 메시지 = ShellStart
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Canceled, "recv start: %v", err)
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be ShellStart")
	}
	if start.GetDeviceId() == "" {
		return status.Error(codes.InvalidArgument, "device_id required")
	}

	md, err := s.manager.GetDevice(start.GetDeviceId())
	if err != nil {
		return status.Errorf(codes.NotFound, "device not found: %s", start.GetDeviceId())
	}

	ctx := stream.Context()
	pty, err := md.Device.ShellPTY(ctx, start.GetCols(), start.GetRows())
	if err != nil {
		return status.Errorf(codes.Internal, "open pty: %v", err)
	}
	// 한 번만 Close 되도록 defer 와 goroutine 양쪽이 안전하게 호출 (PTYSession.Close 자체가 idempotent)
	defer pty.Close()

	slog.Info("shell session started",
		"device_id", start.GetDeviceId(),
		"cols", start.GetCols(),
		"rows", start.GetRows(),
	)

	// stream.Send 는 동시 호출 금지 — 본 핸들러에선 send goroutine 단독 사용이라 별도 mutex 불요.
	// (Exit 도 send goroutine 에서 보냄)

	var wg sync.WaitGroup
	recvDone := make(chan struct{})
	sendDone := make(chan struct{})

	// recv: client → pty
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(recvDone)
		for {
			msg, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					slog.Debug("shell recv error", "error", err)
				}
				return
			}
			switch p := msg.Payload.(type) {
			case *pb.ShellClientMsg_Input:
				if _, werr := pty.Write(p.Input.GetData()); werr != nil {
					return
				}
			case *pb.ShellClientMsg_Resize:
				if rerr := pty.Resize(p.Resize.GetCols(), p.Resize.GetRows()); rerr != nil {
					slog.Debug("pty resize error", "error", rerr)
				}
			case *pb.ShellClientMsg_Start:
				// 중복 start 무시 (이미 처리됨)
			}
		}
	}()

	// send: pty → client
	wg.Add(1)
	var sendErr error
	go func() {
		defer wg.Done()
		defer close(sendDone)
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				if serr := stream.Send(&pb.ShellServerMsg{
					Payload: &pb.ShellServerMsg_Output{
						Output: &pb.ShellOutput{Data: append([]byte(nil), buf[:n]...)},
					},
				}); serr != nil {
					sendErr = serr
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// 어느 한쪽이 끝나면 pty 를 닫아 상대도 깨운다.
	select {
	case <-recvDone:
		pty.Close()
	case <-sendDone:
		pty.Close()
	case <-ctx.Done():
		pty.Close()
	}
	wg.Wait()

	// pty.Wait 으로 exit code 받고 send (recv 끝났어도 stream.Send 는 가능).
	exitCode, waitErr := pty.Wait()
	exitMsg := ""
	if waitErr != nil {
		exitMsg = waitErr.Error()
	}
	// sendErr 가 이미 있으면 stream 이 깨졌으니 Send 시도 안 함.
	if sendErr == nil {
		_ = stream.Send(&pb.ShellServerMsg{
			Payload: &pb.ShellServerMsg_Exit{
				Exit: &pb.ShellExit{Code: int32(exitCode), Error: exitMsg},
			},
		})
	}

	slog.Info("shell session ended",
		"device_id", start.GetDeviceId(),
		"exit", exitCode,
	)
	if sendErr != nil {
		return fmt.Errorf("shell send: %w", sendErr)
	}
	return nil
}
