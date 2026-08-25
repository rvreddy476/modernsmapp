package com.us.android.core.creator.engine

import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.Canonical
import com.us.android.core.creator.model.ConfirmedMedia
import com.us.android.core.creator.model.CreateOutcome
import com.us.android.core.creator.model.PublishTransport
import com.us.android.core.creator.model.RenderExporter
import com.us.android.core.creator.model.RenderResult
import com.us.android.core.creator.model.RenderedOutput
import com.us.android.core.creator.model.UploadOutcome
import com.us.android.core.database.CreatorPublishOperationEntity
import com.us.android.core.database.UsDatabase
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import java.io.File
import java.security.SecureRandom
import java.util.Base64
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * THE publisher — the one orchestrator that turns a project into a post.
 *
 * ## HOW SLICE C'S AUTHORITY IS PRESERVED, NOT FORKED
 *
 * Every network step goes through [PublishTransport], whose production adapter
 * wraps the EXISTING proven pipeline: the same `MediaUploader`
 * reserve→PUT→confirm→exact-ready+passed→alt sequence, the same single
 * `createPost` call site, and frozen bytes serialized by the same
 * `CreatePostRequest` DTO the exact-wire tests guard. This class adds the
 * multi-page orchestration and the durable checkpoints; it re-implements no
 * wire behaviour.
 *
 * ## THE CHECKPOINT DESIGN (process-death recovery)
 *
 * The PROJECT DOCUMENT is the checkpoint. After each page's media is confirmed
 * ready+passed, the page's `renderedOutput` + `confirmedMedia` are saved
 * through [ProjectStore] before the next page starts. A relaunched publish
 * skips every page that already carries a confirmed media id whose output hash
 * matches — so a kill after page 2 of 3 re-uploads nothing and re-renders only
 * page 3.
 *
 * The CREATE step is guarded twice: the `creator_live_operation` slot (at most
 * one publish in flight per project, database-enforced) and the frozen
 * operation row holding the creation key + exact request bytes. A retry after
 * a lost response replays those bytes verbatim; the server's idempotency
 * authority answers with the same post.
 */
@Singleton
class CreatorPublisher @Inject constructor(
    private val db: UsDatabase,
    private val store: ProjectStore,
    private val vault: SourceVault,
    private val renderer: RenderExporter,
    private val transport: PublishTransport,
) {

    sealed interface PublishProgress {
        data class RenderingPage(val index: Int, val total: Int) : PublishProgress
        data class UploadingPage(val index: Int, val total: Int) : PublishProgress
        data object CreatingPost : PublishProgress
    }

    sealed interface PublishResult {
        data class Published(val postId: String) : PublishResult

        /** Try again later — nothing was lost, checkpoints hold. */
        data class Retryable(val reason: String) : PublishResult

        /** Retrying cannot fix this (moderation rejection, invalid project). */
        data class Failed(val reason: String) : PublishResult
    }

    /**
     * Publish a project. Safe to call repeatedly — every step is idempotent
     * against the durable state it left behind.
     */
    suspend fun publish(
        projectId: String,
        onProgress: suspend (PublishProgress) -> Unit = {},
    ): PublishResult {
        var project = when (val gated = loadPublishable(projectId)) {
            is Gated.Ready -> gated.project
            is Gated.Blocked -> return PublishResult.Failed(gated.reason)
        }

        // ---- Pages: render + upload, checkpointing after each ----
        when (val pages = publishPages(project, onProgress)) {
            is PagesOutcome.Done -> project = pages.project
            is PagesOutcome.Retry -> return PublishResult.Retryable(pages.reason)
            is PagesOutcome.Fatal -> return PublishResult.Failed(pages.reason)
        }

        // Every page is now confirmed, so PUBLISHING passes V-9 and the status
        // change is durable before any create attempt.
        if (project.status != AndroidCreatorProject.STATUS_PUBLISHING) {
            project = project.copy(
                revision = project.revision + 1,
                status = AndroidCreatorProject.STATUS_PUBLISHING,
            )
            store.save(project, now = System.currentTimeMillis())
        }

        // ---- The operation: freeze once, replay forever ----
        val operation = existingLiveOperation(projectId) ?: freezeOperation(project)

        onProgress(PublishProgress.CreatingPost)
        val frozenBytes = Base64.getDecoder().decode(operation.frozenRequestBase64)
        return when (val created = transport.createPost(operation.creationKey, frozenBytes)) {
            is CreateOutcome.Created -> complete(project, operation, created.postId)
            is CreateOutcome.AlreadyCreated -> complete(project, operation, created.postId)
            is CreateOutcome.Retryable -> {
                markFailed(operation, created.reason)
                PublishResult.Retryable(created.reason)
            }
            is CreateOutcome.Permanent -> {
                markFailed(operation, created.reason)
                PublishResult.Failed(created.reason)
            }
        }
    }

    // ------------------------------------------------------------------

    private sealed interface Gated {
        data class Ready(val project: AndroidCreatorProject) : Gated
        data class Blocked(val reason: String) : Gated
    }

    /**
     * Load the project and apply the publish gate.
     *
     * The gate is V-12's publish half: every page carries a REAL accessibility
     * decision before anything leaves the device.
     */
    private suspend fun loadPublishable(projectId: String): Gated {
        val project = when (val loaded = store.load(projectId)) {
            is ProjectStore.LoadResult.Loaded -> loaded.project
            is ProjectStore.LoadResult.Missing ->
                return Gated.Blocked("project $projectId does not exist")
            is ProjectStore.LoadResult.Corrupt ->
                return Gated.Blocked("project is corrupt: ${loaded.reason}")
            is ProjectStore.LoadResult.NeedsNewerApp ->
                return Gated.Blocked("project needs a newer app version")
        }
        project.pages.firstOrNull {
            !it.accessibility.decorative && it.accessibility.altText.isBlank()
        }?.let {
            return Gated.Blocked("page ${it.pageId} has no accessibility decision")
        }
        return Gated.Ready(project)
    }

    private sealed interface PagesOutcome {
        data class Done(val project: AndroidCreatorProject) : PagesOutcome
        data class Retry(val reason: String) : PagesOutcome
        data class Fatal(val reason: String) : PagesOutcome
    }

    /**
     * Render + upload every unconfirmed page, checkpointing after each one.
     *
     * The returned document carries every checkpoint made, so a retry — in
     * this process or the next — skips exactly the pages that finished.
     */
    private suspend fun publishPages(
        start: AndroidCreatorProject,
        onProgress: suspend (PublishProgress) -> Unit,
    ): PagesOutcome {
        var project = start
        val total = project.pages.size
        for ((index, page) in start.pages.withIndex()) {
            if (pageAlreadyConfirmed(project, page.pageId)) continue

            onProgress(PublishProgress.RenderingPage(index, total))
            val rendered = renderPage(project, page.pageId)
                ?: return PagesOutcome.Retry("page ${page.pageId} failed to render")
            if (rendered is Rendered.Fatal) return PagesOutcome.Fatal(rendered.reason)
            val output = rendered as Rendered.Ok

            onProgress(PublishProgress.UploadingPage(index, total))
            val pageState = project.pages.first { it.pageId == page.pageId }
            when (
                val uploaded = transport.uploadPage(
                    bytes = output.bytes,
                    mime = output.output.mime,
                    altText = pageState.accessibility.altText,
                    decorative = pageState.accessibility.decorative,
                )
            ) {
                is UploadOutcome.Retryable -> return PagesOutcome.Retry(uploaded.reason)
                is UploadOutcome.Permanent -> return PagesOutcome.Fatal(uploaded.reason)
                is UploadOutcome.Confirmed -> {
                    // THE CHECKPOINT: the confirmed id + output binding are
                    // durable before the next page starts.
                    project = checkpointPage(project, page.pageId, output.output, uploaded.mediaId)
                }
            }
        }
        return PagesOutcome.Done(project)
    }

    private sealed interface Rendered {
        data class Ok(val bytes: ByteArray, val output: RenderedOutput) : Rendered
        data class Fatal(val reason: String) : Rendered
    }

    private fun pageAlreadyConfirmed(project: AndroidCreatorProject, pageId: String): Boolean {
        val page = project.pages.first { it.pageId == pageId }
        val confirmed = page.confirmedMedia ?: return false
        val output = page.renderedOutput ?: return false
        // Only a binding that still matches counts; an edited page re-renders.
        return confirmed.boundOutputSha256 == output.sha256 &&
            confirmed.processingState == "ready" && confirmed.moderationState == "passed"
    }

    private suspend fun renderPage(project: AndroidCreatorProject, pageId: String): Rendered? {
        // Read every referenced source from the vault, verifying each against
        // its recorded hash — exporting a file that changed underneath the
        // project would publish something the author never saw.
        val sources = mutableMapOf<String, ByteArray>()
        for (asset in project.sourceAssets) {
            if (!vault.verify(asset.vaultPath, asset.sha256)) {
                return Rendered.Fatal("source ${asset.assetId} failed hash verification")
            }
            val file = vault.resolve(asset.vaultPath) ?: return Rendered.Fatal(
                "source ${asset.assetId} has an invalid vault path",
            )
            sources[asset.assetId] = file.readBytes()
        }

        return when (val result = renderer.renderPage(project, pageId, sources)) {
            is RenderResult.Success -> {
                val sha = result.sha256()
                val relative = "outputs/$pageId-${sha.take(OUTPUT_NAME_HASH_CHARS)}.bin"
                val target = vault.resolve(relative)
                    ?: return Rendered.Fatal("output path for $pageId is invalid")
                target.parentFile?.mkdirs()
                writeAtomically(target, result.bytes)
                Rendered.Ok(
                    bytes = result.bytes,
                    output = RenderedOutput(
                        outputId = "o$pageId",
                        vaultPath = relative,
                        sha256 = sha,
                        bytes = result.bytes.size.toLong(),
                        mime = result.mime,
                        widthPx = result.widthPx,
                        heightPx = result.heightPx,
                    ),
                )
            }
            is RenderResult.Failure ->
                if (result.recoverable) null else Rendered.Fatal(result.reason)
        }
    }

    private suspend fun checkpointPage(
        project: AndroidCreatorProject,
        pageId: String,
        output: RenderedOutput,
        mediaId: String,
    ): AndroidCreatorProject {
        // Status stays as-is here: V-9 forbids a PUBLISHING document with any
        // unconfirmed page, and mid-loop that is exactly the state we are in.
        // PUBLISHING is set once, after the loop, when every page qualifies.
        val updated = project.copy(
            revision = project.revision + 1,
            pages = project.pages.map { page ->
                if (page.pageId != pageId) {
                    page
                } else {
                    page.copy(
                        renderedOutput = output,
                        confirmedMedia = ConfirmedMedia(
                            mediaId = mediaId,
                            processingState = "ready",
                            moderationState = "passed",
                            boundOutputSha256 = output.sha256,
                        ),
                    )
                }
            },
        )
        store.save(updated, now = System.currentTimeMillis())
        return updated
    }

    /** An unresolved operation for this project, to be replayed verbatim. */
    private suspend fun existingLiveOperation(projectId: String): CreatorPublishOperationEntity? {
        val dao = db.creatorPublishOperationDao()
        return dao.liveSlot(projectId)?.let { dao.load(it.operationId) }
    }

    /**
     * Freeze a NEW operation: real DTO bytes, one creation key, the live slot
     * claimed. From this point the request may only ever be replayed verbatim.
     */
    private suspend fun freezeOperation(
        project: AndroidCreatorProject,
    ): CreatorPublishOperationEntity {
        val mediaIds = project.pages.map { it.confirmedMedia!!.mediaId }
        val bytes = transport.freezeCreateRequest(
            text = project.postText.value,
            language = project.postText.language,
            postType = if (mediaIds.isEmpty()) "text" else "image",
            mediaIds = mediaIds,
        )
        val operation = CreatorPublishOperationEntity(
            operationId = newUlid(),
            projectId = project.projectId,
            boundRevision = project.revision,
            projectDocumentSha256 = Canonical.fingerprint(project),
            orderedOutputSha256 = jsonArrayOf(project.pages.mapNotNull { it.renderedOutput?.sha256 }),
            orderedMediaIds = jsonArrayOf(mediaIds),
            creationKey = UUID.randomUUID().toString(),
            frozenRequestBase64 = Base64.getEncoder().encodeToString(bytes),
            frozenRequestSha256 = Canonical.sha256Hex(bytes),
            frozenRequestBytes = bytes.size,
            state = "publishing",
            createdAtMillis = System.currentTimeMillis(),
            updatedAtMillis = System.currentTimeMillis(),
        )
        db.creatorPublishOperationDao().startOperation(operation)
        return operation
    }

    private suspend fun complete(
        project: AndroidCreatorProject,
        operation: CreatorPublishOperationEntity,
        postId: String,
    ): PublishResult {
        db.creatorPublishOperationDao().resolveOperation(
            operationId = operation.operationId,
            projectId = operation.projectId,
            state = "published",
            serverPostId = postId,
            now = System.currentTimeMillis(),
        )
        store.save(
            project.copy(
                revision = project.revision + 1,
                status = AndroidCreatorProject.STATUS_PUBLISHED,
            ),
            now = System.currentTimeMillis(),
        )
        return PublishResult.Published(postId)
    }

    /**
     * A failed attempt keeps the operation LIVE (`failed` holds the slot): the
     * same key and bytes replay on retry, and only explicit supersession or
     * resolution ever frees the project for a different request.
     */
    private suspend fun markFailed(operation: CreatorPublishOperationEntity, reason: String) {
        db.creatorPublishOperationDao().failOperation(
            operationId = operation.operationId,
            errorCode = reason.take(MAX_ERROR_CHARS),
            now = System.currentTimeMillis(),
        )
    }

    private fun writeAtomically(target: File, bytes: ByteArray) {
        val temp = File(target.parentFile, "${target.name}.tmp")
        temp.writeBytes(bytes)
        if (!temp.renameTo(target)) temp.delete()
    }

    private fun jsonArrayOf(values: List<String>): String =
        Json.encodeToString(
            kotlinx.serialization.json.JsonArray.serializer(),
            buildJsonArray { values.forEach { add(it) } },
        )

    private fun newUlid(): String {
        val alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
        val random = SecureRandom()
        return buildString(ULID_LENGTH) {
            repeat(ULID_LENGTH) { append(alphabet[random.nextInt(alphabet.length)]) }
        }
    }

    private companion object {
        const val ULID_LENGTH = 26
        const val MAX_ERROR_CHARS = 64
        const val OUTPUT_NAME_HASH_CHARS = 12
    }
}
