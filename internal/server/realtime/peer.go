package realtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	// errQueueFull은 bounded 전송 queue의 한도를 넘어 frame을 받지 못했음이다.
	errQueueFull = errors.New("realtime: 전송 queue 한도 초과")
	// errPeerClosed는 종료가 시작되었거나 끝난 connection에 보내려 했음이다. 아무것도 쓰지 않았다.
	errPeerClosed = errors.New("realtime: connection이 종료됨")
	// errJSONTooLarge는 JSON Text message가 1 MiB 한도를 넘었음이다. 계약상 close 1009다.
	errJSONTooLarge = errors.New("realtime: JSON Text message가 한도를 넘음")
)

// closeKind는 queue 안에서 "여기서 close frame을 보내고 끝낸다"를 나타내는 frame 종류다.
const closeKind = -1

// frame은 WebSocket message 하나다. 전송 중인 byte를 잠시 담을 뿐 저장하지 않는다.
type frame struct {
	kind int // websocket.TextMessage, websocket.BinaryMessage, closeKind
	data []byte
	// code와 text는 closeKind의 close code와 reason이다.
	code int
	text string
}

// outQueue는 connection 하나의 bounded 전송 queue다. byte와 message 수를 모두 제한한다.
// history나 replay 용도가 아니며 connection이 끝나면 버린다.
type outQueue struct {
	maxBytes int
	maxItems int

	mu     sync.Mutex
	items  []frame
	bytes  int
	closed bool
	// graceful은 close frame을 보내기로 하고 닫았음을(closeWith) 뜻한다. writer 실패로 버린(abort) 경우는 아니다.
	graceful bool

	// ready는 writer에게 frame이 생겼음을, space는 pushWait 중인 producer에게 자리가 생겼음을 알린다(각각 용량 1).
	ready chan struct{}
	space chan struct{}
	// done은 close 이후 닫혀 대기 중인 producer와 writer를 깨운다.
	done chan struct{}
}

func newOutQueue(maxBytes, maxItems int) *outQueue {
	return &outQueue{
		maxBytes: maxBytes,
		maxItems: maxItems,
		ready:    make(chan struct{}, 1),
		space:    make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// push는 기다리지 않고 frame을 넣는다. 한도를 넘으면 errQueueFull, 닫혔으면 errPeerClosed다.
// 비어 있는 queue에는 frame 하나가 byte 한도보다 커도 넣는다. 그렇지 않으면 그 frame은 영원히 전송할 수 없다.
func (q *outQueue) push(f frame) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errPeerClosed
	}
	if len(q.items) > 0 && (len(q.items) >= q.maxItems || q.bytes+len(f.data) > q.maxBytes) {
		return errQueueFull
	}
	q.items = append(q.items, f)
	q.bytes += len(f.data)
	signal(q.ready)
	return nil
}

// pushWait는 자리가 생길 때까지 기다려 frame을 넣는다. 수신자가 느리면 이 호출자만 기다린다(backpressure).
// ctx가 끝나거나 queue가 닫히면 기다림을 끝낸다.
func (q *outQueue) pushWait(ctx context.Context, f frame) error {
	for {
		err := q.push(f)
		if !errors.Is(err, errQueueFull) {
			return err
		}
		select {
		case <-q.space:
		case <-q.done:
			return errPeerClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// next는 다음 frame을 기다려 꺼낸다. queue가 닫혔고 남은 frame이 없으면 false다.
func (q *outQueue) next() (frame, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			f := q.items[0]
			q.items[0] = frame{} // 참조를 놓아 byte slice가 남지 않게 한다.
			q.items = q.items[1:]
			q.bytes -= len(f.data)
			q.mu.Unlock()
			signal(q.space)
			return f, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return frame{}, false
		}
		<-q.ready
	}
}

// closeWith는 tail frame을 마지막으로 넣고 queue를 닫는다. 마지막 tail은 close frame이어야 한다.
// 이후 push는 errPeerClosed다. 처음 호출만 적용한다. tail은 한도와 무관하게 넣는다.
// flush가 false이면 아직 보내지 않은 frame을 버린다(느린 수신자).
func (q *outQueue) closeWith(flush bool, tail ...frame) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.graceful = true
	if !flush {
		q.items, q.bytes = nil, 0
	}
	q.items = append(q.items, tail...)
	close(q.done)
	signal(q.ready)
}

// closeWithFinal은 마지막 application frame과 close frame을 하나의 atomic tail로 queue에 넣고 닫는다.
// 닫힌 이후의 push는 즉시 errPeerClosed로 거절된다. 처음 호출만 적용한다.
// 정상 수신자(한도 내)는 기존 queue를 flush하고 마지막 frame과 close frame을 받는다.
// 이미 한도를 초과한 느린 수신자는 밀린 backlog를 버리고(flush=false) 마지막 frame과 close frame을 받는다.
func (q *outQueue) closeWithFinal(kind int, data []byte, code int, text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.graceful = true
	if len(q.items) > 0 && (len(q.items) >= q.maxItems || q.bytes >= q.maxBytes) {
		q.items, q.bytes = nil, 0
	}
	q.items = append(q.items,
		frame{kind: kind, data: data},
		frame{kind: closeKind, code: code, text: text},
	)
	close(q.done)
	signal(q.ready)
}

// isGraceful은 close frame을 보내기로 하고 닫았는지 반환한다.
func (q *outQueue) isGraceful() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.graceful
}

// abort는 writer가 더 쓸 수 없을 때 queue를 닫고 남은 frame을 버린다.
func (q *outQueue) abort() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.items, q.bytes = nil, 0
	close(q.done)
	signal(q.ready)
}

// peer는 WebSocket connection 하나의 전송 경계다.
//
// 모든 application write는 이 peer의 writer goroutine 하나가 queue 순서대로 수행하므로 connection마다 writer가 하나다.
// 다른 goroutine은 queue에 넣기만 하고 *websocket.Conn에 직접 쓰지 않는다. 읽기는 connection을 소유한 serve goroutine이 한다.
type peer struct {
	ws           *websocket.Conn
	q            *outQueue
	writeTimeout time.Duration
	closeGrace   time.Duration

	done chan struct{} // writer가 끝나면 닫힌다.
}

func newPeer(ws *websocket.Conn, queueBytes, queueItems int, writeTimeout, closeGrace time.Duration) *peer {
	p := &peer{
		ws:           ws,
		q:            newOutQueue(queueBytes, queueItems),
		writeTimeout: writeTimeout,
		closeGrace:   closeGrace,
		done:         make(chan struct{}),
	}
	go p.writeLoop()
	return p
}

func (p *peer) writeLoop() {
	defer close(p.done)
	for {
		f, ok := p.q.next()
		if !ok {
			return
		}
		if f.kind == closeKind {
			_ = p.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(f.code, f.text), time.Now().Add(p.writeTimeout))
			// 상대가 close에 응답하지 않아도 connection이 정리되도록 일정 시간 뒤 TCP connection을 닫는다.
			time.AfterFunc(p.closeGrace, func() { _ = p.ws.Close() })
			return
		}
		_ = p.ws.SetWriteDeadline(time.Now().Add(p.writeTimeout))
		if err := p.ws.WriteMessage(f.kind, f.data); err != nil {
			// 더 쓸 수 없다. 남은 frame을 버리고 connection을 닫아 read loop가 끝나게 한다.
			p.q.abort()
			_ = p.ws.Close()
			return
		}
	}
}

// send는 기다리지 않고 frame 하나를 보낸다. 한도를 넘으면 errQueueFull이다.
func (p *peer) send(kind int, data []byte) error { return p.q.push(frame{kind: kind, data: data}) }

// sendWait는 queue에 자리가 생길 때까지 기다려 보낸다.
func (p *peer) sendWait(ctx context.Context, kind int, data []byte) error {
	return p.q.pushWait(ctx, frame{kind: kind, data: data})
}

// close는 close frame을 보내고 이후 write를 막는다. flush가 true이면 이미 queue에 있는 frame을 먼저 보낸다.
// 여러 번 호출해도 처음 것만 적용한다.
func (p *peer) close(code int, reason string, flush bool) {
	p.q.closeWith(flush, frame{kind: closeKind, code: code, text: reason})
}

// closeWithError는 아직 보내지 않은 frame을 버리고 ERROR JSON 하나를 보낸 뒤 close frame을 보낸다.
// queue가 가득 찬 느린 수신자나 protocol 위반 peer처럼 이미 쌓인 frame을 더 보낼 이유가 없을 때 사용한다.
func (p *peer) closeWithError(errorFrame []byte, code int, reason string) {
	p.q.closeWith(false,
		frame{kind: websocket.TextMessage, data: errorFrame},
		frame{kind: closeKind, code: code, text: reason},
	)
}

// closeWithFinalMessage는 마지막 application message와 close frame을 queue에 원자적으로 등록하고 queue를 닫는다.
// 등록과 동시에 queue가 closed 상태가 되므로 이후의 push는 즉시 errPeerClosed로 거절되어,
// 마지막 application message 뒤에 다른 data frame이 끼어드는 ordering race를 방지한다.
// 정상 수신자는 기존 queue를 flush하고, 이미 한도를 초과한 느린 수신자는 backlog를 버리고 final frame들을 전달한다.
func (p *peer) closeWithFinalMessage(kind int, data []byte, code int, reason string) {
	p.q.closeWithFinal(kind, data, code, reason)
}

// shutdown은 serve goroutine이 끝날 때 호출한다. 남은 frame을 버리고 writer를 끝내며 TCP connection을 닫는다.
//
// close frame을 보내기로 한 connection(ERROR 뒤의 close 등)은 곧바로 닫지 않는다. serve가 close를 queue에 넣고 바로 반환해도
// writer가 그 frame을 모두 보내야 하고, 상대가 close에 응답하거나 closeGrace가 지날 때까지 읽어서 버려야 한다. 아직 읽지 않은
// 입력이 남은 채 TCP connection을 닫으면 RST로 상대가 close frame을 받기 전에 연결이 reset될 수 있기 때문이다.
// 이미 종료되었거나 writer가 실패한 connection에서는 기다리지 않는다. 여러 번 호출해도 안전하다.
func (p *peer) shutdown() {
	if p.q.isGraceful() {
		select {
		case <-p.done:
		case <-time.After(p.writeTimeout + p.closeGrace):
		}
		_ = p.ws.SetReadDeadline(time.Now().Add(p.closeGrace))
		for {
			if _, _, err := p.ws.NextReader(); err != nil {
				break
			}
		}
	}
	p.q.abort()
	_ = p.ws.Close()
}

// isClosed는 종료가 시작되었는지 반환한다.
func (p *peer) isClosed() bool {
	select {
	case <-p.q.done:
		return true
	default:
		return false
	}
}

// readMessage는 message 하나를 읽는다. JSON Text는 maxText(1 MiB)를 넘는 만큼을 메모리에 적재하지 않고 errJSONTooLarge를 반환한다.
// Binary는 connection의 read limit(SetReadLimit)이 bounded read를 보장한다. 이 한도를 넘으면 gorilla가 close 1009를 보낸다.
func readMessage(ws *websocket.Conn, maxText int64) (kind int, data []byte, err error) {
	kind, r, err := ws.NextReader()
	if err != nil {
		return 0, nil, err
	}
	if kind != websocket.TextMessage {
		data, err = io.ReadAll(r)
		return kind, data, err
	}
	data, err = io.ReadAll(io.LimitReader(r, maxText+1))
	if err != nil {
		return kind, nil, err
	}
	if int64(len(data)) > maxText {
		return kind, nil, errJSONTooLarge
	}
	return kind, data, nil
}
