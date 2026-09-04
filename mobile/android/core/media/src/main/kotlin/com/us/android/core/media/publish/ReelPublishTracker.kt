package com.us.android.core.media.publish

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.getAndUpdate
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Where a background reel publish has got to — the ONE state the Reels tab's
 * pending item renders and the publish worker writes.
 *
 * Product-neutral on purpose: this module knows how to upload and play
 * media, not what a reel's form contains. The pending publish itself (the
 * video, the caption, the switches) lives with the worker in `:feature:post`;
 * only its progress crosses into a module that `:feature:feed` may also see,
 * because features must not depend on each other.
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

    /** Done. The Reels tab swaps the pending item for the real reel. */
    data class Published(val postId: String) : ReelPublishState

    /** Stopped. [retryable] decides whether the pending item offers "Retry". */
    data class Failed(val message: String, val retryable: Boolean) : ReelPublishState

    /** Work is in flight — a second publish must wait for it. */
    val isActive: Boolean
        get() = this is Preparing || this is Uploading || this is Processing || this is Posting

    /** Finished one way or the other: the user may dismiss it. */
    val isTerminal: Boolean
        get() = this is Published || this is Failed
}

/**
 * Which kind of video a publish is (Tube, 2026-09-05).
 *
 * The same pipeline posts both: a [REEL] is a `flick` — vertical, up to five
 * minutes, lands on the Reels tab — and a [LONG] video is a `long_video`
 * with a required title that lands in Tube. Product-neutral on purpose: this
 * module only needs to know which SURFACE the pending item belongs to, so
 * Reels never shows a long video posting and Tube never shows a reel.
 */
enum class VideoKind {
    REEL,
    LONG,
}

/**
 * What the Reels tab (or Tube) shows for a video that is still posting: the
 * cover frame the user chose (a local JPEG path, or null when no frame could
 * be extracted), the caption, and — for a long video — its title, so the
 * pending item looks like the post it is about to become. [kind] decides
 * which surface draws it.
 */
data class ReelPublishPreview(
    val creationKey: String,
    val coverPath: String?,
    val caption: String,
    val kind: VideoKind = VideoKind.REEL,
    val title: String = "",
)

/**
 * The publish worker's side of the pending item — what "Retry" and "Discard" do.
 *
 * Declared here so `:feature:feed` can call it; IMPLEMENTED in `:feature:post`,
 * which owns the worker and the persisted publish, and bound by Hilt at the
 * app level. The same port-and-adapter shape the render exporter uses.
 */
interface ReelPublishActions {
    /** Re-enqueue the failed publish, keeping every media id it already has. */
    fun retry()

    /** Forget the failed publish and delete its cached files. */
    fun discard()

    /** Let go of a finished publish. A publish still in flight is left alone. */
    fun dismiss()
}

/**
 * The single process-wide reel publish status.
 *
 * One at a time by design: the pending publish is persisted as one record,
 * and the Reels tab has one slot above the feed for it. The worker writes
 * here as it goes; the reels tab reads it; a process restart starts it at
 * [Idle] until the worker (restarted by WorkManager) or the persisted record
 * puts the real state back — see [restoreIfIdle].
 */
@Singleton
class ReelPublishTracker @Inject constructor() {

    private val _state = MutableStateFlow<ReelPublishState>(ReelPublishState.Idle)
    val state: StateFlow<ReelPublishState> = _state.asStateFlow()

    private val _preview = MutableStateFlow<ReelPublishPreview?>(null)

    /** What the pending item looks like; null when nothing is pending. */
    val preview: StateFlow<ReelPublishPreview?> = _preview.asStateFlow()

    val isActive: Boolean
        get() = _state.value.isActive

    /** Set by whoever enqueues or restores a publish, before its first state. */
    fun setPreview(preview: ReelPublishPreview?) {
        _preview.value = preview
    }

    /**
     * Report progress. An upload fraction is clamped to 0..1 and never runs
     * backwards within one upload — a bar that jumps from 42% to 40% because
     * two progress callbacks raced reads as a bug, not as honesty.
     */
    fun update(next: ReelPublishState) = _state.update { current -> reconcile(current, next) }

    /**
     * Put a persisted state back after a process restart, but only if the
     * worker has not already reported something newer. Returns whether it won.
     */
    fun restoreIfIdle(state: ReelPublishState): Boolean =
        _state.compareAndSet(ReelPublishState.Idle, state)

    /** Let go of a finished publish. Ignored while work is still in flight. */
    fun dismiss() {
        val cleared = _state.getAndUpdate { if (it.isTerminal) ReelPublishState.Idle else it }.isTerminal
        if (cleared) _preview.value = null
    }

    /** Back to nothing, whatever was there — the publish was discarded. */
    fun reset() {
        _state.value = ReelPublishState.Idle
        _preview.value = null
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
