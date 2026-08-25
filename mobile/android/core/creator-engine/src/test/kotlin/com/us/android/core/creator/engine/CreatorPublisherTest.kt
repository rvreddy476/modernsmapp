package com.us.android.core.creator.engine

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.Accessibility
import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.Canvas
import com.us.android.core.creator.model.CreateOutcome
import com.us.android.core.creator.model.CreatorReducer
import com.us.android.core.creator.model.ImageLayer
import com.us.android.core.creator.model.Page
import com.us.android.core.creator.model.PostText
import com.us.android.core.creator.model.PublishTransport
import com.us.android.core.creator.model.RenderExporter
import com.us.android.core.creator.model.RenderResult
import com.us.android.core.creator.model.SafeZone
import com.us.android.core.creator.model.SourceAsset
import com.us.android.core.creator.model.UploadOutcome
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
import org.robolectric.annotation.Config
import java.io.File

/**
 * The essential publisher regressions: duplicate publication, lost work,
 * process recovery. Everything runs against REAL Room, a REAL vault, and fake
 * ports — the fakes record what crossed them, which is the whole point.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CreatorPublisherTest {

    private lateinit var context: Context
    private lateinit var db: UsDatabase
    private lateinit var vault: SourceVault
    private lateinit var store: ProjectStore
    private lateinit var transport: RecordingTransport
    private lateinit var publisher: CreatorPublisher

    private val photoBytes = "android-creator-project-v1/fixture-asset/a1".toByteArray()

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
        transport = RecordingTransport()
        publisher = CreatorPublisher(db, store, vault, FakeRenderer(), transport)
    }

    @After
    fun tearDown() {
        db.close()
        context.getDatabasePath(TEST_DB).delete()
    }

    /** Renders a deterministic per-page payload; no Android graphics needed here. */
    private class FakeRenderer : RenderExporter {
        override suspend fun renderPage(
            project: AndroidCreatorProject,
            pageId: String,
            sourceBytes: Map<String, ByteArray>,
        ): RenderResult = RenderResult.Success(
            bytes = "rendered:$pageId".toByteArray(),
            widthPx = 1080,
            heightPx = 1350,
            mime = "image/jpeg",
        )

        override suspend fun buildProxy(sourceBytes: ByteArray, maxEdgePx: Int): RenderResult =
            RenderResult.Success(sourceBytes, 100, 125, "image/jpeg")
    }

    /**
     * A transport that records every crossing and can be programmed to fail.
     *
     * [frozenBytesSeen] is the load-bearing recorder: replays must present the
     * IDENTICAL bytes and key, or the idempotency story is fiction.
     */
    private class RecordingTransport : PublishTransport {
        val uploadedAlts = mutableListOf<Pair<String, Boolean>>()
        val uploadCount = mutableMapOf<String, Int>()
        val createCalls = mutableListOf<Pair<String, String>>()
        var freezeCount = 0
        var failUploadOfAlt: String? = null
        var createOutcome: (Int) -> CreateOutcome = { CreateOutcome.Created("post-1") }
        private var nextMedia = 0

        override fun freezeCreateRequest(
            text: String,
            language: String,
            postType: String,
            mediaIds: List<String>,
        ): ByteArray {
            freezeCount++
            return """{"post_type":"$postType","media_ids":[${
                mediaIds.joinToString(",") { "\"$it\"" }
            }],"text":"$text"}""".toByteArray()
        }

        override suspend fun uploadPage(
            bytes: ByteArray,
            mime: String,
            altText: String,
            decorative: Boolean,
        ): UploadOutcome {
            val page = bytes.decodeToString().removePrefix("rendered:")
            uploadCount[page] = (uploadCount[page] ?: 0) + 1
            if (failUploadOfAlt == altText) return UploadOutcome.Retryable("programmed failure")
            uploadedAlts += altText to decorative
            return UploadOutcome.Confirmed("6f3b1c58-2a41-4e0d-9c77-1b5a0d8e4f2${nextMedia++}")
        }

        override suspend fun createPost(creationKey: String, frozenRequest: ByteArray): CreateOutcome {
            createCalls += creationKey to frozenRequest.decodeToString()
            return createOutcome(createCalls.size)
        }

        val frozenBytesSeen: List<String> get() = createCalls.map { it.second }
    }

    private fun projectWithPages(vararg alts: Pair<String, Boolean>): String = runBlocking {
        val assets = alts.indices.map { index ->
            val uri = android.net.Uri.parse("content://media/$index")
            org.robolectric.Shadows.shadowOf(context.contentResolver)
                .registerInputStream(uri, photoBytes.inputStream())
            vault.importSource(uri, "asset$index")!!
        }
        val project = AndroidCreatorProject(
            projectId = "01J9Z4K7QW8XN2VB3M5R7T9Y0C",
            revision = 1,
            status = AndroidCreatorProject.STATUS_EDITING,
            createdAtMillis = 1,
            updatedAtMillis = 1,
            postText = PostText("three cards", "en"),
            canvas = Canvas(1080, 1350, "4:5", SafeZone(0, 0, 0, 0)),
            sourceAssets = assets.mapIndexed { index, entry ->
                SourceAsset(
                    assetId = entry.assetId,
                    kind = "image",
                    vaultPath = entry.relativePath,
                    sha256 = entry.sha256,
                    bytes = entry.bytes,
                    mime = "image/jpeg",
                    widthPx = 10,
                    heightPx = 10,
                    origin = "photoPicker",
                )
            },
            pages = alts.mapIndexed { index, (alt, decorative) ->
                Page(
                    pageId = "page$index",
                    accessibility = Accessibility(alt, decorative),
                    layers = listOf(
                        ImageLayer(
                            layerId = "layer$index",
                            z = 0,
                            transform = CreatorReducer.IDENTITY_TRANSFORM,
                            assetRef = "asset$index",
                            crop = CreatorReducer.FULL_CROP,
                            adjustments = CreatorReducer.NO_ADJUSTMENTS,
                        ),
                    ),
                )
            },
        )
        store.save(project, now = 1)
        project.projectId
    }

    // ------------------------------------------------------------------

    /** The happy path: page order becomes media order; everything resolves. */
    @Test
    fun `a publish uploads pages in order and resolves the operation`() = runBlocking {
        val id = projectWithPages("first" to false, "second" to false, "" to true)

        val result = publisher.publish(id)

        assertThat(result).isEqualTo(CreatorPublisher.PublishResult.Published("post-1"))
        // Alt decisions crossed the wire with their pages, in page order.
        assertThat(transport.uploadedAlts)
            .containsExactly("first" to false, "second" to false, "" to true).inOrder()
        // The operation resolved and freed the slot.
        assertThat(db.creatorPublishOperationDao().liveSlot(id)).isNull()
        val loaded = (store.load(id) as ProjectStore.LoadResult.Loaded).project
        assertThat(loaded.status).isEqualTo(AndroidCreatorProject.STATUS_PUBLISHED)
    }

    /**
     * PROCESS RECOVERY: a failure mid-carousel re-uploads NOTHING it finished.
     *
     * Attempt one dies at page two. Attempt two must skip page one entirely —
     * its checkpoint is durable — and every page still uploads exactly once
     * overall. Re-uploading is wasted bytes at best and a duplicate confirmed
     * asset at worst.
     */
    @Test
    fun `a retry resumes from the checkpoint instead of re-uploading finished pages`() =
        runBlocking {
            val id = projectWithPages("one" to false, "two" to false)
            transport.failUploadOfAlt = "two"

            val first = publisher.publish(id)
            assertThat(first).isInstanceOf(CreatorPublisher.PublishResult.Retryable::class.java)

            transport.failUploadOfAlt = null
            val second = publisher.publish(id)

            assertThat(second).isInstanceOf(CreatorPublisher.PublishResult.Published::class.java)
            assertThat(transport.uploadCount["page0"]).isEqualTo(1)
            // Page two: one failed attempt + one successful one.
            assertThat(transport.uploadCount["page1"]).isEqualTo(2)
        }

    /**
     * DUPLICATE PUBLICATION: the retry replays the SAME key and the SAME bytes.
     *
     * A create that fails retryably keeps the operation live. The next publish
     * must find it and replay it verbatim — a second freeze would mint a second
     * key, and a second key is a second post.
     */
    @Test
    fun `a create retry replays the identical key and bytes and freezes only once`() =
        runBlocking {
            val id = projectWithPages("only" to false)
            transport.createOutcome = { attempt ->
                if (attempt == 1) CreateOutcome.Retryable("flaky") else CreateOutcome.Created("post-9")
            }

            val first = publisher.publish(id)
            assertThat(first).isInstanceOf(CreatorPublisher.PublishResult.Retryable::class.java)

            val second = publisher.publish(id)
            assertThat(second).isEqualTo(CreatorPublisher.PublishResult.Published("post-9"))

            assertThat(transport.freezeCount).isEqualTo(1)
            assertThat(transport.createCalls).hasSize(2)
            assertThat(transport.createCalls[0].first).isEqualTo(transport.createCalls[1].first)
            assertThat(transport.frozenBytesSeen[0]).isEqualTo(transport.frozenBytesSeen[1])
        }

    /** The lost-response case: AlreadyCreated resolves exactly like Created. */
    @Test
    fun `an already-created response resolves without a second post`() = runBlocking {
        val id = projectWithPages("only" to false)
        transport.createOutcome = { CreateOutcome.AlreadyCreated("post-earlier") }

        val result = publisher.publish(id)

        assertThat(result).isEqualTo(CreatorPublisher.PublishResult.Published("post-earlier"))
        assertThat(db.creatorPublishOperationDao().liveSlot(id)).isNull()
    }

    /** The accessibility gate: an undecided page publishes NOTHING. */
    @Test
    fun `a page without an accessibility decision blocks the publish before any upload`() =
        runBlocking {
            val id = projectWithPages("described" to false, "" to false)

            val result = publisher.publish(id)

            assertThat(result).isInstanceOf(CreatorPublisher.PublishResult.Failed::class.java)
            assertThat(transport.uploadedAlts).isEmpty()
            assertThat(transport.createCalls).isEmpty()
        }

    /** DATA LOSS: a tampered vault source fails closed before anything uploads. */
    @Test
    fun `a source that fails hash verification refuses to publish`() = runBlocking {
        val id = projectWithPages("described" to false)
        val project = (store.load(id) as ProjectStore.LoadResult.Loaded).project
        vault.resolve(project.sourceAssets.first().vaultPath)!!
            .writeBytes("tampered".toByteArray())

        val result = publisher.publish(id)

        assertThat(result).isInstanceOf(CreatorPublisher.PublishResult.Failed::class.java)
        assertThat(transport.uploadedAlts).isEmpty()
    }

    private companion object {
        const val TEST_DB = "creator-publisher-test.db"
    }
}
