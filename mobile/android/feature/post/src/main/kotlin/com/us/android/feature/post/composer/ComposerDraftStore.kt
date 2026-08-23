package com.us.android.feature.post.composer

import com.us.android.core.database.ComposerDraftDao
import com.us.android.core.database.ComposerDraftEntity
import com.us.android.feature.post.data.dto.CreatePostRequest
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The DURABLE authority for an unpublished draft — Slice C, C-P0-3.
 *
 * The adapter between the composer's product model and `:core:database`'s Room
 * mechanics, so the feature owns the meaning and the core module owns the
 * storage. `:core:database` does not know what a "frozen publish" is; this class
 * does not know what a Room migration is.
 *
 * `SavedStateHandle` remains in the ViewModel as a fast in-memory mirror for
 * rotation. It is NOT the authority: it dies with the navigation back-stack
 * entry, so a plain Back would otherwise discard the draft and — worse — the
 * creation key that stops a retry publishing twice.
 */
@Singleton
class ComposerDraftStore @Inject constructor(
    private val dao: ComposerDraftDao,
    private val json: Json,
) {

    /**
     * Loads the stored draft, or null.
     *
     * A frozen operation is restored ONLY when both halves survived. A key
     * without its request bytes cannot be safely retried: the retry would have
     * to rebuild the request, and a rebuild that differs at all is refused by
     * the server as key reuse. Half a frozen operation is worse than none, so
     * the partial case is dropped and the next Post mints a fresh key.
     */
    suspend fun load(): ComposerUiState? {
        val row = dao.load() ?: return null

        val frozen = row.creationKey?.let { key ->
            row.frozenRequestJson
                ?.let { runCatching { json.decodeFromString<CreatePostRequest>(it) }.getOrNull() }
                ?.let { FrozenPublish(creationKey = key, request = it) }
        }

        return ComposerUiState(
            text = row.text,
            imageUri = row.imageUri,
            altText = row.altText,
            decorative = row.decorative,
            language = row.language,
            mediaId = row.mediaId,
            // Never restored into an in-flight phase: the upload and the publish
            // died with the process, and showing "uploading" for a job that no
            // longer exists is a lie the user cannot act on. The ViewModel
            // decides what to resume from the durable facts.
            phase = ComposerPhase.Editing,
            frozen = frozen,
            restoredFromDraft = true,
        )
    }

    /** Writes the draft. Called on every meaningful change. */
    suspend fun save(state: ComposerUiState, nowMillis: Long) {
        dao.save(
            ComposerDraftEntity(
                text = state.text,
                imageUri = state.imageUri,
                altText = state.altText,
                decorative = state.decorative,
                language = state.language,
                mediaId = state.mediaId,
                creationKey = state.frozen?.creationKey,
                frozenRequestJson = state.frozen?.let { json.encodeToString(it.request) },
                updatedAtMillis = nowMillis,
            ),
        )
    }

    /**
     * Removes the draft.
     *
     * Called on publish success and on explicit discard — the only two outcomes
     * where the user is finished with it. Never on a failure: a failed publish
     * is exactly when the draft matters most.
     */
    suspend fun clear() = dao.clear()
}
