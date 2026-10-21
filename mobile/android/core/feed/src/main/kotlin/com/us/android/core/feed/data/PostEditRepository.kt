package com.us.android.core.feed.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FeedItem
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The owner's saved edits this session (2026-10-21), by post id: what every
 * list layers over its rows so a saved title or description is what Reels,
 * the watch screen and the More sheet show at once — no refetch, and no row
 * that still says the old words until the next pull. The same shape as
 * `HiddenPosts`: process-wide, in memory, and gone with the process, when
 * the server's own rows already carry the edit.
 */
@Singleton
class PostEdits @Inject constructor() {

    private val _edits = MutableStateFlow<Map<String, PostEditPatch>>(emptyMap())
    val edits: StateFlow<Map<String, PostEditPatch>> = _edits.asStateFlow()

    /** A save landed: it is folded over any earlier one for the same post. */
    fun record(postId: String, patch: PostEditPatch) {
        if (patch.isEmpty) return
        _edits.update { all -> all + (postId to (all[postId]?.then(patch) ?: patch)) }
    }
}

/** [item] as this session's edits have left it. */
fun Map<String, PostEditPatch>.applyTo(item: FeedItem): FeedItem = this[item.id]?.let(item::withEdits) ?: item

/** Reads the post's owner fields for the Edit sheet, and sends the save. */
@Singleton
class PostEditRepository @Inject constructor(
    private val api: PostEditApi,
    private val errorMapper: ErrorMapper,
    private val edits: PostEdits,
) {

    suspend fun detail(postId: String): AppResult<PostEditDetailDto> = apiCall(errorMapper) { api.detail(postId) }

    /** Sends [patch]; on success every list learns it through [PostEdits]. */
    suspend fun save(postId: String, patch: PostEditPatch): AppResult<PostEditDetailDto> {
        val result = apiCall(errorMapper) { api.update(postId, patch) }
        if (result is AppResult.Success) edits.record(postId, patch)
        return result
    }
}
