// Package storage 의 HTTP uploader — Trace Archive 흐름에서 portal 이 발급한 presigned URL 을
// 사용해 nginx 경유 MinIO 로 PUT 하는 용도.
//
// 단방향 원칙: agent 는 portal 로 outbound 호출 안 함. presigned URL 은 portal 이 RPC 인자로 넘김.
//
// 기존 minio-go SDK 기반 직접 PUT (UploadTraceToMinio / UploadParquetFiles) 와 별개:
// 그쪽은 legacy 흐름이고, archive 흐름은 nginx 경유라 SDK 의 endpoint 신호 의존을 회피해야 함.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// PartURL 은 presigned multipart 의 part 1개에 대응. portal 이 발급해 보냄.
type PartURL struct {
	PartNumber int32
	URL        string
}

// CompletedPart 는 PUT 결과로 받은 ETag 를 portal 에 보고할 때 사용.
type CompletedPart struct {
	PartNumber int32
	ETag       string
}

// uploadConcurrency 는 multipart 업로드 시 동시에 진행하는 part 수.
// frontend 의 trace 업로드와 동일 정책 (4-way) — partSize=64MB 기본.
const uploadConcurrency = 4

// httpClient 는 무제한 timeout (큰 part PUT 용). idle conn 만 재사용.
var httpClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: uploadConcurrency,
		IdleConnTimeout:     90 * time.Second,
	},
}

// UploadFilePresigned — 단일 PUT 으로 파일 전체 업로드. 작은 파일(< partSize) 케이스용.
// presignedURL 은 portal 이 발급한 nginx 경유 URL.
// 응답 헤더 ETag 를 추출해 반환 (S3 multipart complete 에 필요).
func UploadFilePresigned(ctx context.Context, localPath, presignedURL string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", localPath, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, presignedURL, f)
	if err != nil {
		return "", err
	}
	req.ContentLength = fi.Size()
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT %s: %w", localPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("PUT %s: HTTP %d: %s", localPath, resp.StatusCode, string(body))
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		// 일부 nginx 설정에서 헤더가 누락 — fail soft 가 아니라 hard fail (multipart complete 가 깨짐).
		return "", fmt.Errorf("PUT %s: ETag header missing", localPath)
	}
	return etag, nil
}

// PartProgress 는 part 1개 완료 시 호출자에게 알리는 콜백 인자.
type PartProgress struct {
	PartNumber int32
	ETag       string
	BytesPut   int64 // 이 part 의 크기
}

// UploadFileMultipart — 큰 파일을 partSize 단위로 잘라 동시 PUT. 각 part 완료마다 onProgress 호출.
// 반환값: parts 정렬되어 있음 (partNumber ASC) → portal 이 그대로 S3 complete 에 사용 가능.
//
// 주의: 메모리 사용량은 (uploadConcurrency × partSize). 64MB × 4 = 256MB peak.
func UploadFileMultipart(
	ctx context.Context,
	localPath string,
	parts []PartURL,
	partSize int64,
	onProgress func(PartProgress),
) ([]CompletedPart, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("no parts provided")
	}
	if partSize <= 0 {
		return nil, fmt.Errorf("invalid partSize: %d", partSize)
	}

	f, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", localPath, err)
	}
	totalSize := fi.Size()

	completed := make([]CompletedPart, len(parts))
	errCh := make(chan error, len(parts))
	sem := make(chan struct{}, uploadConcurrency)
	var wg sync.WaitGroup

	for i, p := range parts {
		i := i
		p := p

		offset := int64(p.PartNumber-1) * partSize
		size := partSize
		if offset+size > totalSize {
			size = totalSize - offset
		}
		if size <= 0 {
			return nil, fmt.Errorf("part %d offset out of range (offset=%d totalSize=%d)",
				p.PartNumber, offset, totalSize)
		}

		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			// 각 goroutine 이 part 영역만 메모리에 올림 — io.SectionReader 로 zero-copy.
			// 단, http.Client 가 retry 시 body 를 다시 읽기 위해 별도 buffer 가 더 안전.
			buf := make([]byte, size)
			if _, rerr := f.ReadAt(buf, offset); rerr != nil && rerr != io.EOF {
				errCh <- fmt.Errorf("part %d read: %w", p.PartNumber, rerr)
				return
			}

			req, rerr := http.NewRequestWithContext(ctx, http.MethodPut, p.URL, bytes.NewReader(buf))
			if rerr != nil {
				errCh <- fmt.Errorf("part %d req: %w", p.PartNumber, rerr)
				return
			}
			req.ContentLength = size
			req.Header.Set("Content-Type", "application/octet-stream")

			resp, perr := httpClient.Do(req)
			if perr != nil {
				errCh <- fmt.Errorf("part %d PUT: %w", p.PartNumber, perr)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				errCh <- fmt.Errorf("part %d PUT: HTTP %d: %s",
					p.PartNumber, resp.StatusCode, string(body))
				return
			}
			etag := resp.Header.Get("ETag")
			if etag == "" {
				errCh <- fmt.Errorf("part %d PUT: ETag header missing", p.PartNumber)
				return
			}

			completed[i] = CompletedPart{PartNumber: p.PartNumber, ETag: etag}
			if onProgress != nil {
				onProgress(PartProgress{PartNumber: p.PartNumber, ETag: etag, BytesPut: size})
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for e := range errCh {
		if e != nil {
			return nil, e
		}
	}
	return completed, nil
}
