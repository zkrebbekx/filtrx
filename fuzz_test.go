package filtrx

import "testing"

// FuzzDecodeCursor hardens the one parser that consumes untrusted input: a cursor
// arrives from a client. It must never panic — only return values or an error —
// regardless of the bytes handed in.
func FuzzDecodeCursor(f *testing.F) {
	good, _ := encodeCursor([]any{int64(1), "x", true, 2.5, nil})
	for _, seed := range []string{string(good), "", "!!!", "AAAA", "e30", "bm90anNvbg"} {
		f.Add(seed)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		_, _ = decodeCursor(Cursor(s)) // only contract: it returns, never panics
	})
}

func BenchmarkKeysetCond(b *testing.B) {
	order := []orderTerm{{col: "created_at"}, {col: "id"}}
	vals := []any{"2026-01-01", 5}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = keysetCond(order, vals, false)
	}
}

func BenchmarkCursorRoundTrip(b *testing.B) {
	vals := []any{int64(1234567890), "alice", true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c, _ := encodeCursor(vals)
		_, _ = decodeCursor(c)
	}
}

func BenchmarkExistsCompile(b *testing.B) {
	f := userWithOrders{
		Orders: Exists[orderSub]{
			When: Some(true),
			Sub:  orderSub{Status: Text{Eq: Some("paid")}},
		},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = compileFilter(f)
	}
}
