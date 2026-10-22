package com.us.android.core.feed.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FeedItem
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.noContentApiCall
import com.us.android.core.network.pagedApiCall
import javax.inject.Inject
import javax.inject.Singleton

/** One video in History: the post, where the viewer left it, and when. */
data class WatchHistoryEntry(
    val item: FeedItem,
    val positionMs: Long,
    val durationMs: Long,
    /** Watched to the end ("Watched" on the row instead of "4:10 of 12:00"). */
    val completed: Boolean,
    /** The row's `updated_at`: when it was last watched. */
    val watchedAt: String,
) {
    val postId: String get() = item.id

    /** 0..1 for the bar under the thumbnail; a finished video is full, an unknown length empty. */
    val fraction: Float
        get() = when {
            completed -> 1f
            durationMs <= 0L -> 0f
            else -> (positionMs.toFloat() / durationMs).coerceIn(0f, 1f)
        }
}

/**
 * What the History page reads and writes. A seam so the page's ViewModel is
 * tested against answers a test controls; [WatchHistoryRepository] is the
 * real one.
 */
interface WatchHistorySource {
    /** One page, most recent first; [cursor] null is the first page. */
    suspend fun page(cursor: String?): AppResult<Paged<WatchHistoryEntry>>

    /** One video out of History. */
    suspend fun remove(postId: String): AppResult<Unit>

    /** Every video out of History. */
    suspend fun clear(): AppResult<Unit>
}

/**
 * Tube's History (web parity, 2026-10-22), over [WatchHistoryApi]. Each page
 * is hydrated as one batch — the author, the delivery and the chosen cover,
 * the way continue-watching is — and a row whose post the viewer may no
 * longer see is dropped (the server drops most of them already).
 */
@Singleton
class WatchHistoryRepository @Inject constructor(
    private val api: WatchHistoryApi,
    private val errorMapper: ErrorMapper,
    private val hydrator: FeedItemHydrator,
) : WatchHistorySource {

    override suspend fun page(cursor: String?): AppResult<Paged<WatchHistoryEntry>> =
        when (val result = pagedApiCall(errorMapper) { api.history(PAGE_SIZE, cursor) }) {
            is AppResult.Failure -> result
            is AppResult.Success -> {
                val rows = result.data.items
                    .filter { row -> row.postId.isNotBlank() && row.post?.let { it.deletedAt.isBlank() } == true }
                    .distinctBy { it.postId }
                // Hydrate keeps order and count, so the two lists zip.
                val items = hydrator.hydrate(rows.map { requireNotNull(it.post).toDomain() })
                AppResult.Success(
                    Paged(
                        items = rows.zip(items) { row, item ->
                            WatchHistoryEntry(
                                item = item,
                                positionMs = row.positionMs.coerceAtLeast(0L),
                                durationMs = row.durationMs.coerceAtLeast(0L),
                                completed = row.completed,
                                watchedAt = row.updatedAt,
                            )
                        },
                        nextCursor = result.data.nextCursor?.takeIf { it.isNotBlank() },
                    ),
                )
            }
        }

    override suspend fun remove(postId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.remove(postId) }

    override suspend fun clear(): AppResult<Unit> = noContentApiCall(errorMapper) { api.clear() }

    companion object {
        /** The web reads 30 a page; the server clamps at 100. */
        const val PAGE_SIZE = 30
    }
}

/**
 * History's search, over the rows already loaded (the web's `filterRecent`):
 * case-insensitive on the title, the text, the channel's name and handle and
 * the author's name and handle. A blank query keeps every row.
 */
fun filterHistory(rows: List<WatchHistoryEntry>, query: String): List<WatchHistoryEntry> {
    val needle = query.trim().lowercase()
    if (needle.isEmpty()) return rows
    return rows.filter { entry ->
        val item = entry.item
        listOfNotNull(
            item.title,
            item.text,
            item.channel?.name,
            item.channel?.handle,
            item.author.displayName,
            item.author.username,
        ).filter { it.isNotBlank() }.joinToString(" ").lowercase().contains(needle)
    }
}
