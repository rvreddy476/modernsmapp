-- Exact row count of every user table in the connected database, one line
-- per table: <schema.table> TAB <rows>. Read-only. Used by
-- docs/runbooks/restore-aurora.md to compare a restored cluster with its
-- source:  psql ... -At -F $'\t' -f restore-aurora-counts.sql
SELECT table_schema || '.' || table_name,
       (xpath('/row/c/text()',
              query_to_xml(format('select count(*) as c from %I.%I', table_schema, table_name),
                           false, true, '')))[1]::text::bigint
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
  AND table_schema NOT IN ('pg_catalog', 'information_schema')
ORDER BY 1;
