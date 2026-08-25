package com.us.android.core.creator.engine

import com.us.android.core.creator.model.Canonical
import com.us.android.core.database.CreatorLegacyRecoveryEntity
import com.us.android.core.database.CreatorMigrationStagingEntity
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * What a staged legacy draft should become.
 *
 * ## THE ORDER OF THESE CHECKS IS THE WHOLE POINT
 *
 * An earlier design asked "can we still read the image?" first. That is the
 * wrong first question, and it is wrong in a way that costs a user a duplicate
 * post.
 *
 * A draft can hold BOTH a readable `imageUri` AND a `creationKey` +
 * `frozenRequestJson` — that is exactly what a publish looks like when the
 * server committed it and the response was lost on the way back. Answering
 * "yes, readable" turns that row into an editable project and throws the frozen
 * operation away. The next publish then mints a fresh idempotency key, the
 * server has no reason to deduplicate it, and the user's post appears twice.
 *
 * So operation authority outranks source availability, always. A copied source
 * may be kept as recovery MATERIAL, but it never replaces the operation.
 */
sealed interface AdoptionOutcome {

    /** Quarantine. Half a frozen operation cannot be retried or discarded safely. */
    data class Quarantine(val reason: String) : AdoptionOutcome

    /**
     * Retry the exact bytes under the existing key.
     *
     * No project, no editing, no re-upload, no re-render. The one action is to
     * replay a request the server may already have committed, and let its
     * idempotency authority answer.
     */
    data class RetryablePublish(
        val creationKey: String,
        val frozenRequestJson: String,
        val frozenRequestSha: String,
        val frozenRequestLen: Int,
        /** Null for a legacy TEXT post — see [verifyFrozenRequest]. */
        val mediaId: String?,
    ) : AdoptionOutcome

    /** No operation and a usable source: this may become a real editable project. */
    data object AdoptAsProject : AdoptionOutcome

    /** No operation, no usable source, but text worth keeping. */
    data object TextOnlyRecovery : AdoptionOutcome

    /** A confirmed remote asset with nothing local. Not editable in v1. */
    data object UnusableRecovery : AdoptionOutcome
}

object LegacyAdoption {

    private val json = Json { ignoreUnknownKeys = true }

    /**
     * Decide what a staged row becomes.
     *
     * @param sourceReadable whether the legacy `content://` URI still resolves.
     *   Deliberately the LAST thing consulted.
     */
    fun decide(
        staging: CreatorMigrationStagingEntity,
        sourceReadable: Boolean,
    ): AdoptionOutcome {
        // 1. A half-frozen operation is never interpreted, under any condition.
        if (staging.classification == CreatorMigrationStagingEntity.CLASSIFICATION_HALF_FROZEN) {
            return AdoptionOutcome.Quarantine("HALF_FROZEN_OPERATION")
        }

        val key = staging.creationKey
        val frozen = staging.frozenRequestJson

        // 2. Operation authority, BEFORE source availability.
        if (key != null && frozen != null) {
            val bytes = frozen.toByteArray(Charsets.UTF_8)
            return AdoptionOutcome.RetryablePublish(
                creationKey = key,
                frozenRequestJson = frozen,
                frozenRequestSha = Canonical.sha256Hex(bytes),
                frozenRequestLen = bytes.size,
                mediaId = staging.mediaId,
            )
        }

        // 3. Only now does the source matter — and "never had one" is not the
        // same as "had one and lost it". A draft with no imageUri at all is the
        // fixture-1 text-only shape and adopts directly: there is nothing to
        // lose and nothing to invent. Recovery kinds exist for drafts whose
        // image EXISTED and cannot be honestly reproduced.
        val hadSource = staging.imageUri != null
        return when {
            sourceReadable -> AdoptionOutcome.AdoptAsProject
            !hadSource && staging.mediaId != null -> AdoptionOutcome.UnusableRecovery
            !hadSource && staging.text.isNotBlank() -> AdoptionOutcome.AdoptAsProject
            hadSource && staging.mediaId != null -> AdoptionOutcome.UnusableRecovery
            hadSource && staging.text.isNotBlank() -> AdoptionOutcome.TextOnlyRecovery
            else -> AdoptionOutcome.Quarantine("NOTHING_RECOVERABLE")
        }
    }

    /**
     * The gate that runs BEFORE any network call — approval R-3.
     *
     * ## WHY EVERY BRANCH HERE REFUSES RATHER THAN REPAIRS
     *
     * These bytes are about to be replayed under an idempotency key the server
     * may already have honoured. If they are not byte-identical to what was
     * originally sent, the replay is not a retry — it is a new post wearing an
     * old key, which the server will either reject as reuse or, if the key has
     * aged out, accept as a second post.
     *
     * There is no safe repair for that, so every mismatch quarantines and no
     * request is made.
     */
    fun verifyFrozenRequest(
        recovery: CreatorLegacyRecoveryEntity,
    ): FrozenRequestVerdict {
        val frozen = recovery.frozenRequestJson
            ?: return FrozenRequestVerdict.Quarantine("no frozen request")
        val bytes = frozen.toByteArray(Charsets.UTF_8)

        if (recovery.frozenRequestLen != bytes.size) {
            return FrozenRequestVerdict.Quarantine(
                "length ${recovery.frozenRequestLen} does not match ${bytes.size} stored bytes",
            )
        }
        if (recovery.frozenRequestSha != Canonical.sha256Hex(bytes)) {
            return FrozenRequestVerdict.Quarantine("sha256 does not match the stored bytes")
        }

        // Parsed, never re-serialized. Re-encoding to inspect it is how the
        // bytes would drift from what the server hashed.
        // The WHOLE structural read is fallible, not just the parse.
        //
        // The previous version caught only `parseToJsonElement` and the cast to
        // JsonObject. Everything after it — `jsonPrimitive`, `jsonArray`, and the
        // per-element conversions — throws on valid JSON with an unexpected
        // shape. A persisted payload from an older or broken client would then
        // take down the recovery route itself, which is the one path that exists
        // to rescue that exact user. It must quarantine, never throw.
        val extracted = extractFrozenFields(frozen)
            ?: return FrozenRequestVerdict.Quarantine("frozen request is not a readable v1 request")

        val (postType, mediaIds) = extracted
        val mediaId = recovery.mediaId

        return when {
            // A legacy TEXT publish. Fixture 4c proves this is a real shape: the
            // composer could freeze a text post with no media at all.
            mediaId == null && postType == POST_TYPE_TEXT && mediaIds.isEmpty() ->
                FrozenRequestVerdict.Retry

            mediaId == null ->
                FrozenRequestVerdict.Quarantine(
                    "no media id, but the request is post_type=$postType with ${mediaIds.size} media",
                )

            postType == POST_TYPE_IMAGE && mediaIds == listOf(mediaId) ->
                FrozenRequestVerdict.Retry

            else ->
                FrozenRequestVerdict.Quarantine(
                    "media id $mediaId does not match post_type=$postType media_ids=$mediaIds",
                )
        }
    }

    /**
     * Read `post_type` and `media_ids` out of a frozen request, or null.
     *
     * ## REQUIRED-FIELD PRESENCE IS PART OF THE CONTRACT
     *
     * `media_ids` ABSENT is not the same as `media_ids: []`, and treating it as
     * an empty list is what let a malformed payload pass the text-retry branch.
     * Approval R-3 requires the field to be present as an array. The real
     * `CreatePostRequest` always emits it — `@EncodeDefault(ALWAYS)` on
     * `mediaIds` — so an absent field means these bytes were not produced by a
     * client this build can reason about, and replaying them would be a guess.
     *
     * Every extraction here is total: each step either produces a value of the
     * required shape or returns null. Nothing throws.
     */
    private fun extractFrozenFields(frozen: String): Pair<String, List<String>>? {
        val root = runCatching { json.parseToJsonElement(frozen) }.getOrNull() as? JsonObject
            ?: return null

        val postType = (root[FIELD_POST_TYPE] as? JsonPrimitive)
            ?.takeIf { it.isString }
            ?.content
            ?: return null

        // Present, and an array. Absent, null, an object or a scalar all fail.
        val mediaArray = root[FIELD_MEDIA_IDS] as? JsonArray ?: return null

        val mediaIds = mediaArray.map { element ->
            (element as? JsonPrimitive)?.takeIf { it.isString }?.content ?: return null
        }

        return postType to mediaIds
    }

    private const val FIELD_POST_TYPE = "post_type"
    private const val FIELD_MEDIA_IDS = "media_ids"

    const val POST_TYPE_TEXT = "text"
    const val POST_TYPE_IMAGE = "image"
}

sealed interface FrozenRequestVerdict {
    /** The bytes are exactly what they claim. Replay them under the existing key. */
    data object Retry : FrozenRequestVerdict

    /** Something does not line up. Quarantine, and make NO network call. */
    data class Quarantine(val reason: String) : FrozenRequestVerdict
}
