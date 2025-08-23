package promfp

import (
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"
)

func TestFingerprint_InvalidQueries(t *testing.T) {
	tests := []string{"", "up{", "rate("}
	for _, q := range tests {
		t.Run(q, func(t *testing.T) {
			h, c := Fingerprint(q, false)
			require.Equal(t, "", h)
			require.Equal(t, "", c)
		})
	}
}

func TestFingerprint_LabelOrderAndValuesMasked(t *testing.T) {
	q1 := `foo{b="1",a="2"}`
	q2 := `foo{a="X",b="Y"}`

	h1, c1 := Fingerprint(q1, false)
	h2, c2 := Fingerprint(q2, false)

	require.NotEmpty(t, c1)
	require.Equal(t, c1, c2, "canonical must be equal for different label order/values")
	require.Equal(t, h1, h2, "hash must match for equal canonical")

	require.NotContains(t, c1, "1")
	require.NotContains(t, c1, "2")
	require.NotContains(t, c2, "X")
	require.NotContains(t, c2, "Y")
	require.Contains(t, c1, "MASKED")
	require.Contains(t, c2, "MASKED")

	// Hash is 64-bit -> 16 hex characters
	require.Len(t, h1, 16)
}

func TestFingerprint_MatrixRangeNormalization(t *testing.T) {
	qA := `rate(foo[5m])`
	qB := `rate(foo[1h])`

	h1, c1 := Fingerprint(qA, false)
	h2, c2 := Fingerprint(qB, false)
	require.NotEqual(t, c1, c2)
	require.NotEqual(t, h1, h2)

	h3, c3 := Fingerprint(qA, true)
	h4, c4 := Fingerprint(qB, true)
	require.Equal(t, c3, c4)
	require.Equal(t, h3, h4)
	require.True(t, len(c3) > 0)
}

func TestFingerprint_AggregateGroupingNormalization(t *testing.T) {
	q1 := `sum by (b,a) (foo)`
	q2 := `sum by (a,b) (foo)`
	h1, c1 := Fingerprint(q1, false)
	h2, c2 := Fingerprint(q2, false)
	require.Equal(t, c1, c2)
	require.Equal(t, h1, h2)
}

func TestFingerprint_VectorMatchingLabelsNormalization(t *testing.T) {
	q1 := `foo + on(b,a) group_left(c) bar`
	q2 := `foo + on(a,b) group_left(c) bar`
	h1, c1 := Fingerprint(q1, false)
	h2, c2 := Fingerprint(q2, false)
	require.Equal(t, c1, c2)
	require.Equal(t, h1, h2)
}

func TestFingerprint_HashMatchesCanonical(t *testing.T) {
	h, canonical := Fingerprint(`sum by (a,b) (rate(foo{env="prod"}[5m]))`, true)
	require.NotEmpty(t, canonical)
	recomputed := xxhash.Sum64String(canonical)
	require.Equal(t, fmt.Sprintf("%x", recomputed), h)
}
