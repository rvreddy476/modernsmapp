-- UH6: add ON DELETE CASCADE to every FK that references auth.users.
-- The GDPR soft-delete path emits UserDeletionRequested and downstream
-- services purge their projections via that event, but the auth-side
-- tables still hold orphan-blocking FKs without CASCADE — so a hard
-- DELETE on auth.users (the eventual grace-period purge) would fail
-- mid-transaction and leave the user in a half-deleted state.
--
-- Idempotent: drops the constraint by name, recreates with CASCADE.
-- Each block tolerates a missing constraint (deployments that already
-- shipped 015 once are no-ops). DO blocks because IF EXISTS isn't
-- universally supported on ALTER TABLE DROP CONSTRAINT across PG
-- versions older than 13 — and we deploy to 14+ but the production
-- migrations get applied to fresh dev DBs that may run later 16+.

-- auth.sessions
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.table_constraints
               WHERE table_schema = 'auth' AND table_name = 'sessions'
                 AND constraint_name = 'sessions_user_id_fkey') THEN
        ALTER TABLE auth.sessions DROP CONSTRAINT sessions_user_id_fkey;
    END IF;
EXCEPTION WHEN OTHERS THEN NULL; END $$;
ALTER TABLE auth.sessions
    ADD CONSTRAINT sessions_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES auth.users(user_id) ON DELETE CASCADE;

-- usr.users
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.table_constraints
               WHERE table_schema = 'usr' AND table_name = 'users'
                 AND constraint_name = 'users_id_fkey') THEN
        ALTER TABLE usr.users DROP CONSTRAINT users_id_fkey;
    END IF;
EXCEPTION WHEN OTHERS THEN NULL; END $$;
ALTER TABLE usr.users
    ADD CONSTRAINT users_id_fkey
    FOREIGN KEY (id) REFERENCES auth.users(user_id) ON DELETE CASCADE;

-- usr.user_settings (refs usr.users, which now cascades from auth.users)
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.table_constraints
               WHERE table_schema = 'usr' AND table_name = 'user_settings'
                 AND constraint_name = 'user_settings_user_id_fkey') THEN
        ALTER TABLE usr.user_settings DROP CONSTRAINT user_settings_user_id_fkey;
    END IF;
EXCEPTION WHEN OTHERS THEN NULL; END $$;
ALTER TABLE usr.user_settings
    ADD CONSTRAINT user_settings_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES usr.users(id) ON DELETE CASCADE;

-- profile.profiles
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.table_constraints
               WHERE table_schema = 'profile' AND table_name = 'profiles'
                 AND constraint_name = 'profiles_user_id_fkey') THEN
        ALTER TABLE profile.profiles DROP CONSTRAINT profiles_user_id_fkey;
    END IF;
EXCEPTION WHEN OTHERS THEN NULL; END $$;
ALTER TABLE profile.profiles
    ADD CONSTRAINT profiles_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES auth.users(user_id) ON DELETE CASCADE;
