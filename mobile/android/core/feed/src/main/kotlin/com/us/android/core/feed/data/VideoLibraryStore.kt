package com.us.android.core.feed.data

import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/** The viewer's Watch later and dislike state for the posts touched this session, layered over the server's. */
data class VideoLibraryState(
    val queued: Map<String, Boolean> = emptyMap(),
    val disliked: Map<String, Boolean> = emptyMap(),
) {
    /** Local value when there is one, the row's otherwise. */
    fun queuedOr(postId: String, server: Boolean): Boolean = queued[postId] ?: server
    fun dislikedOr(postId: String, server: Boolean): Boolean = disliked[postId] ?: server
}

/**
 * Optimistic Watch later and dislike state, shared by every Tube surface
 * (2026-10-02).
 *
 * A singleton for the reason `EngagementStore` is: the watch screen, the
 * Watch later list and a card's row must agree the instant one of them
 * changes, and a video reopened from a list row that predates the tap must
 * still show what the viewer did.
 *
 * A tap moves the shown value at once; the write follows. Writes are
 * serialized, so "add" then "remove" tapped quickly reach the server in that
 * order. A refused write puts the shown value back to the last one the
 * server acknowledged (or the row's own value when it never has), unless the
 * viewer has asked for something else since. Both routes are idempotent on
 * the server, so a retry after a lost answer cannot double-apply.
 */
@Singleton
class VideoLibraryStore @Inject constructor(
    private val writes: VideoLibraryWrites,
) {
    private val _state = MutableStateFlow(VideoLibraryState())
    val state: StateFlow<VideoLibraryState> = _state.asStateFlow()

    private val lock = Mutex()

    /** The last value the server acknowledged, per lane and post. Guarded by [lock]. */
    private val confirmedQueued = mutableMapOf<String, Boolean>()
    private val confirmedDisliked = mutableMapOf<String, Boolean>()

    /** Writes asked for and not yet answered, per lane and post; [adopt] leaves those posts alone. */
    private val pending = MutableStateFlow<Map<String, Int>>(emptyMap())

    private var viewerId: String? = null

    @Volatile
    private var generation: Long = 0

    /**
     * Binds the store to the signed-in viewer, clearing everything on a
     * change: this is private per-viewer state keyed by post id alone.
     */
    fun setViewer(newViewerId: String?) {
        if (newViewerId == viewerId) return
        viewerId = newViewerId
        generation += 1
        _state.value = VideoLibraryState()
        pending.value = emptyMap()
        confirmedQueued.clear()
        confirmedDisliked.clear()
    }

    /** [serverQueued] is what the row says; it is what a refused first write rolls back to. */
    suspend fun setQueued(postId: String, queued: Boolean, serverQueued: Boolean): AppResult<Unit> =
        write(
            key = "q:$postId",
            show = { value -> _state.update { it.copy(queued = it.queued.with(postId, value)) } },
            shown = { _state.value.queued[postId] },
            confirmed = confirmedQueued,
            postId = postId,
            target = queued,
            server = serverQueued,
        ) { writes.setWatchLater(postId, queued) }

    suspend fun setDisliked(postId: String, disliked: Boolean, serverDisliked: Boolean): AppResult<Unit> =
        write(
            key = "d:$postId",
            show = { value -> _state.update { it.copy(disliked = it.disliked.with(postId, value)) } },
            shown = { _state.value.disliked[postId] },
            confirmed = confirmedDisliked,
            postId = postId,
            target = disliked,
            server = serverDisliked,
        ) { writes.setDisliked(postId, disliked) }

    /**
     * The post detail arrived: its values replace the local ones, unless a
     * write for that post is still on the wire (the detail was read before
     * the write landed, so it does not describe it).
     */
    fun adopt(postId: String, serverQueued: Boolean, serverDisliked: Boolean) {
        val busy = pending.value
        _state.update { current ->
            current.copy(
                queued = if ("q:$postId" in busy) current.queued else current.queued.with(postId, serverQueued),
                disliked = if ("d:$postId" in busy) {
                    current.disliked
                } else {
                    current.disliked.with(postId, serverDisliked)
                },
            )
        }
    }

    @Suppress("LongParameterList") // One lane's accessors; the two lanes share every rule.
    private suspend fun write(
        key: String,
        show: (Boolean) -> Unit,
        shown: () -> Boolean?,
        confirmed: MutableMap<String, Boolean>,
        postId: String,
        target: Boolean,
        server: Boolean,
        call: suspend () -> AppResult<Unit>,
    ): AppResult<Unit> {
        val started = generation
        show(target)
        pending.update { it + (key to (it[key] ?: 0) + 1) }
        return try {
            lock.withLock {
                val result = call()
                // A viewer change while the write was out: nothing of it lands on the next account.
                if (started != generation) return@withLock result
                when (result) {
                    is AppResult.Success -> confirmed[postId] = target
                    // Rolled back only while this is still what the viewer last asked for.
                    is AppResult.Failure -> if (shown() == target) show(confirmed[postId] ?: server)
                }
                result
            }
        } finally {
            pending.update { counts ->
                val left = (counts[key] ?: 1) - 1
                if (left <= 0) counts - key else counts + (key to left)
            }
        }
    }

    private fun Map<String, Boolean>.with(postId: String, value: Boolean): Map<String, Boolean> =
        this + (postId to value)
}
