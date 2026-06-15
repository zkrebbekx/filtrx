package filtrx

// FullText is a filter holder for a full-text search column. Unlike Text's LIKE,
// it compiles to the dialect's native full-text match — Postgres
// "@@ websearch_to_tsquery", MySQL "MATCH ... AGAINST", SQLite FTS5 "MATCH" — so
// the database does stemming, ranking and query parsing rather than a substring
// scan.
//
// Query is the user's search string; it binds as a parameter, so it is safe to
// fill straight from a search box. From the wire and from JSON it reads the bare
// (eq) value, so ?title=fast+car or {"eq":"fast car"} both populate it. Config is
// the Postgres text-search configuration (default "english"); it is ignored by
// MySQL and SQLite, and is developer-set, not request input.
//
//	type ArticleFilter struct {
//		Body filtrx.FullText `col:"search_vec"`
//	}
//	ArticleFilter{Body: filtrx.FullText{Query: filtrx.Some("fast car")}}
//	// Postgres → "search_vec" @@ websearch_to_tsquery('english', $1)
type FullText struct {
	Query  Opt[string] `json:"eq"`
	Config string      `json:"-"`
}

// Predicates implements Predicate.
func (f FullText) Predicates(col string) []Cond {
	q, ok := f.Query.Get()
	if !ok {
		return nil
	}
	cfg := f.Config
	if cfg == "" {
		cfg = "english"
	}
	return []Cond{fullText{col: col, config: cfg, query: q}}
}

// fullText is a dialect-rendered full-text match. The search string is bound as a
// parameter; only the column and the developer-set config are emitted into the
// SQL text, never request input.
type fullText struct {
	col    string
	config string
	query  string
}

func (ft fullText) write(b *builder) {
	ph := b.bind(ft.query)
	b.sql.WriteString(b.d.fullTextMatch(b.d.quoteIdent(ft.col), ft.config, ph))
}
