package com.us.android.core.database

import android.content.Context
import androidx.sqlite.db.SupportSQLiteDatabase
import androidx.sqlite.db.SupportSQLiteOpenHelper
import androidx.sqlite.db.framework.FrameworkSQLiteOpenHelperFactory
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * CS-LB-1M — the schema 2 → 3 migration, against a REAL schema-v2 database.
 *
 * ## WHY THIS IS NOT A FAKE
 *
 * The claim being tested is "a user's unpublished work survives, and a publish
 * that already committed on the server is never sent twice". A mock DAO can
 * prove a function was called. It cannot prove that a real SQLite file, opened
 * at `user_version = 2` with a real legacy row in it, comes out the other side
 * with the frozen request bytes unchanged.
 *
 * So this test builds an actual v2 database file — the exact DDL from
 * `MIGRATION_1_2` — writes real rows into it, and runs the real
 * [MIGRATION_2_3] against it.
 *
 * ## THE TWO ROWS THAT MATTER
 *
 * A **clean** row has `creationKey` and `frozenRequestJson` both present or both
 * absent. An **ambiguous** row has exactly one. The ambiguous case is the
 * dangerous one: a key with no bytes cannot be retried safely, because the retry
 * would have to rebuild the request and might rebuild it differently — which the
 * server correctly refuses, or worse, accepts as a new post.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class Migration23Test {

    private lateinit var helper: SupportSQLiteOpenHelper
    private lateinit var context: Context

    /** The exact bytes a lost-response publish left behind. Must survive verbatim. */
    private val frozenRequest =
        """{"text":"Notes from a slow morning","visibility":"public","content_type":"post",""" +
            """"post_type":"text","app_origin":"postbook","media_ids":[],"language":"en",""" +
            """"distribution":{"version":1,"main_feed":true,"notify_subscribers":false,""" +
            """"create_reel_preview":false}}"""

    private val creationKey = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f"

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        context.getDatabasePath(TEST_DB).delete()
    }

    @After
    fun tearDown() {
        if (::helper.isInitialized) helper.close()
        context.getDatabasePath(TEST_DB).delete()
    }

    /**
     * Builds a genuine schema-v2 database: version 2, `composer_draft` present,
     * none of the Creator Studio tables.
     */
    private fun openV2(): SupportSQLiteDatabase {
        helper = FrameworkSQLiteOpenHelperFactory().create(
            SupportSQLiteOpenHelper.Configuration.builder(context)
                .name(TEST_DB)
                .callback(object : SupportSQLiteOpenHelper.Callback(SCHEMA_V2) {
                    override fun onCreate(db: SupportSQLiteDatabase) {
                        MIGRATION_1_2.migrate(db)
                        // The REAL v2 remote_keys DDL, copied from the checked-in
                        // schema 2.json. A stub with invented columns would make
                        // Room reject the migrated database for a reason that has
                        // nothing to do with this migration.
                        db.execSQL(
                            "CREATE TABLE IF NOT EXISTS `remote_keys` " +
                                "(`id` TEXT NOT NULL, `feedId` TEXT NOT NULL, " +
                                "`nextCursor` TEXT, `lastUpdatedMillis` INTEGER NOT NULL, " +
                                "PRIMARY KEY(`id`))",
                        )
                        db.execSQL(
                            "CREATE INDEX IF NOT EXISTS `index_remote_keys_feedId` " +
                                "ON `remote_keys` (`feedId`)",
                        )
                    }

                    override fun onUpgrade(db: SupportSQLiteDatabase, old: Int, new: Int) = Unit
                })
                .build(),
        )
        return helper.writableDatabase
    }

    private fun insertDraft(
        db: SupportSQLiteDatabase,
        text: String,
        imageUri: String? = null,
        mediaId: String? = null,
        key: String? = null,
        frozen: String? = null,
    ) {
        db.execSQL(
            """
            INSERT INTO composer_draft
                (id, text, imageUri, altText, decorative, language, mediaId,
                 creationKey, frozenRequestJson, updatedAtMillis)
            VALUES ('composer', ?, ?, '', 0, 'en', ?, ?, ?, 1755939600000)
            """.trimIndent(),
            arrayOf(text, imageUri, mediaId, key, frozen),
        )
    }

    private fun SupportSQLiteDatabase.one(sql: String): List<String?> =
        query(sql).use { cursor ->
            if (!cursor.moveToFirst()) return emptyList()
            (0 until cursor.columnCount).map { cursor.getString(it) }
        }

    private fun SupportSQLiteDatabase.count(sql: String): Int =
        query(sql).use { cursor -> if (cursor.moveToFirst()) cursor.getInt(0) else 0 }

    // ------------------------------------------------------------------
    // The clean path
    // ------------------------------------------------------------------

    /**
     * The bytes are the whole point.
     *
     * `frozenRequestJson` is the server's idempotency authority. If the
     * migration re-encoded it, re-serialized it, or normalized so much as one
     * character, the retry would present different bytes under the same key —
     * and the platform would either refuse it as key reuse or publish a second
     * post.
     */
    @Test
    fun `a clean frozen operation survives the migration byte-for-byte`() {
        val db = openV2()
        insertDraft(db, text = "Notes from a slow morning", key = creationKey, frozen = frozenRequest)

        MIGRATION_2_3.migrate(db)

        val row = db.one(
            "SELECT classification, adoptionState, creationKey, frozenRequestJson " +
                "FROM creator_migration_staging",
        )
        assertThat(row[0]).isEqualTo(CreatorMigrationStagingEntity.CLASSIFICATION_CLEAN)
        assertThat(row[1]).isEqualTo(CreatorMigrationStagingEntity.STATE_PENDING)
        assertThat(row[2]).isEqualTo(creationKey)
        assertThat(row[3]).isEqualTo(frozenRequest)
    }

    /** A draft with no frozen operation at all is also clean — both-or-NEITHER. */
    @Test
    fun `a draft with no frozen operation is clean and pending`() {
        val db = openV2()
        insertDraft(db, text = "still writing this", imageUri = "content://media/42")

        MIGRATION_2_3.migrate(db)

        val row = db.one("SELECT classification, adoptionState FROM creator_migration_staging")
        assertThat(row[0]).isEqualTo(CreatorMigrationStagingEntity.CLASSIFICATION_CLEAN)
        assertThat(row[1]).isEqualTo(CreatorMigrationStagingEntity.STATE_PENDING)
    }

    // ------------------------------------------------------------------
    // The dangerous path
    // ------------------------------------------------------------------

    /**
     * A key without its bytes is quarantined — and the app still opens.
     *
     * This is the case an earlier design aborted on. Aborting inside a Room
     * migration means Room cannot open the database, so the app dies at launch
     * and the recovery UI that was meant to handle this never runs.
     */
    @Test
    fun `an ambiguous half-frozen row is quarantined without throwing`() {
        val db = openV2()
        insertDraft(db, text = "half a publish", key = creationKey, frozen = null)

        MIGRATION_2_3.migrate(db)

        val row = db.one("SELECT classification, adoptionState FROM creator_migration_staging")
        assertThat(row[0]).isEqualTo(CreatorMigrationStagingEntity.CLASSIFICATION_HALF_FROZEN)
        assertThat(row[1]).isEqualTo(CreatorMigrationStagingEntity.STATE_QUARANTINED)
    }

    /** The mirror image: bytes with no key is equally unretryable. */
    @Test
    fun `frozen bytes without a creation key are also quarantined`() {
        val db = openV2()
        insertDraft(db, text = "half a publish", key = null, frozen = frozenRequest)

        MIGRATION_2_3.migrate(db)

        val row = db.one("SELECT classification FROM creator_migration_staging")
        assertThat(row[0]).isEqualTo(CreatorMigrationStagingEntity.CLASSIFICATION_HALF_FROZEN)
    }

    /**
     * NOTHING is minted for an ambiguous row.
     *
     * No project, no publish operation, and above all no replacement
     * `creationKey`. Minting one is precisely how a post that already committed
     * server-side gets published a second time.
     */
    @Test
    fun `a quarantined row creates no project and no publish operation`() {
        val db = openV2()
        insertDraft(db, text = "half a publish", key = creationKey, frozen = null)

        MIGRATION_2_3.migrate(db)

        assertThat(db.count("SELECT count(*) FROM creator_project")).isEqualTo(0)
        assertThat(db.count("SELECT count(*) FROM creator_publish_operation")).isEqualTo(0)
    }

    /** A quarantined row also disables the legacy fallback, with a stated reason. */
    @Test
    fun `quarantine marks the legacy fallback unavailable`() {
        val db = openV2()
        insertDraft(db, text = "half a publish", key = creationKey, frozen = null)

        MIGRATION_2_3.migrate(db)

        val row = db.one("SELECT state, reason FROM composer_draft_fallback_state")
        assertThat(row[0]).isEqualTo(ComposerDraftFallbackStateEntity.UNAVAILABLE)
        assertThat(row[1]).isEqualTo(ComposerDraftFallbackStateEntity.REASON_QUARANTINED)
    }

    @Test
    fun `a clean migration leaves the legacy fallback available`() {
        val db = openV2()
        insertDraft(db, text = "ordinary draft")

        MIGRATION_2_3.migrate(db)

        val row = db.one("SELECT state, reason FROM composer_draft_fallback_state")
        assertThat(row[0]).isEqualTo(ComposerDraftFallbackStateEntity.AVAILABLE)
        assertThat(row[1]).isNull()
    }

    // ------------------------------------------------------------------
    // Rollback surface
    // ------------------------------------------------------------------

    /**
     * The legacy row is RETAINED.
     *
     * An earlier design deleted it on successful adoption, which quietly removed
     * the only rollback path: a feature-flag-off in the same build reads this
     * row, and there is nothing to read if the migration threw it away.
     */
    @Test
    fun `the legacy composer draft is retained so flag-off has something to read`() {
        val db = openV2()
        insertDraft(db, text = "ordinary draft", imageUri = "content://media/42")

        MIGRATION_2_3.migrate(db)

        val row = db.one("SELECT text, imageUri FROM composer_draft")
        assertThat(row[0]).isEqualTo("ordinary draft")
        assertThat(row[1]).isEqualTo("content://media/42")
    }

    /** An empty v2 database migrates cleanly and stages nothing. */
    @Test
    fun `a database with no draft migrates without staging anything`() {
        val db = openV2()

        MIGRATION_2_3.migrate(db)

        assertThat(db.count("SELECT count(*) FROM creator_migration_staging")).isEqualTo(0)
        assertThat(db.count("SELECT count(*) FROM composer_draft_fallback_state")).isEqualTo(1)
    }

    // ------------------------------------------------------------------
    // The one-live-operation invariant
    // ------------------------------------------------------------------

    /**
     * The partial unique index is a real database constraint, not a convention.
     *
     * Two live operations for one project is the shape of a duplicate post, so
     * SQLite refuses it. `published` and `superseded` are terminal and excluded,
     * which is what lets a resolved project legitimately start a new publish.
     */
    // The one-live-operation invariant moved from a partial index to the
    // creator_live_operation table, because Room cannot generate a partial index
    // and the mismatch made the migrated schema unopenable. It is now proven
    // through production Room on BOTH cohorts in RoomOpenAfterMigrationTest.

    private companion object {
        const val TEST_DB = "migration-23-test.db"
        const val SCHEMA_V2 = 2
    }
}
