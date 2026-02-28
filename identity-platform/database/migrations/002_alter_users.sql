-- Migration 002: Add missing columns to auth.users
-- Depends on: 001_enum_types.sql
-- Run against: identity_db

\connect identity_db;

ALTER TABLE auth.users
    ADD COLUMN IF NOT EXISTS email_verified       BOOLEAN         NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS phone_verified       BOOLEAN         NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS two_factor_enabled   BOOLEAN         NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS two_factor_secret    VARCHAR(255),
    ADD COLUMN IF NOT EXISTS account_type         account_type    NOT NULL DEFAULT 'personal',
    ADD COLUMN IF NOT EXISTS account_status       account_status  NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS login_provider       VARCHAR(50),
    ADD COLUMN IF NOT EXISTS recovery_email       VARCHAR(255),
    ADD COLUMN IF NOT EXISTS recovery_phone       VARCHAR(20),
    ADD COLUMN IF NOT EXISTS age_verification     age_verification NOT NULL DEFAULT 'unverified',
    ADD COLUMN IF NOT EXISTS consent_terms        BOOLEAN         NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS consent_privacy      BOOLEAN         NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS consent_age          BOOLEAN         NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deletion_requested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS scheduled_purge_date  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_login_at         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at            TIMESTAMPTZ    NOT NULL DEFAULT NOW();

-- Index for account deletion cleanup job
CREATE INDEX IF NOT EXISTS idx_users_pending_deletion
    ON auth.users(scheduled_purge_date)
    WHERE account_status = 'pending_deletion';

-- Index for login provider lookups
CREATE INDEX IF NOT EXISTS idx_users_login_provider
    ON auth.users(login_provider)
    WHERE login_provider IS NOT NULL;
