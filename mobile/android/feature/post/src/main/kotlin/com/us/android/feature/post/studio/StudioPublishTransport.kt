package com.us.android.feature.post.studio

import com.us.android.core.common.result.AppResult
import com.us.android.core.creator.model.CreateOutcome
import com.us.android.core.creator.model.PublishTransport
import com.us.android.core.creator.model.UploadOutcome
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.UploadSource
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import kotlinx.coroutines.delay
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The production [PublishTransport] — Slice C's proven pipeline behind the port.
 *
 * ## WHY THIS FILE LIVES NEXT TO THE DTO
 *
 * The frozen request bytes MUST come from the real [CreatePostRequest] — the
 * errata's provenance rule exists because a hand-written JSON drifted from the
 * DTO once. This adapter is in the one module that owns that DTO and its
 * exact-wire tests, so freezing and replaying can never disagree with what the
 * legacy composer sends.
 *
 * ## HOW REPLAY STAYS BYTE-STABLE
 *
 * [createPost] decodes the frozen bytes back into the DTO and sends it through
 * the SAME repository call site the composer uses (guard G-7: one create call
 * site). That is Slice C's own replay semantic: byte stability comes from the
 * DTO's deterministic serialization — declaration order, `@EncodeDefault`
 * ALWAYS — which `CreatePostWireTest` pins. The decode is strict; bytes that no
 * longer parse as this DTO version are refused as permanent rather than sent
 * as a guess.
 */
@Singleton
class StudioPublishTransport @Inject constructor(
    private val uploader: MediaUploader,
    private val repository: ComposerRepository,
) : PublishTransport {

    private val json = Json { ignoreUnknownKeys = false }

    override fun freezeCreateRequest(
        text: String,
        language: String,
        postType: String,
        mediaIds: List<String>,
    ): ByteArray {
        val request = CreatePostRequest(
            text = text,
            postType = postType,
            mediaIds = mediaIds,
            language = language,
            distribution = DistributionRequest(),
        )
        return json.encodeToString(CreatePostRequest.serializer(), request)
            .toByteArray(Charsets.UTF_8)
    }

    /**
     * The exact Slice C sequence for one rendered page:
     * reserve → presigned PUT → confirm → poll for EXACT ready+passed → alt.
     */
    // ReturnCount: the pipeline is five sequential server steps, each with its
    // own typed refusal. Folding them into one exit would trade a readable
    // failure ladder for a nested conditional pyramid.
    @Suppress("ReturnCount")
    override suspend fun uploadPage(
        bytes: ByteArray,
        mime: String,
        altText: String,
        decorative: Boolean,
    ): UploadOutcome {
        val init = when (val reserved = uploader.reserve(mime, bytes.size.toLong())) {
            is AppResult.Failure -> return UploadOutcome.Retryable("reserve failed")
            is AppResult.Success -> reserved.data
        }

        val put = uploader.upload(
            uploadUrl = init.uploadUrl,
            mimeType = mime,
            sizeBytes = bytes.size.toLong(),
            source = UploadSource { bytes.inputStream() },
            onProgress = { _, _ -> },
        )
        if (put !is PresignedPutResult.Success) {
            return UploadOutcome.Retryable("upload PUT failed")
        }

        if (uploader.confirm(init.mediaId) is AppResult.Failure) {
            return UploadOutcome.Retryable("confirm failed")
        }

        when (val readiness = awaitReady(init.mediaId)) {
            Readiness.Ready -> Unit
            Readiness.TimedOut -> return UploadOutcome.Retryable("processing timed out")
            is Readiness.Rejected -> return UploadOutcome.Permanent(readiness.reason)
        }

        // The FINAL accessibility decision, written before the asset can be
        // attached. Alt text is optional: when the user made no decision at
        // all (no text, not marked decorative) the server-side placeholder
        // already says exactly that, so there is nothing to write — but a
        // decision that WAS made must land, or the post ships wrong.
        val decisionMade = altText.isNotBlank() || decorative
        if (decisionMade && !uploader.updateAccessibility(init.mediaId, altText, decorative)) {
            return UploadOutcome.Retryable("accessibility update failed")
        }

        return UploadOutcome.Confirmed(init.mediaId)
    }

    override suspend fun createPost(creationKey: String, frozenRequest: ByteArray): CreateOutcome {
        // Strict decode of OUR OWN frozen bytes. Failure means the bytes are
        // from an incompatible DTO version, and sending a reconstruction would
        // be a different request under the same idempotency key.
        val request = runCatching {
            json.decodeFromString(CreatePostRequest.serializer(), frozenRequest.decodeToString())
        }.getOrNull() ?: return CreateOutcome.Permanent("frozen request no longer decodes")

        return when (val result = repository.createPost(creationKey, request)) {
            is AppResult.Success -> CreateOutcome.Created(result.data)
            is AppResult.Failure ->
                if (repository.isTerminal(result.error)) {
                    CreateOutcome.Permanent(result.error.javaClass.simpleName)
                } else {
                    CreateOutcome.Retryable(result.error.javaClass.simpleName)
                }
        }
    }

    // ------------------------------------------------------------------

    private sealed interface Readiness {
        data object Ready : Readiness
        data object TimedOut : Readiness
        data class Rejected(val reason: String) : Readiness
    }

    /** The composer's exact readiness discipline: EXACT ready+passed, bounded. */
    private suspend fun awaitReady(mediaId: String): Readiness {
        repeat(READINESS_POLLS) { attempt ->
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> return Readiness.TimedOut
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (processing == "rejected" || processing == "failed" ||
                        moderation == "rejected"
                    ) {
                        return Readiness.Rejected("media was rejected ($processing/$moderation)")
                    }
                    if (processing == "ready" && moderation == "passed") return Readiness.Ready
                }
            }
            if (attempt < READINESS_POLLS - 1) delay(READINESS_POLL_MILLIS)
        }
        return Readiness.TimedOut
    }

    private companion object {
        const val READINESS_POLLS = 30
        const val READINESS_POLL_MILLIS = 1_000L
    }
}
