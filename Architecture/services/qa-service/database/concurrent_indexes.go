package database

// ConcurrentIndex is one index built with CREATE INDEX CONCURRENTLY outside
// any transaction (Q&A backend audit M6/M7, 30 Oct 2026). A plain CREATE
// INDEX in setup.sql or a migration would hold a SHARE lock on questions for
// the whole build, blocking every question write; CONCURRENTLY cannot run in
// a transaction block at all, and both of those run in one. So
// postgres.BuildConcurrentIndexes builds these one statement at a time on a
// dedicated connection after the schema is ready, in the background, on one
// replica at a time.
//
// The queries work without them (they are only slower), so a fresh database
// still gets everything: the first boot builds them.
type ConcurrentIndex struct {
	// Name is the index name in the service's schema; it is how an INVALID
	// leftover of an interrupted build is found and dropped.
	Name string
	// SQL is the single CREATE INDEX CONCURRENTLY IF NOT EXISTS statement.
	SQL string
}

// SearchDocumentSQL is the text search document of a question (alias q):
// title and body, bounded so a huge body can never exceed the tsvector size
// limit. idx_questions_search_tsv indexes exactly this expression, and
// store.SearchQuestions must use it verbatim for the planner to match it.
const SearchDocumentSQL = `to_tsvector('simple', left(coalesce(%[1]s.title, '') || ' ' || coalesce(%[1]s.body, ''), 100000))`

// TitleDocumentSQL is the similar-questions document (title only);
// idx_questions_title_tsv indexes it.
const TitleDocumentSQL = `to_tsvector('simple', coalesce(%[1]s.title, ''))`

// ConcurrentIndexes lists every online-built index, in build order. Add new
// ones to the end.
var ConcurrentIndexes = []ConcurrentIndex{
	{
		// M6: /search matches title and body through this GIN index
		// instead of an ILIKE '%q%' scan of every question.
		Name: "idx_questions_search_tsv",
		SQL: `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_questions_search_tsv ON questions USING gin ` +
			`((to_tsvector('simple', left(coalesce(title, '') || ' ' || coalesce(body, ''), 100000)))) WHERE deleted_at IS NULL`,
	},
	{
		// M6: /questions/similar matches titles through this one.
		Name: "idx_questions_title_tsv",
		SQL: `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_questions_title_tsv ON questions USING gin ` +
			`((to_tsvector('simple', coalesce(title, '')))) WHERE deleted_at IS NULL`,
	},
	{
		// M7: follow counts and follower fan-out by question; the primary
		// key (user_id, question_id) cannot serve "WHERE question_id = $1".
		Name: "idx_question_follows_question",
		SQL:  `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_question_follows_question ON question_follows (question_id)`,
	},
}
