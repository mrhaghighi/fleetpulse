package ringbuf

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingPushAndEach(t *testing.T) {
	t.Parallel()

	r := New[int](3)
	r.Push(1)
	r.Push(2)

	require.Equal(t, 2, r.Len())
	require.Equal(t, []int{1, 2}, collect(r))
}

func TestRingWrapAround(t *testing.T) {
	t.Parallel()

	r := New[int](3)
	for i := 1; i <= 7; i++ {
		r.Push(i)
	}

	require.Equal(t, 3, r.Len())
	require.Equal(t, []int{5, 6, 7}, collect(r))
}

func TestRingEachStopsEarly(t *testing.T) {
	t.Parallel()

	r := New[int](5)
	for i := 1; i <= 5; i++ {
		r.Push(i)
	}

	var got []int
	r.Each(func(v int) bool {
		got = append(got, v)
		return len(got) < 3
	})

	require.Equal(t, []int{1, 2, 3}, got)
}

func TestRingZeroCapacity(t *testing.T) {
	t.Parallel()

	for _, capacity := range []int{0, -1} {
		r := New[int](capacity)
		r.Push(1)

		require.Zero(t, r.Len())
		r.Each(func(int) bool {
			t.Fatal("empty ring must not yield values")
			return true
		})
	}
}

func collect(r *Ring[int]) []int {
	var values []int
	r.Each(func(v int) bool {
		values = append(values, v)
		return true
	})
	return values
}
