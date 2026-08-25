package com.us.android.feature.post.composer

import com.us.android.feature.post.data.dto.CreatePostRequest
import kotlinx.serialization.Serializable

/**
 * The draft as it is written to durable storage.
 *
 * A separate type from [ComposerUiState] deliberately. UI state carries
 * transient things — validation flags, a discard dialog, a progress fraction —
 * that are meaningless after a restart and would be actively wrong to restore:
 * a composer that comes back showing "uploading, 40%" for an upload that died
 * with the process is a lie.
 *
 * What IS persisted is exactly what the user would lose and cannot retype: the
 * text, which image they chose, their accessibility decision, their language,
 * the confirmed media id, and the frozen publish operation.
 *
 * ## THE FROZEN OPERATION IS THE IMPORTANT PART
 *
 * If the process dies between "the server committed the post" and "the client
 * saw the response", the ONLY thing that prevents a duplicate on the next
 * attempt is retrying with the same creation key and the same bytes. Both are
 * here. Losing them is not a cosmetic regression — it is a duplicated post.
 */
@Serializable
data class PersistedDraft(
    val text: String = "",
    val imageUri: String? = null,
    val altText: String = "",
    val decorative: Boolean = false,
    val language: String = DEFAULT_LANGUAGE,
    /** A confirmed asset. Present means "reuse this", never "upload again". */
    val mediaId: String? = null,
    val creationKey: String? = null,
    val frozenRequest: CreatePostRequest? = null,
)

internal fun ComposerUiState.toPersisted() = PersistedDraft(
    text = text,
    imageUri = imageUri,
    altText = altText,
    decorative = decorative,
    language = language,
    mediaId = mediaId,
    creationKey = frozen?.creationKey,
    frozenRequest = frozen?.request,
)

/**
 * Restores into [ComposerPhase.Editing], never into an in-flight phase.
 *
 * A restored composer is not uploading and not publishing — those jobs died
 * with the process. Coming back as `Editing` with the draft intact is the
 * truthful state, and the user decides whether to press Post again. That press
 * reuses the frozen key, so a publish that DID commit before the crash replays
 * instead of duplicating.
 */
internal fun PersistedDraft.toState() = ComposerUiState(
    text = text,
    imageUri = imageUri,
    altText = altText,
    decorative = decorative,
    language = language,
    mediaId = mediaId,
    phase = ComposerPhase.Editing,
    frozen = if (creationKey != null && frozenRequest != null) {
        FrozenPublish(creationKey, frozenRequest)
    } else {
        null
    },
)
