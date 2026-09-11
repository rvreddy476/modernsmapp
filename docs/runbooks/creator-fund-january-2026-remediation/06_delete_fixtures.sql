-- Step 6 of 02_january_remediation.md: delete the 25 + 25 integration-test fixture rows.
-- NOT EXECUTED by its author. Generated 12 September 2026 from the read-only exports
-- approved_fixture_rate_ids.txt (sha256 d0d70578649f92f215e7acff7b8ca1a899d825f13d4de0e104527774708e8a97)
-- and approved_fixture_band_ids.txt (sha256 7163203448ba0d80d2b6bf30b6fa14e0807b14b9ce3ea966aca81228b0a1727b).
--
-- Run:  docker exec -i atpost_stack-postgres-1 psql -U postgres -d app -v ON_ERROR_STOP=1 -v operator="'<ADMIN_ID>'" -f - < 06_delete_fixtures.sql
-- Any RAISE aborts the transaction before COMMIT; nothing is deleted unless every check passes.
--
-- Replace <ADMIN_ID> via -v operator=... (the founder's user_id from identity_db, runbook step 0.4).

SET TIME ZONE 'UTC';
BEGIN;

-- 1. Snapshot the exact approved rows, full rows, into a temp table and the audit log.
CREATE TEMP TABLE january_fixture_snapshot ON COMMIT DROP AS
  SELECT 'monetization_rpm_rates'::text AS table_name, r.id, to_jsonb(r) AS row_data
  FROM monetization_rpm_rates r
  WHERE r.id = ANY(ARRAY['06713908-d1f1-4546-8bb0-86ce06fe6e8a','08e0f639-5bd0-4fb6-b847-a98b73935580','16afe41a-258a-4ef8-8918-ab2427f4e8dc','24903d2c-f625-429c-896c-fe1507ddc47a','255e661a-863f-4fbf-80db-14bee2f5f180','2734cb41-1b36-4e6b-9d5f-92c95be55ae8','391e07d4-b8bb-4813-9c34-4a7765db5ff0','39df5353-2d36-47da-aa55-f0f39a9bf2e2','4b2f426c-dfba-48d6-ab3b-c44533500247','4f026b3b-2689-4e3d-8502-73d03e34bd01','58d98c7a-0087-4076-bf8e-e6c6817fd2a2','64b6f43e-9af0-4c4c-9e51-acbd6959432c','66c6c7bc-6e45-4074-af5f-8c580f52705e','6acc6e72-3e25-464b-bef0-6891711d5dc5','6b93a6d7-d1b5-44c4-bb0a-114a8fa4d6ed','71981bf6-4283-4c6e-bf8a-04e59374dc0e','93417e65-3c1e-41d0-b5a9-6ac31fa64bd2','a695de00-6503-4e6e-92ba-389c90cccd2f','bf3e2a34-1e46-4967-824a-7dd36b4c6db2','d830ba17-f3c9-4361-9a5a-68477230e5e1','d8b4237a-a506-4816-be8f-7b832cb9e0a4','e3648495-43f4-4ba7-898e-687e4686cde3','e3833da5-68eb-4730-9697-edfcc9045098','e4d4c88e-a5f6-4ce9-aefc-793a37909c75','ec0a00ed-626e-47a5-a5bb-b81c1dab2e29']::uuid[])
  UNION ALL
  SELECT 'monetization_quality_bands', b.id, to_jsonb(b)
  FROM monetization_quality_bands b
  WHERE b.id = ANY(ARRAY['193a4ec8-4606-4daf-bff1-e4e176556110','1d7fb677-d18d-4af9-8266-3ba8faf594e3','377eb409-c1f4-42bc-b6ec-360fb30858cb','5676b3e1-b0da-4fdc-a467-f3512187596d','5813d808-630a-4001-a6cc-c0a59623e28d','6b16f654-5cfb-4efd-b5dc-741b6afc45ce','6c3e4d0f-685b-44fd-96a4-ba4703e84adf','70b65401-2032-4be5-9823-dbaec437c1b3','800fbf78-d87e-418c-ab73-88c8fca207bf','834281e5-7406-469c-83b5-40aeebc26f31','8c19763b-1dda-4817-8cea-56ea669f579c','910e4481-292d-4e89-ba27-235077c33a41','9549506c-6691-41c0-8d0a-2132376d9eff','9ee1f994-e33e-4e34-9c08-62ac3cce93c2','a633bbb2-4951-4513-a2f3-45dfb1d7e952','b31de189-e608-4961-b7a7-d9e1da444fc9','b45d00ea-c53d-4e2b-9d9a-5143d15008e0','b45db851-8ae0-484d-b7e1-8eb3acd54f3c','bb16cd23-c74e-4c44-ab26-0ff4b288960e','c652731c-97f9-48d0-922b-d6235e3c2a92','ceb7619b-4f3b-4729-b9de-da1725fc8881','d2aedf07-0635-4fd6-b102-0e0a3910930c','d6716c84-9cb4-4f77-bcbe-b7dae7e46fec','e013080e-16b2-4eec-bf46-b79e5e6c4b7e','f97e6fe3-ade0-4263-816e-9c7a1f57f5c2']::uuid[]);

INSERT INTO monetization_audit_log (table_name, operation, old_data, new_data, performer_id)
SELECT table_name, 'january_remediation_fixture_delete',
       row_data || jsonb_build_object('operator_user_id', :operator::text),
       NULL, :operator::uuid
FROM january_fixture_snapshot;

-- 2. Proof inside the transaction, then delete ONLY the approved ids, then count.
DO $$
DECLARE
  rate_ids  uuid[] := ARRAY['06713908-d1f1-4546-8bb0-86ce06fe6e8a','08e0f639-5bd0-4fb6-b847-a98b73935580','16afe41a-258a-4ef8-8918-ab2427f4e8dc','24903d2c-f625-429c-896c-fe1507ddc47a','255e661a-863f-4fbf-80db-14bee2f5f180','2734cb41-1b36-4e6b-9d5f-92c95be55ae8','391e07d4-b8bb-4813-9c34-4a7765db5ff0','39df5353-2d36-47da-aa55-f0f39a9bf2e2','4b2f426c-dfba-48d6-ab3b-c44533500247','4f026b3b-2689-4e3d-8502-73d03e34bd01','58d98c7a-0087-4076-bf8e-e6c6817fd2a2','64b6f43e-9af0-4c4c-9e51-acbd6959432c','66c6c7bc-6e45-4074-af5f-8c580f52705e','6acc6e72-3e25-464b-bef0-6891711d5dc5','6b93a6d7-d1b5-44c4-bb0a-114a8fa4d6ed','71981bf6-4283-4c6e-bf8a-04e59374dc0e','93417e65-3c1e-41d0-b5a9-6ac31fa64bd2','a695de00-6503-4e6e-92ba-389c90cccd2f','bf3e2a34-1e46-4967-824a-7dd36b4c6db2','d830ba17-f3c9-4361-9a5a-68477230e5e1','d8b4237a-a506-4816-be8f-7b832cb9e0a4','e3648495-43f4-4ba7-898e-687e4686cde3','e3833da5-68eb-4730-9697-edfcc9045098','e4d4c88e-a5f6-4ce9-aefc-793a37909c75','ec0a00ed-626e-47a5-a5bb-b81c1dab2e29']::uuid[];
  band_ids  uuid[] := ARRAY['193a4ec8-4606-4daf-bff1-e4e176556110','1d7fb677-d18d-4af9-8266-3ba8faf594e3','377eb409-c1f4-42bc-b6ec-360fb30858cb','5676b3e1-b0da-4fdc-a467-f3512187596d','5813d808-630a-4001-a6cc-c0a59623e28d','6b16f654-5cfb-4efd-b5dc-741b6afc45ce','6c3e4d0f-685b-44fd-96a4-ba4703e84adf','70b65401-2032-4be5-9823-dbaec437c1b3','800fbf78-d87e-418c-ab73-88c8fca207bf','834281e5-7406-469c-83b5-40aeebc26f31','8c19763b-1dda-4817-8cea-56ea669f579c','910e4481-292d-4e89-ba27-235077c33a41','9549506c-6691-41c0-8d0a-2132376d9eff','9ee1f994-e33e-4e34-9c08-62ac3cce93c2','a633bbb2-4951-4513-a2f3-45dfb1d7e952','b31de189-e608-4961-b7a7-d9e1da444fc9','b45d00ea-c53d-4e2b-9d9a-5143d15008e0','b45db851-8ae0-484d-b7e1-8eb3acd54f3c','bb16cd23-c74e-4c44-ab26-0ff4b288960e','c652731c-97f9-48d0-922b-d6235e3c2a92','ceb7619b-4f3b-4729-b9de-da1725fc8881','d2aedf07-0635-4fd6-b102-0e0a3910930c','d6716c84-9cb4-4f77-bcbe-b7dae7e46fec','e013080e-16b2-4eec-bf46-b79e5e6c4b7e','f97e6fe3-ade0-4263-816e-9c7a1f57f5c2']::uuid[];
  n         integer;
  proof     integer;
  snap_r    integer;
  snap_b    integer;
BEGIN
  IF array_length(rate_ids, 1) <> 25 OR array_length(band_ids, 1) <> 25 THEN
    RAISE EXCEPTION 'approved id lists carry % rates and % bands, expected 25 and 25', array_length(rate_ids, 1), array_length(band_ids, 1);
  END IF;
  SELECT count(*) INTO snap_r FROM january_fixture_snapshot WHERE table_name = 'monetization_rpm_rates';
  SELECT count(*) INTO snap_b FROM january_fixture_snapshot WHERE table_name = 'monetization_quality_bands';
  IF snap_r <> 25 OR snap_b <> 25 THEN
    RAISE EXCEPTION 'snapshot holds % rates and % bands, expected 25 and 25 (an approved id no longer exists); rolled back', snap_r, snap_b;
  END IF;
  -- Every approved id must still be a fixture row; the delete never touches an untagged row.
  IF (SELECT count(*) FROM monetization_rpm_rates WHERE id = ANY(rate_ids) AND notes = 'integration test window') <> 25 THEN
    RAISE EXCEPTION 'an approved rate id is not tagged integration test window; rolled back';
  END IF;
  IF (SELECT count(*) FROM monetization_quality_bands WHERE id = ANY(band_ids) AND notes = 'integration test window') <> 25 THEN
    RAISE EXCEPTION 'an approved band id is not tagged integration test window; rolled back';
  END IF;
  -- And nothing tagged is outside the approved lists.
  IF (SELECT count(*) FROM monetization_rpm_rates WHERE notes = 'integration test window' AND NOT (id = ANY(rate_ids))) <> 0
     OR (SELECT count(*) FROM monetization_quality_bands WHERE notes = 'integration test window' AND NOT (id = ANY(band_ids))) <> 0 THEN
    RAISE EXCEPTION 'a fixture-tagged row exists outside the approved lists; rolled back';
  END IF;

  -- The step-5 proof query, re-run here: any non-reversed earning that resolves
  -- to a fixture rate or band means the delete would orphan money.
  SELECT count(*) INTO proof
  FROM creator_fund_earnings e
  LEFT JOIN LATERAL (
    SELECT id, rpm_paise, notes FROM monetization_rpm_rates
    WHERE content_type = e.content_type AND region_code = e.region_code
      AND effective_from <= e.day_bucket AND (effective_to IS NULL OR effective_to > e.day_bucket)
    ORDER BY effective_from DESC, created_at DESC, id DESC LIMIT 1) r ON true
  LEFT JOIN LATERAL (
    SELECT id, notes FROM monetization_quality_bands
    WHERE content_type = e.content_type AND region_code = e.region_code
      AND effective_from <= e.day_bucket AND (effective_to IS NULL OR effective_to > e.day_bucket)
    ORDER BY effective_from DESC, created_at DESC, id DESC LIMIT 1) b ON true
  WHERE e.status <> 'reversed'
    AND (r.notes = 'integration test window' OR b.notes = 'integration test window');
  IF proof <> 0 THEN
    RAISE EXCEPTION 'proof query returned % rows (must be 0): a non-reversed earning still resolves to a fixture; nothing deleted', proof;
  END IF;

  DELETE FROM monetization_rpm_rates WHERE id = ANY(rate_ids);
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n <> 25 THEN
    RAISE EXCEPTION 'rates delete touched % rows, expected exactly 25; rolled back', n;
  END IF;
  DELETE FROM monetization_quality_bands WHERE id = ANY(band_ids);
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n <> 25 THEN
    RAISE EXCEPTION 'bands delete touched % rows, expected exactly 25; rolled back', n;
  END IF;
  RAISE NOTICE 'deleted 25 rates and 25 bands; committing';
END $$;

-- 3. What remains, still inside the transaction.
SELECT count(*) AS rates_tagged_after FROM monetization_rpm_rates     WHERE notes = 'integration test window';   -- 0
SELECT count(*) AS bands_tagged_after FROM monetization_quality_bands WHERE notes = 'integration test window';   -- 0
SELECT content_type, count(*) FROM monetization_rpm_rates GROUP BY 1 ORDER BY 1;                                 -- flick 1, long_video 1
SELECT content_type, enabled, count(*) FROM monetization_quality_bands GROUP BY 1,2 ORDER BY 1,2;               -- flick f 1 / t 1, long_video f 1 / t 1
SELECT count(*) AS audit_rows FROM monetization_audit_log WHERE operation = 'january_remediation_fixture_delete'; -- 50

COMMIT;
