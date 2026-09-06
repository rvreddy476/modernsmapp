package com.us.android.core.media.publish

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Where a background reel publish has got to — the state a pending tile
 * renders and the publish worker writes.
 *
 * Product-neutral on purpose: this module knows how to upload and play
 * media, not what a reel's form contains. The pending publish itself (the
 * video, the caption, the switches) lives with the worker in `:feature:post`;
 * only its progress crosses into a module that `:feature:feed` and
 * `:feature:profile` may also see, because features must not depend on each
 * other.
 */
sealed interface ReelPublishState {
    /** Nothing is posting and nothing is waiting to be seen. */
    data object Idle : ReelPublishState

    /** The picked video is being copied into app storage. */
    data object Preparing : ReelPublishState

    /** Bytes are leaving the device; [fraction] is 0..1 of the video. */
    data class Uploading(val fraction: Float) : ReelPublishState

    /**
     * The server is transcoding and moderating the video BEFORE the post can
     * exist. Since instant reels this is the fallback only: a server that
     * still refuses a confirmed-but-unready video is polled until it takes it.
     */
    data object Processing : ReelPublishState

    /** The cover is going up and the post is being created. */
    data object Posting : ReelPublishState

    /**
     * Done. The pending tile swaps for the real post — or, when the post was
     * given a [publishAt] in the future, for a scheduled tile that waits for it.
     */
    data class Published(val postId: String, val publishAt: String? = null) : ReelPublishState

    /**
     * Stopped. [retryable] decides whether the pending item offers "Retry";
     * [needsChannel] is the one refusal with a different way out — the
     * server wants a channel before a long video (`CHANNEL_REQUIRED`), so
     * the pending item offers "Create channel" and retries once there is one.
     */
    data class Failed(
        val message: String,
        val retryable: Boolean,
        val needsChannel: Boolean = false,
    ) : ReelPublishState

    /** Work is in flight. */
    val isActive: Boolean
        get() = this is Preparing || this is Uploading || this is Processing || this is Posting

    /** Finished one way or the other: the user may dismiss it. */
    val isTerminal: Boolean
        get() = this is Published || this is Failed
}

/**
 * Which kind of post a publish is making.
 *
 * A [REEL] is a `flick` — vertical, up to five minutes, lands on the Reels
 * tab; a [LONG] video is a `long_video` with a required title that lands in
 * Tube; a [PHOTO] is an ordinary photo post (one picture or a carousel) that
 * lands on the home feed and the profile's Posts tab.
 *
 * Product-neutral on purpose: this module only needs to know which SURFACE the
 * pending item belongs to, so Reels never shows a long video posting, Tube
 * never shows a reel, and neither of them shows a photo carousel.
 *
 * Was `VideoKind` until the photo studio joined this queue (founder,
 * 2026-09-06: pressing Post should land on the profile with the upload's
 * progress, the way a reel already does). The two video pipelines are
 * unchanged; only the name widened to match what the queue now carries.
 */
enum class PublishKind {
    PHOTO,
    REEL,
    LONG,
}

/**
 * What a pending tile shows for a video that is still posting: the cover
 * frame the user chose (a local JPEG path, or null when no frame could be
 * extracted), the caption, and — for a long video — its title, so the
 * pending item looks like the post it is about to become. [kind] decides
 * which surface draws it; [publishAt] (RFC 3339) is set when the post was
 * scheduled rather than posted now.
 */
data class ReelPublishPreview(
    val creationKey: String,
    val coverPath: String?,
    val caption: String,
    val kind: PublishKind = PublishKind.REEL,
    val title: String = "",
    val publishAt: String? = null,
)

/**
 * One entry of the publish queue: its preview (null until whoever enqueued
 * or restored the record has set it — a worker restarted by WorkManager
 * reports state before the controller has read the file) and its state.
 */
data class ReelPublishItem(
    val creationKey: String,
    val preview: ReelPublishPreview?,
    val state: ReelPublishState,
) {
    /** Only an item with a cover to draw and something happening is a tile. */
    val isDrawable: Boolean
        get() = preview != null && state !is ReelPublishState.Idle
}

/**
 * The publish worker's side of the pending tile — what "Retry" and "Discard" do.
 *
 * Declared here so `:feature:feed` and `:feature:profile` can call it;
 * IMPLEMENTED in `:feature:post`, which owns the worker and the persisted
 * queue, and bound by Hilt at the app level. The same port-and-adapter shape
 * the render exporter uses.
 */
interface ReelPublishActions {
    /** Re-enqueue the failed publish, keeping every media id it already has. */
    fun retry(creationKey: String)

    /** Forget the publish — queued, running or failed — and delete its cached files. */
    fun discard(creationKey: String)

    /** Let go of a finished publish. A publish still in flight is left alone. */
    fun dismiss(creationKey: String)
}

/**
 * The process-wide publish queue's status: one entry per creation key, in
 * the order they were enqueued (founder, 2026-09-05: "the user can start
 * another reel while one uploads"). The worker writes here as it goes; the
 * profile grid and the Reels tab read it; a process restart starts it empty
 * until the workers (restarted by WorkManager) or the persisted records put
 * the real states back — see [restoreIfIdle].
 */
@Singleton
class ReelPublishTracker @Inject constructor() {

    private val _items = MutableStateFlow<List<ReelPublishItem>>(emptyList())

    /** Every tracked publish, oldest first. */
    val items: StateFlow<List<ReelPublishItem>> = _items.asStateFlow()

    /** True while any publish is in flight. */
    val isActive: Boolean
        get() = _items.value.any { it.state.isActive }

    fun stateOf(creationKey: String): ReelPublishState =
        _items.value.firstOrNull { it.creationKey == creationKey }?.state ?: ReelPublishState.Idle

    fun previewOf(creationKey: String): ReelPublishPreview? =
        _items.value.firstOrNull { it.creationKey == creationKey }?.preview

    /** Set by whoever enqueues or restores a publish, before its first state. */
    fun setPreview(preview: ReelPublishPreview) = _items.update { list ->
        list.upsert(preview.creationKey) { it.copy(preview = preview) }
    }

    /**
     * Report progress. An upload fraction is clamped to 0..1 and never runs
     * backwards within one upload — a bar that jumps from 42% to 40% because
     * two progress callbacks raced reads as a bug, not as honesty.
     */
    fun update(creationKey: String, next: ReelPublishState) = _items.update { list ->
        list.upsert(creationKey) { it.copy(state = reconcile(it.state, next)) }
    }

    /**
     * Put a persisted state back after a process restart, but only if the
     * worker has not already reported something newer. Returns whether it won.
     */
    fun restoreIfIdle(creationKey: String, state: ReelPublishState): Boolean {
        var won = false
        _items.update { list ->
            list.upsert(creationKey) { item ->
                if (item.state is ReelPublishState.Idle) {
                    won = true
                    item.copy(state = state)
                } else {
                    item
                }
            }
        }
        return won
    }

    /** Let go of a finished publish. Ignored while work is still in flight. */
    fun dismiss(creationKey: String) = _items.update { list ->
        list.filterNot { it.creationKey == creationKey && it.state.isTerminal }
    }

    /** Back to nothing, whatever was there — the publish was discarded. */
    fun reset(creationKey: String) = _items.update { list -> list.filterNot { it.creationKey == creationKey } }

    private fun List<ReelPublishItem>.upsert(
        creationKey: String,
        change: (ReelPublishItem) -> ReelPublishItem,
    ): List<ReelPublishItem> {
        val index = indexOfFirst { it.creationKey == creationKey }
        return if (index < 0) {
            this + change(ReelPublishItem(creationKey, preview = null, state = ReelPublishState.Idle))
        } else {
            toMutableList().also { it[index] = change(it[index]) }
        }
    }

    private fun reconcile(current: ReelPublishState, next: ReelPublishState): ReelPublishState {
        if (next !is ReelPublishState.Uploading) return next
        val clamped = next.fraction.coerceIn(0f, 1f)
        return if (current is ReelPublishState.Uploading && current.fraction > clamped) {
            current
        } else {
            ReelPublishState.Uploading(clamped)
        }
    }
}
