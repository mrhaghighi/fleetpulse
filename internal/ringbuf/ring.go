package ringbuf

// Ring is a fixed-capacity FIFO that overwrites the oldest element when full.
// It is not safe for concurrent use; the caller must guard it.
type Ring[T any] struct {
	buf   []T
	start int
	count int
}

// New returns a ring that stores at most capacity elements. A capacity of zero
// or less returns a ring that stores nothing.
func New[T any](capacity int) *Ring[T] {
	if capacity < 0 {
		capacity = 0
	}
	return &Ring[T]{buf: make([]T, capacity)}
}

// Push appends v, overwriting the oldest element when the ring is full.
func (r *Ring[T]) Push(v T) {
	if len(r.buf) == 0 {
		return
	}

	if r.count < len(r.buf) {
		r.buf[(r.start+r.count)%len(r.buf)] = v
		r.count++
		return
	}

	r.buf[r.start] = v
	r.start = (r.start + 1) % len(r.buf)
}

// Len returns the number of stored elements.
func (r *Ring[T]) Len() int {
	return r.count
}

// Each calls fn for every element from oldest to newest. It stops early when
// fn returns false.
func (r *Ring[T]) Each(fn func(T) bool) {
	for i := 0; i < r.count; i++ {
		if !fn(r.buf[(r.start+i)%len(r.buf)]) {
			return
		}
	}
}
