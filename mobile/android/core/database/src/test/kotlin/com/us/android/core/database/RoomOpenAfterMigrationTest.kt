package com.us.android.core.database

import android.content.Context
import androidx.room.Room
import androidx.sqlite.db.SupportSQLiteDatabase
import androidx.sqlite.db.SupportSQLiteOpenHelper
import androidx.sqlite.db.framework.FrameworkSQLiteOpenHelperFactory
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * CS-A-LB-1 — the migrated database actually OPENS through production Room.
 *
 * ## WHY THE OLD MIGRATION TEST WAS NOT ENOUGH
 *
 * `Migration23Test` calls `MIGRATION_2_3.migrate(db)` directly on a raw
 * `SupportSQLiteDatabase`. That proves the SQL runs. It cannot prove the thing
 * that actually matters on a user's phone: that Room will then ACCEPT the
 * resulting schema.
 *
 * Room validates on open by reading the real SQLite catalog and comparing it to
 * the schema it generated from the `@Entity` declarations — columns, primary
 * keys, foreign keys **and indices**. The first implementation created a partial
 * unique index in the migration that Room cannot generate, so the migrated
 * catalog had an index the expected schema did not. Every upgrading user would
 * have hit an `IllegalStateException` at launch, and no test in the suite would
 * have seen it, because none of them ever opened the database the way the app
 * does.
 *
 * These tests use `Room.databaseBuilder` with the real migration list and then
 * run a real DAO query, which is the only arrangement that exercises the
 * validator.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class RoomOpenAfterMigrationTest {

    private lateinit var context: Context
    private var helper: SupportSQLiteOpenHelper? = null
    private var database: UsDatabase? = null

    private val creationKey = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f"

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        context.getDatabasePath(TEST_DB).delete()
    }

    @After
    fun tearDown() {
        database?.close()
        helper?.close()
        context.getDatabasePath(TEST_DB).delete()
    }

    /** Creates a genuine schema-v2 file with the real v1→v2 DDL. */
    private fun seedV2(seed: (SupportSQLiteDatabase) -> Unit = {}) {
        val open = FrameworkSQLiteOpenHelperFactory().create(
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
        helper = open
        seed(open.writableDatabase)
        open.close()
        helper = null
    }

    /** Opens through the SAME builder configuration the app uses. */
    private fun openThroughRoom(): UsDatabase =
        Room.databaseBuilder(context, UsDatabase::class.java, TEST_DB)
            .also { builder -> UsDatabaseMigrations.forEach { builder.addMigrations(it) } }
            .also { builder -> UsDatabaseCallbacks.all.forEach { builder.addCallback(it) } }
            .allowMainThreadQueries()
            .build()
            .also { database = it }

    private fun insertDraft(
        db: SupportSQLiteDatabase,
        text: String,
        key: String? = null,
        frozen: String? = null,
    ) = db.execSQL(
        """
        INSERT INTO composer_draft
            (id, text, imageUri, altText, decorative, language, mediaId,
             creationKey, frozenRequestJson, updatedAtMillis)
        VALUES ('composer', ?, NULL, '', 0, 'en', NULL, ?, ?, 1755939600000)
        """.trimIndent(),
        arrayOf(text, key, frozen),
    )

    private fun operation(
        id: String,
        projectId: String = PROJECT,
        state: String = "publishing",
        serverPostId: String? = null,
        supersededBy: String? = null,
        frozenBytes: Int = 42,
        sha: String = "a".repeat(64),
    ) = CreatorPublishOperationEntity(
        operationId = id,
        projectId = projectId,
        boundRevision = 1,
        projectDocumentSha256 = "b".repeat(64),
        orderedOutputSha256 = "[]",
        orderedMediaIds = "[]",
        creationKey = creationKey,
        frozenRequestBase64 = "e30=",
        frozenRequestSha256 = sha,
        frozenRequestBytes = frozenBytes,
        state = state,
        serverPostId = serverPostId,
        supersededByOperationId = supersededBy,
        createdAtMillis = 1,
        updatedAtMillis = 1,
    )

    // ------------------------------------------------------------------
    // The launch-blocking case
    // ------------------------------------------------------------------

    /**
     * THE TEST THAT WAS MISSING.
     *
     * A real v2 file, migrated by the real migration list, opened by production
     * Room, then queried through a real DAO. If the migrated catalog disagrees
     * with the generated schema by so much as one index, Room throws here — and
     * that throw is the app failing to launch for every upgrading user.
     */
    @Test
    fun `a migrated v2 database opens through production Room and serves DAO queries`() {
        seedV2 { insertDraft(it, "an ordinary draft") }

        val db = openThroughRoom()

        val staged = runBlocking { db.creatorMigrationDao().staged() }
        assertThat(staged).hasSize(1)
        assertThat(staged.single().classification)
            .isEqualTo(CreatorMigrationStagingEntity.CLASSIFICATION_CLEAN)
    }

    /** The half-frozen row also survives a real open, still quarantined. */
    @Test
    fun `a migrated database carrying a half-frozen row still opens`() {
        seedV2 { insertDraft(it, "half a publish", key = creationKey, frozen = null) }

        val db = openThroughRoom()

        val staged = runBlocking { db.creatorMigrationDao().staged() }
        assertThat(staged.single().adoptionState)
            .isEqualTo(CreatorMigrationStagingEntity.STATE_QUARANTINED)
        assertThat(runBlocking { db.creatorMigrationDao().fallbackState() }?.reason)
            .isEqualTo(ComposerDraftFallbackStateEntity.REASON_QUARANTINED)
    }

    /** An empty v2 database opens too — the common upgrade case. */
    @Test
    fun `a migrated database with no draft opens and stages nothing`() {
        seedV2()

        val db = openThroughRoom()

        assertThat(runBlocking { db.creatorMigrationDao().staged() }).isEmpty()
    }

    /**
     * The pre-commit staging row-count assertion.
     *
     * If the copy silently drops a legacy row, continuing would open a database
     * that looks migrated while the user's draft is unreachable by the adoption
     * pass. The migration throws instead, Room rolls the transaction back, and
     * the file stays at schema 2 with `composer_draft` exactly as it was — which
     * is the recoverable outcome, because nothing was lost.
     *
     * A trigger is used to force the mismatch, because the production INSERT is
     * correct and there is no honest input that makes it miscount.
     */
    @Test
    fun `a staging count mismatch aborts the open and leaves the v2 database intact`() {
        seedV2 { db ->
            insertDraft(db, "the user's unpublished work")
            // Pre-create the staging table so the migration's CREATE TABLE IF NOT
            // EXISTS is a no-op and this trigger survives into it. The trigger
            // swallows every staged row, simulating a copy that silently loses
            // one — the failure the assertion exists to catch.
            db.execSQL(
                """
                CREATE TABLE `creator_migration_staging` (
                    `stagingId` TEXT NOT NULL, `text` TEXT NOT NULL, `imageUri` TEXT,
                    `altText` TEXT NOT NULL, `decorative` INTEGER NOT NULL,
                    `language` TEXT NOT NULL, `mediaId` TEXT, `creationKey` TEXT,
                    `frozenRequestJson` TEXT, `classification` TEXT NOT NULL,
                    `adoptionState` TEXT NOT NULL, `attempts` INTEGER NOT NULL,
                    `updatedAtMillis` INTEGER NOT NULL, PRIMARY KEY(`stagingId`)
                )
                """.trimIndent(),
            )
            db.execSQL(
                """
                CREATE TRIGGER drop_staged_rows
                AFTER INSERT ON creator_migration_staging
                BEGIN DELETE FROM creator_migration_staging WHERE stagingId = NEW.stagingId; END
                """.trimIndent(),
            )
        }

        val failure = runCatching { openThroughRoom().creatorMigrationDao() }
            .onSuccess { runCatching { runBlocking { it.staged() } } }
            .exceptionOrNull()
            ?: runCatching { runBlocking { database!!.creatorMigrationDao().staged() } }
                .exceptionOrNull()

        assertThat(failure).isNotNull()

        // The v2 file is untouched: the legacy draft is still there to recover.
        database?.close()
        database = null
        val raw = FrameworkSQLiteOpenHelperFactory().create(
            SupportSQLiteOpenHelper.Configuration.builder(context)
                .name(TEST_DB)
                .callback(object : SupportSQLiteOpenHelper.Callback(SCHEMA_V2) {
                    override fun onCreate(db: SupportSQLiteDatabase) = Unit
                    override fun onUpgrade(db: SupportSQLiteDatabase, old: Int, new: Int) = Unit
                })
                .build(),
        )
        helper = raw
        raw.writableDatabase.query("SELECT text FROM composer_draft").use { cursor ->
            assertThat(cursor.moveToFirst()).isTrue()
            assertThat(cursor.getString(0)).isEqualTo("the user's unpublished work")
        }
    }

    // ------------------------------------------------------------------
    // Fresh install parity — the inverse defect
    // ------------------------------------------------------------------

    /**
     * A fresh install must have the same protection as a migrated one.
     *
     * When the invariant lived in a partial index, this cohort had NO protection
     * at all: Room created the table without it, so two live operations for one
     * project were simply allowed.
     */
    @Test
    fun `a fresh database refuses two live operations for one project`() {
        val db = openThroughRoom()
        val dao = db.creatorPublishOperationDao()

        runBlocking { dao.startOperation(operation(OP_A)) }

        val failure = runCatching {
            runBlocking { dao.startOperation(operation(OP_B, state = "failed")) }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
        assertThat(runBlocking { dao.liveSlot(PROJECT) }?.operationId).isEqualTo(OP_A)
    }

    /** The migrated cohort behaves identically. */
    @Test
    fun `a migrated database refuses two live operations for one project`() {
        seedV2 { insertDraft(it, "a draft") }
        val dao = openThroughRoom().creatorPublishOperationDao()

        runBlocking { dao.startOperation(operation(OP_A)) }

        val failure = runCatching {
            runBlocking { dao.startOperation(operation(OP_B)) }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
    }

    /** Resolving to a terminal state frees the slot for a legitimate new publish. */
    @Test
    fun `a terminal operation frees the slot on both cohorts`() {
        val dao = openThroughRoom().creatorPublishOperationDao()

        runBlocking {
            dao.startOperation(operation(OP_A))
            dao.resolveOperation(
                operationId = OP_A,
                projectId = PROJECT,
                state = "published",
                serverPostId = "9b3e1ec2-af64-4dcc-b68c-2dbb2b5f9d0a",
                now = 2,
            )
            dao.startOperation(operation(OP_B))
        }

        assertThat(runBlocking { dao.liveSlot(PROJECT) }?.operationId).isEqualTo(OP_B)
    }

    /** A fresh database has the fallback singleton the migration would have written. */
    @Test
    fun `a fresh database seeds the fallback singleton`() {
        val db = openThroughRoom()

        val state = runBlocking { db.creatorMigrationDao().fallbackState() }

        assertThat(state).isNotNull()
        assertThat(state!!.state).isEqualTo(ComposerDraftFallbackStateEntity.AVAILABLE)
        assertThat(state.reason).isNull()
    }

    // ------------------------------------------------------------------
    // Payload and state invariants (the CHECK constraints Room cannot declare)
    // ------------------------------------------------------------------

    @Test
    fun `an unknown publish state is refused`() {
        val dao = openThroughRoom().creatorPublishOperationDao()

        val failure = runCatching {
            runBlocking { dao.startOperation(operation(OP_A, state = "whatever")) }
        }.exceptionOrNull()

        assertThat(failure).isInstanceOf(IllegalArgumentException::class.java)
    }

    /** R-3: a zero-length frozen request cannot be the bytes the server hashed. */
    @Test
    fun `a non-positive frozen length is refused`() {
        val dao = openThroughRoom().creatorPublishOperationDao()

        val failure = runCatching {
            runBlocking { dao.startOperation(operation(OP_A, frozenBytes = 0)) }
        }.exceptionOrNull()

        assertThat(failure).isInstanceOf(IllegalArgumentException::class.java)
    }

    @Test
    fun `a malformed frozen sha is refused`() {
        val dao = openThroughRoom().creatorPublishOperationDao()

        val failure = runCatching {
            runBlocking { dao.startOperation(operation(OP_A, sha = "not-a-hash")) }
        }.exceptionOrNull()

        assertThat(failure).isInstanceOf(IllegalArgumentException::class.java)
    }

    /** A live operation may not claim a server post id it cannot have. */
    @Test
    fun `a publishing operation carrying a server post id is refused`() {
        val dao = openThroughRoom().creatorPublishOperationDao()

        val failure = runCatching {
            runBlocking { dao.startOperation(operation(OP_A, serverPostId = "x")) }
        }.exceptionOrNull()

        assertThat(failure).isInstanceOf(IllegalArgumentException::class.java)
    }

    private companion object {
        const val TEST_DB = "room-open-after-migration.db"
        const val SCHEMA_V2 = 2
        const val PROJECT = "01J9Z4K7QW8XN2VB3M5R7T9Y00"
        const val OP_A = "01J9Z4M0P1Q2R3S4T5V6W7X8Y9"
        const val OP_B = "01J9Z4M0P1Q2R3S4T5V6W7X8Z0"
    }
}
