-- 046 — a fresh database gets the same approval vocabulary an old one has.
--
-- BootstrapSchema runs database/setup.sql and THEN the migrations. setup.sql
-- ends by restating products_approval_status_check with the full set the code
-- writes, including 'pending', 'flagged' and 'changes_requested' (Phase 3.4,
-- the moderator's "request changes"). Migration 001 restates the same
-- constraint with the ORIGINAL, narrower list, which has none of those three.
--
-- On a database that has booted before, 001 ran long ago and every boot's
-- setup.sql has put the wide list back. On a FRESH database (a new
-- environment, CI) the first boot runs setup.sql, then 001 narrows it again,
-- and nothing widens it until the next restart. Until then
-- POST /internal/admin/products/:id/request-changes answers 400 INVALID_VALUE
-- and a product in 'changes_requested' cannot be represented at all.
--
-- This restates setup.sql's list, so on a database that has already booted it
-- installs exactly what is there. It must stay identical to setup.sql's; the
-- contract half that removes 'live' is gated/1001.

ALTER TABLE products DROP CONSTRAINT IF EXISTS products_approval_status_check; -- expand-only: replaced below by setup.sql's list, a strict superset of 001's and equal to what every booted database already has
ALTER TABLE products ADD CONSTRAINT products_approval_status_check -- expand-only: strict superset of 001's list and identical to setup.sql's, so no existing row and no old replica's write can fail it
    CHECK (approval_status IN (
        'draft','submitted','under_review','pending','approved','rejected',
        'flagged','changes_requested','live','hidden','archived'
    ));
