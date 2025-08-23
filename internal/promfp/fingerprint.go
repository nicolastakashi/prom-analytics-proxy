package promfp

import (
	"fmt"
	"sort"

	"github.com/cespare/xxhash/v2"
	"github.com/prometheus/prometheus/promql/parser"
)

// Fingerprint returns (hash, canonical).
// If ignoreRanges is true, matrix selector ranges/offsets are zeroed for identity.
func Fingerprint(query string, ignoreRanges bool) (string, string) {
	expr, err := parser.ParseExpr(query)
	if err != nil {
		return "", ""
	}

	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			// Sort matchers by label name for stability.
			sort.Slice(n.LabelMatchers, func(i, j int) bool {
				return n.LabelMatchers[i].Name < n.LabelMatchers[j].Name
			})
			// Mask all values except metric name.
			for _, m := range n.LabelMatchers {
				if m.Name != "__name__" {
					m.Value = "MASKED"
				}
			}
		case *parser.MatrixSelector:
			if ignoreRanges {
				n.Range = 0
			}
			// Sort underlying VectorSelector matchers if present.
			if vs, ok := n.VectorSelector.(*parser.VectorSelector); ok {
				sort.Slice(vs.LabelMatchers, func(i, j int) bool {
					return vs.LabelMatchers[i].Name < vs.LabelMatchers[j].Name
				})
				for _, m := range vs.LabelMatchers {
					if m.Name != "__name__" {
						m.Value = "MASKED"
					}
				}
			}
		case *parser.AggregateExpr:
			// Sort grouping labels (by(...) / without(...)).
			sort.Strings(n.Grouping)
		case *parser.BinaryExpr:
			// Normalize ON/IGNORING label lists.
			if n.VectorMatching != nil {
				sort.Strings(n.VectorMatching.MatchingLabels)
				sort.Strings(n.VectorMatching.Include)
			}
			// Note: we do not normalize commutative ops here due to version differences.
		}
		return nil
	})

	canonical := expr.String() // stable-ish printable form
	sum := xxhash.Sum64String(canonical)
	return fmt.Sprintf("%x", sum), canonical
}
