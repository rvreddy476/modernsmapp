package com.us.android.core.database

import android.content.Context
import androidx.room.Room
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
 * CS-A-LB-2 — recovery cleanup can never delete work it does not own.
 *
 * ## THE DEFECT THIS CLOSES
 *
 * `completeRecoveredPublish` used to run four statements in a transaction:
 * delete the named recovery, delete the named staging row, clear **all** legacy
 * composer drafts, mark the fallback available. It never checked that the first
 * two affected anything.
 *
 * So a stale or mismatched callback — a retry delivered twice, a recycled id, a
 * response arriving for a recovery that was already cleaned — could name rows
 * that do not exist, delete nothing, and then clear the user's retained legacy
 * draft anyway. That draft is the rollback surface and may be the only copy of
 * work they never published. Losing it is silent.
 *
 * The cleanup now proves identity (the creation key it resolved) and proves row
 * counts before it touches `composer_draft` at all.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class RecoveryCleanupSafetyTest {

    private lateinit var context: Context
    private lateinit var db: UsDatabase

    private val creationKey = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f"
    private val otherKey = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
    private val frozen = """{"post_type":"text","media_ids":[]}"""

    private val dao get() = db.creatorMigrationDao()

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        context.getDatabasePath(TEST_DB).delete()
        db = Room.databaseBuilder(context, UsDatabase::class.java, TEST_DB)
            .also { builder -> UsDatabaseMigrations.forEach { builder.addMigrations(it) } }
            .also { builder -> UsDatabaseCallbacks.all.forEach { builder.addCallback(it) } }
            .allowMainThreadQueries()
            .build()
    }

    @After
    fun tearDown() {
        db.close()
        context.getDatabasePath(TEST_DB).delete()
    }

    private fun recovery(id: String = RECOVERY, key: String? = creationKey) =
        CreatorLegacyRecoveryEntity(
            recoveryId = id,
            kind = CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH,
            text = "a draft",
            language = "en",
            mediaId = null,
            creationKey = key,
            frozenRequestJson = frozen,
            frozenRequestSha = "a".repeat(64),
            frozenRequestLen = frozen.length,
            createdAtMillis = 1,
        )

    /** Puts a legacy draft on disk — the row that must never be collateral damage. */
    private fun seedLegacyDraft(text: String = "the user's unpublished work") = runBlocking {
        db.composerDraftDao().save(
            ComposerDraftEntity(
                text = text,
                imageUri = null,
                altText = "",
                decorative = false,
                language = "en",
                mediaId = null,
                creationKey = null,
                frozenRequestJson = null,
                updatedAtMillis = 1,
            ),
        )
    }

    private fun legacyDraftText(): String? = runBlocking { db.composerDraftDao().load()?.text }

    private fun seedRecoveryAndStaging() = runBlocking {
        db.creatorMigrationDao().insertRecovery(recovery())
        db.openHelper.writableDatabase.execSQL(
            """
            INSERT INTO creator_migration_staging (
                stagingId, text, imageUri, altText, decorative, language, mediaId,
                creationKey, frozenRequestJson, classification, adoptionState,
                attempts, updatedAtMillis)
            VALUES (?, 'a draft', NULL, '', 0, 'en', NULL, ?, ?, 'CLEAN', 'PENDING', 0, 1)
            """.trimIndent(),
            arrayOf(STAGING, creationKey, frozen),
        )
    }

    // ------------------------------------------------------------------
    // The happy path still works
    // ------------------------------------------------------------------

    @Test
    fun `a correct completion cleans exactly the intended rows`() {
        seedLegacyDraft()
        seedRecoveryAndStaging()

        runBlocking {
            dao.completeRecoveredPublish(RECOVERY, STAGING, expectedCreationKey = creationKey, now = 2)
        }

        assertThat(runBlocking { dao.recoveries() }).isEmpty()
        assertThat(runBlocking { dao.staged() }).isEmpty()
        assertThat(legacyDraftText()).isNull()
        assertThat(runBlocking { dao.fallbackState() }!!.state)
            .isEqualTo(ComposerDraftFallbackStateEntity.AVAILABLE)
    }

    /**
     * The same response delivered twice.
     *
     * The second delivery must be a refusal, not a second cleanup — and by then
     * the legacy draft is already gone, so the thing it must not do is throw the
     * database into a state that looks like a fresh unresolved recovery.
     */
    @Test
    fun `a replayed completion refuses instead of cleaning again`() {
        seedLegacyDraft()
        seedRecoveryAndStaging()
        runBlocking {
            dao.completeRecoveredPublish(RECOVERY, STAGING, expectedCreationKey = creationKey, now = 2)
        }

        val failure = runCatching {
            runBlocking {
                dao.completeRecoveredPublish(RECOVERY, STAGING, expectedCreationKey = creationKey, now = 3)
            }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
    }

    // ------------------------------------------------------------------
    // The data-loss cases
    // ------------------------------------------------------------------

    /** THE ONE THAT MATTERS: a recovery id that is not there must not clear the draft. */
    @Test
    fun `a completion naming a missing recovery leaves the legacy draft intact`() {
        seedLegacyDraft()

        val failure = runCatching {
            runBlocking {
                dao.completeRecoveredPublish("no-such-recovery", STAGING, creationKey, now = 2)
            }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
        assertThat(legacyDraftText()).isEqualTo("the user's unpublished work")
    }

    /**
     * A recycled id pointing at somebody else's recovery.
     *
     * The id matches but the creation key does not, so this response belongs to
     * different work. Cleaning up here would delete a draft whose publish never
     * resolved.
     */
    @Test
    fun `a completion whose creation key does not match rolls back untouched`() {
        seedLegacyDraft()
        seedRecoveryAndStaging()

        val failure = runCatching {
            runBlocking {
                dao.completeRecoveredPublish(RECOVERY, STAGING, expectedCreationKey = otherKey, now = 2)
            }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
        assertThat(legacyDraftText()).isEqualTo("the user's unpublished work")
        assertThat(runBlocking { dao.recoveries() }).hasSize(1)
        assertThat(runBlocking { dao.staged() }).hasSize(1)
    }

    /** A staging id that is not there rolls the whole thing back, recovery included. */
    @Test
    fun `a completion naming a missing staging row rolls back the recovery delete too`() {
        seedLegacyDraft()
        runBlocking { dao.insertRecovery(recovery()) }

        val failure = runCatching {
            runBlocking {
                dao.completeRecoveredPublish(RECOVERY, "no-such-staging", creationKey, now = 2)
            }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
        assertThat(legacyDraftText()).isEqualTo("the user's unpublished work")
        // The recovery delete must have rolled back with it.
        assertThat(runBlocking { dao.recoveries() }).hasSize(1)
    }

    // ------------------------------------------------------------------
    // routeToRecovery has the symmetric hole
    // ------------------------------------------------------------------

    /**
     * Routing must not disable the fallback when it staged nothing.
     *
     * Marking the fallback UNAVAILABLE while the staging row it refers to does
     * not exist leaves the legacy composer refusing to show a draft that no
     * recovery flow will ever pick up.
     */
    @Test
    fun `routing to recovery with a missing staging row leaves fallback untouched`() {
        seedLegacyDraft()
        val before = runBlocking { dao.fallbackState() }!!

        val failure = runCatching {
            runBlocking { dao.routeToRecovery(recovery(), "no-such-staging", now = 2) }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
        assertThat(runBlocking { dao.fallbackState() }!!.state).isEqualTo(before.state)
        assertThat(runBlocking { dao.recoveries() }).isEmpty()
        assertThat(legacyDraftText()).isEqualTo("the user's unpublished work")
    }

    @Test
    fun `routing to recovery with a real staging row disables the fallback`() {
        seedLegacyDraft()
        runBlocking {
            db.openHelper.writableDatabase.execSQL(
                """
                INSERT INTO creator_migration_staging (
                    stagingId, text, imageUri, altText, decorative, language, mediaId,
                    creationKey, frozenRequestJson, classification, adoptionState,
                    attempts, updatedAtMillis)
                VALUES (?, 'a draft', NULL, '', 0, 'en', NULL, ?, ?, 'CLEAN', 'PENDING', 0, 1)
                """.trimIndent(),
                arrayOf(STAGING, creationKey, frozen),
            )
            dao.routeToRecovery(recovery(), STAGING, now = 2)
        }

        val state = runBlocking { dao.fallbackState() }!!
        assertThat(state.state).isEqualTo(ComposerDraftFallbackStateEntity.UNAVAILABLE)
        assertThat(state.reason).isEqualTo(ComposerDraftFallbackStateEntity.REASON_RECOVERY_REQUIRED)
        // The legacy draft is RETAINED — recovery does not delete it.
        assertThat(legacyDraftText()).isEqualTo("the user's unpublished work")
    }

    /** A recovery row whose payload contradicts its kind never reaches the database. */
    @Test
    fun `routing refuses a RETRYABLE_PUBLISH with no frozen bytes`() {
        val malformed = recovery().copy(creationKey = null, frozenRequestJson = null)

        val failure = runCatching {
            runBlocking { dao.routeToRecovery(malformed, STAGING, now = 2) }
        }.exceptionOrNull()

        assertThat(failure).isInstanceOf(IllegalArgumentException::class.java)
        assertThat(runBlocking { dao.recoveries() }).isEmpty()
    }

    private companion object {
        const val TEST_DB = "recovery-cleanup-safety.db"
        const val RECOVERY = "r1"
        const val STAGING = "composer"
    }
}
