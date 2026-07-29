package framework

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

var transportBenchmarkSink any

func BenchmarkTransportJSONRoundTrip(b *testing.B) {
	for _, size := range []int{1 << 10, 64 << 10, 1 << 20, 3 << 20} {
		b.Run(transportBenchmarkSize(size), func(b *testing.B) {
			response := Response{
				ID: "benchmark",
				Result: map[string]any{
					"payload": string(bytes.Repeat([]byte{'x'}, size)),
				},
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				b.Fatalf("marshal fixture: %v", err)
			}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				current, err := json.Marshal(response)
				if err != nil {
					b.Fatalf("marshal: %v", err)
				}
				var decoded Response
				if err := json.Unmarshal(current, &decoded); err != nil {
					b.Fatalf("unmarshal: %v", err)
				}
				transportBenchmarkSink = decoded.Result
			}
			b.ReportMetric(float64(len(encoded)), "wire_bytes/op")
		})
	}
}

func BenchmarkTransportLengthPrefixedPipe(b *testing.B) {
	for _, size := range []int{1 << 10, 64 << 10, 1 << 20, 3 << 20, 16 << 20} {
		b.Run(transportBenchmarkSize(size), func(b *testing.B) {
			payload := bytes.Repeat([]byte{0xa5}, size)
			reader, writer, err := os.Pipe()
			if err != nil {
				b.Fatalf("create pipe: %v", err)
			}
			b.Cleanup(func() {
				_ = reader.Close()
				_ = writer.Close()
			})

			var header [4]byte
			binary.BigEndian.PutUint32(header[:], uint32(size))
			readDone := make(chan error, 1)
			readReady := make(chan struct{})
			go func() {
				incomingHeader := make([]byte, len(header))
				incomingPayload := make([]byte, size)
				close(readReady)
				for range b.N {
					if _, err := io.ReadFull(reader, incomingHeader); err != nil {
						readDone <- fmt.Errorf("read header: %w", err)
						return
					}
					if got := binary.BigEndian.Uint32(incomingHeader); got != uint32(size) {
						readDone <- fmt.Errorf("frame size = %d, want %d", got, size)
						return
					}
					if _, err := io.ReadFull(reader, incomingPayload); err != nil {
						readDone <- fmt.Errorf("read payload: %w", err)
						return
					}
				}
				transportBenchmarkSink = incomingPayload
				readDone <- nil
			}()
			<-readReady

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				if err := transportBenchmarkWriteAll(writer, header[:]); err != nil {
					b.Fatalf("write header: %v", err)
				}
				if err := transportBenchmarkWriteAll(writer, payload); err != nil {
					b.Fatalf("write payload: %v", err)
				}
			}
			b.StopTimer()
			if err := <-readDone; err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(size+len(header)), "wire_bytes/op")
		})
	}
}

func BenchmarkTransportManagedFileRoundTrip(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 3 << 20, 16 << 20} {
		b.Run(transportBenchmarkSize(size), func(b *testing.B) {
			content := bytes.Repeat([]byte{0xa5}, size)
			expected := sha256.Sum256(content)
			store, err := NewFileTransferStore(FileTransferOptions{
				RootDir:  b.TempDir(),
				TTL:      time.Minute,
				MaxBytes: int64(size),
			})
			if err != nil {
				b.Fatalf("create store: %v", err)
			}
			b.Cleanup(func() {
				if err := store.Close(); err != nil {
					b.Errorf("close store: %v", err)
				}
			})
			copyBuffer := make([]byte, 64<<10)

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				reference, err := store.Stage(
					context.Background(),
					"benchmark.bin",
					"application/octet-stream",
					bytes.NewReader(content),
				)
				if err != nil {
					b.Fatalf("stage: %v", err)
				}
				download, err := store.Take(reference.ID)
				if err != nil {
					b.Fatalf("take: %v", err)
				}
				hash := sha256.New()
				written, copyErr := io.CopyBuffer(hash, download, copyBuffer)
				closeErr := download.Close()
				if copyErr != nil {
					b.Fatalf("copy: %v", copyErr)
				}
				if closeErr != nil {
					b.Fatalf("close download: %v", closeErr)
				}
				if written != int64(size) ||
					!bytes.Equal(hash.Sum(nil), expected[:]) {
					b.Fatalf("download verification failed")
				}
			}
		})
	}
}

func transportBenchmarkSize(size int) string {
	if size >= 1<<20 {
		return fmt.Sprintf("%dMiB", size>>20)
	}
	return fmt.Sprintf("%dKiB", size>>10)
}

func transportBenchmarkWriteAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[written:]
	}
	return nil
}
