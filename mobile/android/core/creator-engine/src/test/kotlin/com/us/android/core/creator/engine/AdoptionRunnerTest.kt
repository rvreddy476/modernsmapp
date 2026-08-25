package com.us.android.core.creator.engine

import android.content.Context
import android.net.Uri
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.CreatorCommand
import com.us.android.core.creator.model.EditSession
import com.us.android.core.database.ComposerDraftEntity
import com.us.android.core.database.ComposerDraftFallbackStateEntity
import com.us.android.core.database.CreatorLegacyRecoveryEntity
import com.us.android.core.database.CreatorMigrationStagingEntity
import com.us.android.core.database.UsDatabase
import com.us.android.core.database.UsDatabaseCallbacks
import com.us.android.core.database.UsDatabaseMigrations
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import java.io.File

/**
 * The startup adoption pass — stage two and three of the legacy migration,
 * against a real Room database and a real filesystem vault.
 *
 * ## WHAT WAS PREVIOUSLY MISSING
 *
 * The migration staged rows and the decision logic existed, but nothing CALLED
 * them: an upgrading user's draft sat in staging forever. This runner is that
 * missing orchestration, and these tests are its proof — including the
 * idempotency that makes it safe to fire on every app start.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class AdoptionRunnerTest {

    private lateinit var context: Context
    private lateinit var db: UsDatabase
    private lateinit var vault: SourceVault
    private lateinit var store: ProjectStore
    private lateinit var runner: AdoptionRunner

    private val photoBytes = "android-creator-project-v1/fixture-asset/a1".toByteArray()
    private val frozen =
        """{"text":"Notes","visibility":"public","content_type":"post","post_type":"text",""" +
            """"app_origin":"postbook","media_ids":[],"language":"en",""" +
            """"distribution":{"version":1,"main_feed":true,"notify_subscribers":false,""" +
            """"create_reel_preview":false}}"""

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        context.getDatabasePath(TEST_DB).delete()
        File(context.filesDir, "creator").deleteRecursively()
        db = Room.databaseBuilder(context, UsDatabase::class.java, TEST_DB)
            .also { builder -> UsDatabaseMigrations.forEach { builder.addMigrations(it) } }
            .also { builder -> UsDatabaseCallbacks.all.forEach { builder.addCallback(it) } }
            .allowMainThreadQueries()
            .build()
        vault = SourceVault(context, Dispatchers.IO)
        store = ProjectStore(db)
        runner = AdoptionRunner(context, db, vault, store)
    }

    @After
    fun tearDown() {
        db.close()
        context.getDatabasePath(TEST_DB).delete()
    }

    private fun stage(
        text: String = "a draft",
        imageUri: String? = null,
        mediaId: String? = null,
        key: String? = null,
        frozenJson: String? = null,
        classification: String = CreatorMigrationStagingEntity.CLASSIFICATION_CLEAN,
        state: String = CreatorMigrationStagingEntity.STATE_PENDING,
    ) = runBlocking {
        db.openHelper.writableDatabase.execSQL(
            """
            INSERT INTO creator_migration_staging (
                stagingId, text, imageUri, altText, decorative, language, mediaId,
                creationKey, frozenRequestJson, classification, adoptionState,
                attempts, updatedAtMillis)
            VALUES ('composer', ?, ?, 'sunset', 0, 'en', ?, ?, ?, ?, ?, 0, 1)
            """.trimIndent(),
            arrayOf(text, imageUri, mediaId, key, frozenJson, classification, state),
        )
    }

    private fun readableUri(): String {
        val uri = Uri.parse("content://media/external/images/media/77")
        Shadows.shadowOf(context.contentResolver).registerInputStream(uri, photoBytes.inputStream())
        return uri.toString()
    }

    // ------------------------------------------------------------------
    // The ordinary adoptions
    // ------------------------------------------------------------------

    @Test
    fun `a text-only draft becomes a valid text project`() = runBlocking {
        stage(text = "Notes from a slow morning")

        val outcome = runner.runIfNeeded(now = 2)

        val projectId = (outcome as AdoptionRunner.Outcome.ProjectCreated).projectId
        val loaded = store.load(projectId) as ProjectStore.LoadResult.Loaded
        assertThat(loaded.project.postText.value).isEqualTo("Notes from a slow morning")
        assertThat(loaded.project.pages).isEmpty()
    }

    @Test
    fun `a draft with a readable image becomes a one-page project backed by the vault`() = runBlocking {
        stage(imageUri = readableUri())

        val outcome = runner.runIfNeeded(now = 2)

        val projectId = (outcome as AdoptionRunner.Outcome.ProjectCreated).projectId
        val project = (store.load(projectId) as ProjectStore.LoadResult.Loaded).project
        val source = project.sourceAssets.single()
        // The bytes really are in the vault and really match their hash.
        assertThat(vault.verify(source.vaultPath, source.sha256)).isTrue()
        assertThat(project.pages.single().accessibility.altText).isEqualTo("sunset")
    }

    /** The second run finds ADOPTED and does nothing — every-start safety. */
    @Test
    fun `adoption is idempotent across restarts`() = runBlocking {
        stage(text = "once only")

        val first = runner.runIfNeeded(now = 2)
        val second = runner.runIfNeeded(now = 3)

        assertThat(first).isInstanceOf(AdoptionRunner.Outcome.ProjectCreated::class.java)
        assertThat(second).isEqualTo(AdoptionRunner.Outcome.NothingToAdopt)
        assertThat(store.all()).hasSize(1)
    }

    @Test
    fun `an empty staging table is a no-op`() = runBlocking {
        assertThat(runner.runIfNeeded(now = 2)).isEqualTo(AdoptionRunner.Outcome.NothingToAdopt)
    }

    // ------------------------------------------------------------------
    // Operation authority still outranks everything
    // ------------------------------------------------------------------

    /** A frozen operation routes to retryable recovery even with a readable image. */
    @Test
    fun `a frozen operation becomes RETRYABLE_PUBLISH, not an editable project`() = runBlocking {
        stage(imageUri = readableUri(), key = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f", frozenJson = frozen)

        val outcome = runner.runIfNeeded(now = 2)

        assertThat(outcome).isEqualTo(
            AdoptionRunner.Outcome.RoutedToRecovery(CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH),
        )
        // No project was created, and the frozen bytes survived verbatim.
        assertThat(store.all()).isEmpty()
        val recovery = runBlocking { db.creatorMigrationDao().recoveries() }.single()
        assertThat(recovery.frozenRequestJson).isEqualTo(frozen)
        // The legacy draft is retained — recovery never deletes it.
        assertThat(runBlocking { db.creatorMigrationDao().fallbackState() }!!.reason)
            .isEqualTo(ComposerDraftFallbackStateEntity.REASON_RECOVERY_REQUIRED)
    }

    /** A confirmed remote asset with no local source is UNUSABLE, never invented. */
    @Test
    fun `a remote-only media id routes to UNUSABLE recovery`() = runBlocking {
        stage(mediaId = "6f3b1c58-2a41-4e0d-9c77-1b5a0d8e4f21")

        val outcome = runner.runIfNeeded(now = 2)

        assertThat(outcome).isEqualTo(
            AdoptionRunner.Outcome.RoutedToRecovery(CreatorLegacyRecoveryEntity.KIND_UNUSABLE),
        )
        assertThat(store.all()).isEmpty()
    }

    /** A quarantined row is untouchable — the recovery screen owns it. */
    @Test
    fun `a quarantined row is left exactly where the migration put it`() = runBlocking {
        stage(
            key = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f",
            classification = CreatorMigrationStagingEntity.CLASSIFICATION_HALF_FROZEN,
            state = CreatorMigrationStagingEntity.STATE_QUARANTINED,
        )

        val outcome = runner.runIfNeeded(now = 2)

        assertThat(outcome).isInstanceOf(AdoptionRunner.Outcome.Quarantined::class.java)
        val staging = runBlocking {
            db.creatorMigrationDao().staging(CreatorMigrationStagingEntity.SINGLETON_ID)
        }
        assertThat(staging!!.adoptionState).isEqualTo(CreatorMigrationStagingEntity.STATE_QUARANTINED)
    }

    // ------------------------------------------------------------------
    // The dual-write rule, end to end through ProjectStore
    // ------------------------------------------------------------------

    @Test
    fun `growing a project past the legacy shape clears the draft and marks it unrepresentable`() =
        runBlocking {
            stage(imageUri = readableUri())
            val projectId =
                (runner.runIfNeeded(now = 2) as AdoptionRunner.Outcome.ProjectCreated).projectId
            val project = (store.load(projectId) as ProjectStore.LoadResult.Loaded).project

            // Import a second source and add a second page — now unrepresentable.
            val second = vault.importSource(
                Uri.parse(readableUri()),
                assetId = "second",
            )!!
            val session = EditSession(
                project.copy(
                    sourceAssets = project.sourceAssets + com.us.android.core.creator.model.SourceAsset(
                        assetId = second.assetId,
                        kind = "image",
                        vaultPath = second.relativePath,
                        sha256 = second.sha256,
                        bytes = second.bytes,
                        mime = "image/jpeg",
                        widthPx = 1,
                        heightPx = 1,
                        origin = "photoPicker",
                    ),
                ),
            )
            session.apply(CreatorCommand.AddPage("p2", "l2", "second"))
            store.save(session.current, now = 3)

            assertThat(runBlocking { db.composerDraftDao().load() }).isNull()
            val fallback = runBlocking { db.creatorMigrationDao().fallbackState() }!!
            assertThat(fallback.state).isEqualTo(ComposerDraftFallbackStateEntity.UNAVAILABLE)
            assertThat(fallback.reason)
                .isEqualTo(ComposerDraftFallbackStateEntity.REASON_UNREPRESENTABLE)

            // And shrinking back restores the mirror and the fallback.
            session.apply(CreatorCommand.RemovePage("p2"))
            store.save(session.current, now = 4)

            assertThat(runBlocking { db.composerDraftDao().load() }).isNotNull()
            assertThat(runBlocking { db.creatorMigrationDao().fallbackState() }!!.state)
                .isEqualTo(ComposerDraftFallbackStateEntity.AVAILABLE)
        }

    /** A representable save refreshes the legacy mirror with the CURRENT text. */
    @Test
    fun `a representable save keeps the legacy mirror fresh`() = runBlocking {
        stage(text = "first words")
        val projectId =
            (runner.runIfNeeded(now = 2) as AdoptionRunner.Outcome.ProjectCreated).projectId
        val project = (store.load(projectId) as ProjectStore.LoadResult.Loaded).project

        val session = EditSession(project)
        session.apply(
            CreatorCommand.SetPostText(
                com.us.android.core.creator.model.PostText("rewritten words", "en"),
            ),
        )
        store.save(session.current, now = 3)

        val mirror: ComposerDraftEntity? = runBlocking { db.composerDraftDao().load() }
        assertThat(mirror!!.text).isEqualTo("rewritten words")
        assertThat(mirror.creationKey).isNull()
    }

    private companion object {
        const val TEST_DB = "adoption-runner.db"
    }
}
