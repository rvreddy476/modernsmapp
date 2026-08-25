package com.us.android.core.database

import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteDatabase

/**
 * 2 → 3: Creator Studio P0-A.
 *
 * ## THIS MIGRATION NEVER THROWS ON USER DATA
 *
 * An earlier design aborted when it found a legacy draft carrying exactly one of
 * `creationKey`/`frozenRequestJson`. Aborting is the wrong shape: Room opens the
 * database by running migrations, so a throw here means the app cannot open at
 * all — and the "recovery screen" that was supposed to appear would have no
 * database to read. The ambiguous row is therefore CLASSIFIED and quarantined,
 * which is fail-closed in the way that matters (no project, no publish
 * operation, no new idempotency key) while leaving a running app that can tell
 * the user what happened.
 *
 * ## CLASSIFICATION HAPPENS HERE, IN SQL
 *
 * Not in a preflight that reads the row with a separate handle, writes a
 * preference, and lets a later transaction trust it. That is a
 * time-of-check/time-of-use gap on the single most dangerous row in the
 * database. The CASE below reads and classifies in the same statement that
 * copies the row.
 *
 * ## WHAT IS DELIBERATELY NOT DONE HERE
 *
 * No filesystem work. A SQL migration cannot atomically copy ContentResolver
 * bytes into app-private storage, and the legacy `content://` grant may already
 * be gone — this codebase never took a persistable one. The source copy is stage
 * two, after the database opens; activation is stage three.
 *
 * `composer_draft` is NOT dropped and NOT emptied. It is the rollback surface
 * for a feature-flag-off inside a schema-v3 build. A schema-v2 APK cannot open
 * this database at all — `user_version` is 3 and there is no 3→2 migration —
 * and that limitation is stated rather than discovered.
 */
private const val SCHEMA_V2 = 2
private const val SCHEMA_V3 = 3

val MIGRATION_2_3 = object : Migration(SCHEMA_V2, SCHEMA_V3) {

    override fun migrate(db: SupportSQLiteDatabase) {
        createTables(db)
        createAdoptionTables(db)

        val now = System.currentTimeMillis()

        db.execSQL(
            "INSERT OR REPLACE INTO composer_draft_fallback_state " +
                "(id, state, reason, updatedAtMillis) VALUES ('singleton', 'AVAILABLE', NULL, ?)",
            arrayOf<Any>(now),
        )

        // Classify and stage the legacy row, in this transaction.
        //
        // `frozenRequestJson` is copied column to column: SQLite moves the bytes
        // and nothing decodes, re-encodes or normalizes them. They are the
        // server's idempotency authority, and one changed byte is a 409 at best
        // and a duplicate post at worst.
        //
        // The both-or-neither test is `(a IS NULL) = (b IS NULL)`. A key without
        // its bytes cannot be safely retried, because the retry would have to
        // rebuild the request and might rebuild it differently.
        db.execSQL(
            """
            INSERT INTO creator_migration_staging (
                stagingId, text, imageUri, altText, decorative, language,
                mediaId, creationKey, frozenRequestJson,
                classification, adoptionState, attempts, updatedAtMillis)
            SELECT
                d.id, d.text, d.imageUri, d.altText, d.decorative, d.language,
                d.mediaId, d.creationKey, d.frozenRequestJson,
                CASE WHEN (d.creationKey IS NULL) = (d.frozenRequestJson IS NULL)
                     THEN 'CLEAN' ELSE 'HALF_FROZEN_OPERATION' END,
                CASE WHEN (d.creationKey IS NULL) = (d.frozenRequestJson IS NULL)
                     THEN 'PENDING' ELSE 'QUARANTINED' END,
                0, ?
            FROM composer_draft d
            """.trimIndent(),
            arrayOf<Any>(now),
        )

        // PRE-COMMIT ASSERTION: every legacy row was staged.
        //
        // This is the one place in this migration that is allowed to throw, and
        // the reason is the opposite of the ambiguous-row case. An ambiguous row
        // is data we understood and chose to quarantine; a count mismatch means
        // the copy itself did not do what it claims, and continuing would leave
        // rows the adoption pass will never see while the app behaves as though
        // migration succeeded.
        //
        // Throwing here rolls the Room transaction back, so the database stays
        // at schema 2 with `composer_draft` untouched. The app fails to open
        // loudly instead of quietly losing a draft — recoverable, because the
        // user's row is still exactly where it was.
        val legacyCount = db.query("SELECT count(*) FROM composer_draft").use { cursor ->
            if (cursor.moveToFirst()) cursor.getInt(0) else 0
        }
        val stagedCount = db.query("SELECT count(*) FROM creator_migration_staging").use { cursor ->
            if (cursor.moveToFirst()) cursor.getInt(0) else 0
        }
        check(stagedCount == legacyCount) {
            "MIGRATION_2_3 staged $stagedCount of $legacyCount legacy drafts; " +
                "rolling back rather than opening a database that has silently dropped one"
        }

        // A quarantined row makes the legacy fallback unusable: the draft the
        // legacy composer would show is precisely the one we could not safely
        // interpret, so offering it would be offering a guess.
        db.execSQL(
            """
            UPDATE composer_draft_fallback_state
            SET state = 'UNAVAILABLE', reason = 'LEGACY_QUARANTINED', updatedAtMillis = ?
            WHERE EXISTS (
                SELECT 1 FROM creator_migration_staging WHERE adoptionState = 'QUARANTINED')
            """.trimIndent(),
            arrayOf<Any>(now),
        )
    }

    private fun createTables(db: SupportSQLiteDatabase) {
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `creator_project` (
                `projectId` TEXT NOT NULL,
                `schemaVersion` INTEGER NOT NULL,
                `profile` TEXT NOT NULL,
                `revision` INTEGER NOT NULL,
                `status` TEXT NOT NULL,
                `document` TEXT NOT NULL,
                `documentSha256` TEXT NOT NULL,
                `createdAtMillis` INTEGER NOT NULL,
                `updatedAtMillis` INTEGER NOT NULL,
                PRIMARY KEY(`projectId`)
            )
            """.trimIndent(),
        )
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `creator_publish_operation` (
                `operationId` TEXT NOT NULL,
                `projectId` TEXT NOT NULL,
                `boundRevision` INTEGER NOT NULL,
                `projectDocumentSha256` TEXT NOT NULL,
                `orderedOutputSha256` TEXT NOT NULL,
                `orderedMediaIds` TEXT NOT NULL,
                `creationKey` TEXT NOT NULL,
                `frozenRequestBase64` TEXT NOT NULL,
                `frozenRequestSha256` TEXT NOT NULL,
                `frozenRequestBytes` INTEGER NOT NULL,
                `state` TEXT NOT NULL,
                `serverPostId` TEXT,
                `lastErrorCode` TEXT,
                `supersededByOperationId` TEXT,
                `createdAtMillis` INTEGER NOT NULL,
                `updatedAtMillis` INTEGER NOT NULL,
                PRIMARY KEY(`operationId`)
            )
            """.trimIndent(),
        )
        // At most ONE live publication per project.
        //
        // This used to be a partial unique index on creator_publish_operation.
        // It could not stay: Room cannot GENERATE a partial index, so the fresh
        // schema had no such index, the migrated schema did, and Room's
        // TableInfo comparison rejects that mismatch on open — every upgrading
        // user would have been unable to start the app. Worse, fresh installs
        // had no protection at all.
        //
        // A table keyed on projectId says the same thing in something Room
        // generates identically for both cohorts, and says it more strongly: the
        // partial index only constrained states inside its predicate, so an
        // unrecognised state slipped past. A slot is held or it is not.
        //
        // This DDL is byte-compatible with what Room generates from
        // CreatorLiveOperationEntity; the schema test asserts that.
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `creator_live_operation` (
                `projectId` TEXT NOT NULL,
                `operationId` TEXT NOT NULL,
                PRIMARY KEY(`projectId`)
            )
            """.trimIndent(),
        )
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `creator_source_asset` (
                `projectId` TEXT NOT NULL,
                `assetId` TEXT NOT NULL,
                `vaultPath` TEXT NOT NULL,
                `sha256` TEXT NOT NULL,
                `bytes` INTEGER NOT NULL,
                `mime` TEXT NOT NULL,
                `widthPx` INTEGER NOT NULL,
                `heightPx` INTEGER NOT NULL,
                PRIMARY KEY(`projectId`, `assetId`)
            )
            """.trimIndent(),
        )
    }

    /**
     * The adoption machinery: staging, the fallback-state singleton, and the
     * typed recovery table.
     *
     * Separated from the project tables above because these three exist only to
     * carry a schema-v2 draft safely across the version boundary; once every
     * install has adopted, nothing writes to them again.
     */
    private fun createAdoptionTables(db: SupportSQLiteDatabase) {
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `creator_migration_staging` (
                `stagingId` TEXT NOT NULL,
                `text` TEXT NOT NULL,
                `imageUri` TEXT,
                `altText` TEXT NOT NULL,
                `decorative` INTEGER NOT NULL,
                `language` TEXT NOT NULL,
                `mediaId` TEXT,
                `creationKey` TEXT,
                `frozenRequestJson` TEXT,
                `classification` TEXT NOT NULL,
                `adoptionState` TEXT NOT NULL,
                `attempts` INTEGER NOT NULL,
                `updatedAtMillis` INTEGER NOT NULL,
                PRIMARY KEY(`stagingId`)
            )
            """.trimIndent(),
        )
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `composer_draft_fallback_state` (
                `id` TEXT NOT NULL,
                `state` TEXT NOT NULL,
                `reason` TEXT,
                `updatedAtMillis` INTEGER NOT NULL,
                PRIMARY KEY(`id`)
            )
            """.trimIndent(),
        )
        // Created HERE, not in a later release: the classification above can
        // already decide a row needs recovery, and a table that does not exist
        // yet cannot be the destination of that decision.
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `creator_legacy_recovery` (
                `recoveryId` TEXT NOT NULL,
                `kind` TEXT NOT NULL,
                `text` TEXT NOT NULL,
                `language` TEXT NOT NULL,
                `mediaId` TEXT,
                `creationKey` TEXT,
                `frozenRequestJson` TEXT,
                `frozenRequestSha` TEXT,
                `frozenRequestLen` INTEGER,
                `recoveredSourcePath` TEXT,
                `createdAtMillis` INTEGER NOT NULL,
                PRIMARY KEY(`recoveryId`)
            )
            """.trimIndent(),
        )
    }
}
