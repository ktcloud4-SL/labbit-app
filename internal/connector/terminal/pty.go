package terminal

import (
	"bytes"
	"io"
	"sync"
)

// PTYChannel 은 가상 터미널(PTY)의 최소 입출력 및 리사이즈 추상화 인터페이스입니다.
type PTYChannel interface {
	io.Reader
	io.Writer
	io.Closer
	Resize(cols, rows int) error
}

// MockPTY 는 테스트 및 시뮬레이션을 위한 In-memory Echo/Shell PTY 구현체입니다.
type MockPTY struct {
	mu       sync.Mutex
	inPipe   *io.PipeReader
	inWriter *io.PipeWriter

	outPipe   *io.PipeReader
	outWriter *io.PipeWriter

	Cols int
	Rows int

	closed bool
}

// NewMockEchoPTY 는 입력된 바이트를 그대로 출력으로 에코하는 Mock PTY를 생성합니다.
func NewMockEchoPTY(cols, rows int) *MockPTY {
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()

	pty := &MockPTY{
		inPipe:    inReader,
		inWriter:  inWriter,
		outPipe:   outReader,
		outWriter: outWriter,
		Cols:      cols,
		Rows:      rows,
	}

	// Echo 루프: stdin 입력을 읽어 stdout 출력으로 즉시 복사
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := inReader.Read(buf)
			if err != nil {
				outWriter.Close()
				return
			}
			if n > 0 {
				pty.mu.Lock()
				_, writeErr := outWriter.Write(buf[:n])
				pty.mu.Unlock()
				if writeErr != nil {
					return
				}
			}
		}
	}()

	return pty
}

// Read 는 PTY stdout 출력을 읽습니다.
func (m *MockPTY) Read(p []byte) (n int, err error) {
	return m.outPipe.Read(p)
}

// Write 는 PTY stdin 입력을 전달합니다.
func (m *MockPTY) Write(p []byte) (n int, err error) {
	return m.inWriter.Write(p)
}

// Close 는 PTY 파이프를 닫습니다.
func (m *MockPTY) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.inWriter.Close()
	m.outWriter.Close()
	return nil
}

// Resize 는 창 크기를 변경합니다.
func (m *MockPTY) Resize(cols, rows int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Cols = cols
	m.Rows = rows
	return nil
}

// BufferPTY 는 고정된 출력을 제공하거나 기록하는 간단한 버퍼 PTY입니다.
type BufferPTY struct {
	mu     sync.Mutex
	InBuf  bytes.Buffer
	OutBuf bytes.Buffer
	Cols   int
	Rows   int
	Closed bool
}

func NewBufferPTY(cols, rows int) *BufferPTY {
	return &BufferPTY{Cols: cols, Rows: rows}
}

func (b *BufferPTY) Read(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.OutBuf.Read(p)
}

func (b *BufferPTY) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.InBuf.Write(p)
}

func (b *BufferPTY) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Closed = true
	return nil
}

func (b *BufferPTY) Resize(cols, rows int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Cols = cols
	b.Rows = rows
	return nil
}
