package parser

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress"
	"github.com/parquet-go/parquet-go/compress/snappy"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// 1MB 이상이면 ZSTD, 미만이면 SNAPPY — Rust 정책과 동일.
// 단, parquet-go 는 file size 가 아니라 row group 단위 압축이므로 row 수 기준으로 단순 분기.
const compressSwitchRows = 1 << 14 // 16K rows ≈ 1MB 근사

// WriteUFSParquet — events 를 outputDir/result_ufs.parquet 으로 atomic 쓰기한다.
// `.tmp` 에 먼저 쓰고 fsync → close → rename → 부모 디렉토리 fsync.
func WriteUFSParquet(events []UFSEvent, outputDir string) error {
	if len(events) == 0 {
		return nil
	}
	return writeAtomic(outputDir, "result_ufs.parquet", func(f *os.File) error {
		comp := chooseCompression(len(events))
		w := parquet.NewGenericWriter[UFSEvent](f, parquet.Compression(comp))
		if _, err := w.Write(events); err != nil {
			return err
		}
		return w.Close()
	})
}

// WriteBlockParquet — block 이벤트 parquet 쓰기.
func WriteBlockParquet(events []BlockEvent, outputDir string) error {
	if len(events) == 0 {
		return nil
	}
	return writeAtomic(outputDir, "result_block.parquet", func(f *os.File) error {
		comp := chooseCompression(len(events))
		w := parquet.NewGenericWriter[BlockEvent](f, parquet.Compression(comp))
		if _, err := w.Write(events); err != nil {
			return err
		}
		return w.Close()
	})
}

// WriteUFSCustomParquet — ufscustom 이벤트 parquet 쓰기.
func WriteUFSCustomParquet(events []UFSCustomEvent, outputDir string) error {
	if len(events) == 0 {
		return nil
	}
	return writeAtomic(outputDir, "result_ufscustom.parquet", func(f *os.File) error {
		comp := chooseCompression(len(events))
		w := parquet.NewGenericWriter[UFSCustomEvent](f, parquet.Compression(comp))
		if _, err := w.Write(events); err != nil {
			return err
		}
		return w.Close()
	})
}

func chooseCompression(nrows int) compress.Codec {
	if nrows >= compressSwitchRows {
		return &zstd.Codec{}
	}
	return &snappy.Codec{}
}

// writeAtomic — outputDir/filename.tmp 에 쓰고 동일 디렉토리에서 rename. 부모 fsync 까지.
// crash-safe 한 쓰기 패턴 (POSIX rename atomic + 부모 디렉토리 fsync).
func writeAtomic(outputDir, filename string, writeFn func(*os.File) error) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("mkdir output: %w", err)
	}
	finalPath := filepath.Join(outputDir, filename)
	tmpPath := finalPath + ".tmp"

	// 기존 .tmp 가 남아있으면 정리 (이전 실패 흔적)
	_ = os.Remove(tmpPath)

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmpPath) }

	if err := writeFn(f); err != nil {
		cleanup()
		return fmt.Errorf("write parquet: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	// 부모 디렉토리 fsync — crash 후 rename 살아남기 위해.
	if dir, err := os.Open(outputDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// WriteFsioUfsParquet — bpftrace UFS 이벤트 parquet 쓰기.
// 파일명은 Rust `save_to_parquet` 의 `{prefix}_fsio_ufs.parquet` 과 맞춘다.
func WriteFsioUfsParquet(events []FsioUfsEvent, outputDir string) error {
	if len(events) == 0 {
		return nil
	}
	return writeAtomic(outputDir, "result_fsio_ufs.parquet", func(f *os.File) error {
		comp := chooseCompression(len(events))
		w := parquet.NewGenericWriter[FsioUfsEvent](f, parquet.Compression(comp))
		if _, err := w.Write(events); err != nil {
			return err
		}
		return w.Close()
	})
}

// WriteFsioBlockParquet — bpftrace BLK 이벤트 parquet 쓰기.
func WriteFsioBlockParquet(events []FsioBlockEvent, outputDir string) error {
	if len(events) == 0 {
		return nil
	}
	return writeAtomic(outputDir, "result_fsio_block.parquet", func(f *os.File) error {
		comp := chooseCompression(len(events))
		w := parquet.NewGenericWriter[FsioBlockEvent](f, parquet.Compression(comp))
		if _, err := w.Write(events); err != nil {
			return err
		}
		return w.Close()
	})
}
