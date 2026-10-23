-- 014: the eligibility floor (copyright quick wins, 2026-10-24).
--
-- 011 dated INELIGIBILITY: a day counts unless the content became
-- ineligible on or before it. It could not say "eligible only from": once
-- the state was eligible again, every day counted, including the days the
-- content was held. A post held for copyright review (a title-review hold
-- from publication, or a copyright case's hold on a live post) must earn
-- nothing for the held period and earn from the clear onward.
--
-- eligibility_eligible_from is that floor: the moment the content last
-- BECAME eligible after being ineligible (ApplyContentEligibility sets it
-- on the transition), or the eligible_from a deferred PostCreated carries
-- (a title-review hold withholds the publication's PostCreated until the
-- clear). NULL means no floor (eligible since creation). The daily rollup
-- skips any day that ended at or before it (internal/aggregation/daily.go).
--
-- Expand-only; existing rows keep NULL, so nothing that accrues today
-- changes.
ALTER TABLE analytics.content_ownership
    ADD COLUMN IF NOT EXISTS eligibility_eligible_from TIMESTAMPTZ;
