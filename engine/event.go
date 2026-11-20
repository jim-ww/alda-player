package engine

type kind uint8

const (
	kindNoteOn kind = iota
	kindNoteOff
	kindProgram
	kindControl
	kindTempo
	kindFinished
	kindShutdown
)

// MIDI status bytes and controllers.
const (
	statusNoteOff = 0x80
	statusNoteOn  = 0x90
	statusControl = 0xB0
	statusProgram = 0xC0

	ccPanning    = 10
	ccExpression = 11
)

// event is a point on the clock. seq orders events with equal ms by
// scheduling order.
type event struct {
	ms   int64
	seq  uint64
	bpm  float32 // kindTempo
	kind kind
	ch   uint8
	d1   uint8
	d2   uint8
}

func (e event) isMIDI() bool { return e.kind <= kindControl }

func (e event) before(o event) bool {
	if e.ms != o.ms {
		return e.ms < o.ms
	}
	return e.seq < o.seq
}

// queue is a min-heap of events ordered by (ms, seq).
type queue []event

func (q *queue) push(e event) {
	*q = append(*q, e)
	h := *q
	i := len(h) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !h[i].before(h[p]) {
			break
		}
		h[i], h[p] = h[p], h[i]
		i = p
	}
}

func (q *queue) pop() event {
	h := *q
	top := h[0]
	n := len(h) - 1
	h[0] = h[n]
	*q = h[:n]
	q.down(0)
	return top
}

// init restores the heap invariant over arbitrary contents.
func (q queue) init() {
	for i := len(q)/2 - 1; i >= 0; i-- {
		q.down(i)
	}
}

func (q queue) down(i int) {
	for {
		l := 2*i + 1
		if l >= len(q) {
			return
		}
		m := l
		if r := l + 1; r < len(q) && q[r].before(q[l]) {
			m = r
		}
		if !q[m].before(q[i]) {
			return
		}
		q[i], q[m] = q[m], q[i]
		i = m
	}
}
