package com.us.android.core.creator.model

/**
 * The publish transport PORT — how the engine reaches the proven Slice C
 * media/create pipeline without depending on the modules that implement it.
 *
 * ## WHY THE ENGINE DOES NOT CALL THE NETWORK ITSELF
 *
 * The hardened upload/create machinery lives in `:core:media` and
 * `:feature:post` (the real `CreatePostRequest` DTO and its exact-wire tests).
 * The engine may depend on neither (guards G-4/G-6), so the port lives here in
 * the pure model — exactly like [RenderExporter] — and the adapter that
 * implements it sits next to the DTO it protects. Guard G-7 still holds: the
 * adapter routes through the ONE existing create-post call site.
 *
 * ## WHY [freezeCreateRequest] IS PART OF THE PORT
 *
 * The frozen request bytes are the server's idempotency authority and MUST be
 * produced by the real kotlinx DTO — the errata's provenance rule exists
 * because a hand-built JSON drifted from the DTO once already. The engine
 * therefore never builds request bytes; it asks the adapter to freeze them
 * once, stores them opaquely in the operation row, and replays those exact
 * bytes on every retry.
 */
interface PublishTransport {

    /**
     * Serialize the REAL create-post request for these inputs.
     *
     * Called exactly once per operation, at freeze time. The returned bytes
     * are stored and replayed verbatim; the engine never re-invokes this for
     * a retry.
     */
    fun freezeCreateRequest(
        text: String,
        language: String,
        postType: String,
        mediaIds: List<String>,
    ): ByteArray

    /**
     * The full Slice C media pipeline for ONE rendered page:
     * reserve → PUT → confirm → await exactly ready+passed → set alt/decorative.
     *
     * Returns the confirmed media id, or a typed failure. Implementations must
     * not retry internally past their existing Slice C behaviour — retry
     * policy belongs to the caller, which owns the durable checkpoints.
     */
    suspend fun uploadPage(
        bytes: ByteArray,
        mime: String,
        altText: String,
        decorative: Boolean,
    ): UploadOutcome

    /**
     * `POST /v1/posts` with the given idempotency key and the EXACT frozen
     * bytes. The single production call site behind this is the Slice C
     * repository — G-7.
     */
    suspend fun createPost(creationKey: String, frozenRequest: ByteArray): CreateOutcome
}

sealed interface UploadOutcome {
    data class Confirmed(val mediaId: String) : UploadOutcome

    /** Network flaked, server 5xx'd, moderation still pending past patience. */
    data class Retryable(val reason: String) : UploadOutcome

    /** Moderation rejected, unsupported media — retrying cannot change it. */
    data class Permanent(val reason: String) : UploadOutcome
}

sealed interface CreateOutcome {
    data class Created(val postId: String) : CreateOutcome

    /**
     * The server says this key already produced a post — the lost-response
     * case. The publish SUCCEEDED earlier; the caller records [postId] and
     * cleans up exactly as for [Created].
     */
    data class AlreadyCreated(val postId: String) : CreateOutcome

    data class Retryable(val reason: String) : CreateOutcome

    data class Permanent(val reason: String) : CreateOutcome
}
