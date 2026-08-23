package com.us.android.core.engagement.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.flow.updateAndGet
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Which engagement action a mutation belongs to.
 *
 * State is per post AND per action: liking a post must not cancel or roll back
 * a bookmark on the same post that is still in flight.
 */
enum class EngagementAction { REACTION, BOOKMARK, REPOST }

/** Identifies one serialized mutation lane. */
private data class ActionKey(val postId: String, val action: EngagementAction)

/**
 * One lane's full state.
 *
 * THREE FIELDS, NOT ONE. An earlier version tracked only the desired value and
 * a version token, which could not answer the question a rollback actually
 * needs — "what is the last value the server acknowledged?" It rolled back to
 * whatever the loaded page happened to say, so a failed unlike after a
 * successful like restored the page's stale `false` and the UI then disagreed
 * with a server that held `true`.
 *
 * @param confirmed the newest value the server has acknowledged.
 * @param desired what the viewer wants. Diverges from [confirmed] only while
 *   work is outstanding.
 * @param inFlight whether a write for this lane is currently on the wire. Read
 *   by [reconcile] so a refresh cannot retire an overlay mid-request.
 */
private data class ActionState(
    val confirmed: Boolean,
    val desired: Boolean,
    val inFlight: Boolean = false,
    /**
     * The value a failed write was trying to reach.
     *
     * Kept because rolling `desired` back to `confirmed` erases what the
     * viewer actually asked for. Without it `retry()` had nothing to resend:
     * it called `drain`, which saw `confirmed == desired`, concluded there was
     * no work, and returned without touching the network. The button was
     * wired to a no-op.
     *
     * Null whenever the lane is healthy.
     */
    val failedTarget: Boolean? = null,
) {
    val settled: Boolean get() = confirmed == desired && !inFlight
}

/**
 * The viewer's local intent for one post, layered over the server's values.
 *
 * A null field means "no local intent — show the server's value".
 */
data class EngagementOverlay(
    val reacted: Boolean? = null,
    val bookmarked: Boolean? = null,
    val reposted: Boolean? = null,
) {
    val isEmpty: Boolean get() = reacted == null && bookmarked == null && reposted == null
}

/** A failed mutation the UI can surface and offer to retry. */
data class EngagementFailure(
    val postId: String,
    val action: EngagementAction,
    val error: AppError,
)

/**
 * Optimistic engagement state shared by every surface.
 *
 * WHY A SINGLETON
 *
 * The same post appears in the feed, in post detail, and later in search and
 * profile. If each screen kept its own optimistic set, liking in the feed and
 * opening the post would show an unliked heart until a refresh — the app
 * disagreeing with itself. One store means one answer.
 *
 * WHY WRITES ARE SERIALIZED PER LANE
 *
 * Versioning responses is not enough, because it only decides which REPLY to
 * believe; it does not stop two requests being in flight at once. This is the
 * schedule that corrupted state:
 *
 *   1. tap like    -> POST   dispatched
 *   2. tap unlike  -> DELETE dispatched (POST has not returned)
 *   3. DELETE arrives at the server FIRST -> no-op, there is nothing to remove
 *   4. POST arrives second -> server stores LIKED
 *   -> both responses succeed, the UI shows UNLIKED, the server holds LIKED.
 *
 * No amount of response-ordering logic can repair that: by the time either
 * reply is examined the damage is already written. The fix has to be that the
 * second request is not sent until the first has settled, which is what the
 * per-lane [Mutex] guarantees.
 *
 * Rapid taps are still accepted immediately — [ActionState.desired] moves on
 * every tap and the UI follows it. What is serialized is the network effect,
 * not the intent. Because the worker re-reads `desired` after each write, a
 * like/unlike pair that lands during one request collapses into at most one
 * further call instead of queueing a burst.
 */
@Singleton
class EngagementStore @Inject constructor(
    private val repository: EngagementWrites,
) {

    private val _states = MutableStateFlow<Map<ActionKey, ActionState>>(emptyMap())

    private val _overlays = MutableStateFlow<Map<String, EngagementOverlay>>(emptyMap())

    /**
     * Desired state per post, for rendering.
     *
     * Republished from [_states] through [updateStates] rather than held as an
     * independent source of truth, so the two cannot drift. It is a separate
     * flow only because deriving one would need a CoroutineScope, and a
     * data-layer singleton that owns a scope outlives every screen that uses
     * it.
     */
    val overlays: StateFlow<Map<String, EngagementOverlay>> = _overlays.asStateFlow()

    private val _failures = MutableStateFlow<List<EngagementFailure>>(emptyList())
    val failures: StateFlow<List<EngagementFailure>> = _failures.asStateFlow()

    /**
     * One lock per lane, so a bookmark is never blocked behind a reaction on
     * the same post. Created under [registryLock] because two taps arriving
     * together must not each create their own mutex and both proceed.
     */
    private val locks = mutableMapOf<ActionKey, Mutex>()
    private val registryLock = Any()

    /**
     * Who this state belongs to.
     *
     * Overlays are per-viewer private data: a bookmark, a reaction, a repost.
     * The keys are post ids only, so without this the next account signed in
     * on the same process would inherit — and then act on — the previous
     * viewer's intent.
     */
    private var viewerId: String? = null

    /**
     * Bumped on every viewer change; a write carries the value it started with.
     *
     * Clearing the maps in [setViewer] does not stop work already in flight —
     * that coroutine holds a lock this class no longer tracks, and it will
     * happily apply its result to whatever lane now sits at the same
     * `(postId, action)` key. The damage is concrete: viewer A likes a post,
     * the account switches, viewer B likes the same post, A's write succeeds
     * and writes `confirmed = true` into B's lane, and B's own failure then
     * "rolls back" to A's confirmation — showing B as having liked something
     * their request never managed to like.
     *
     * `@Volatile` because the writer is whichever thread calls [setViewer] and
     * the readers are the coroutines already running.
     */
    @Volatile
    private var viewerGeneration: Long = 0

    private fun currentGeneration(): Long = viewerGeneration

    private fun isCurrent(generation: Long): Boolean = generation == viewerGeneration

    private fun lockFor(key: ActionKey): Mutex = synchronized(registryLock) {
        locks.getOrPut(key) { Mutex() }
    }

    fun overlayFor(postId: String): EngagementOverlay =
        overlays.value[postId] ?: EngagementOverlay()

    /**
     * Binds the store to the signed-in viewer, clearing everything on a change.
     *
     * Called with the authenticated user id, and with null on sign-out. A
     * repeat call for the same viewer is a no-op, so it is safe to invoke on
     * every session emission.
     *
     * Clearing is one atomic step across states, failures and locks. Dropping
     * only the overlays would leave a lane's mutex held by the previous
     * viewer's in-flight write and a failure banner attributed to the new one.
     */
    fun setViewer(newViewerId: String?) {
        if (newViewerId == viewerId) return
        viewerId = newViewerId
        // Bumped BEFORE the maps are cleared. Any write already running is
        // fenced out from this instant, so it cannot repopulate the state that
        // is about to be emptied.
        viewerGeneration += 1
        synchronized(registryLock) { locks.clear() }
        updateStates { emptyMap() }
        _failures.value = emptyList()
    }

    /**
     * Applies the viewer's reaction intent and persists it.
     *
     * [serverReacted] is the value the current page carries. It seeds
     * `confirmed` the first time this post is touched; afterwards the lane's
     * own confirmed value wins, because the page snapshot may be older than
     * what this store has already had acknowledged.
     */
    suspend fun toggleReaction(postId: String, serverReacted: Boolean) {
        toggle(ActionKey(postId, EngagementAction.REACTION), serverReacted)
    }

    suspend fun toggleBookmark(postId: String, serverBookmarked: Boolean) {
        toggle(ActionKey(postId, EngagementAction.BOOKMARK), serverBookmarked)
    }

    suspend fun toggleRepost(postId: String, serverReposted: Boolean) {
        toggle(ActionKey(postId, EngagementAction.REPOST), serverReposted)
    }

    /**
     * Re-sends the target a failed write was trying to reach.
     *
     * Restores `desired` from [ActionState.failedTarget] rather than flipping
     * anything: Retry means "try that again", not "toggle". Doing nothing when
     * there is no retained target keeps a stray tap on a healthy lane from
     * inverting it.
     */
    suspend fun retry(postId: String, action: EngagementAction) {
        val key = ActionKey(postId, action)
        val generation = currentGeneration()
        val target = _states.value[key]?.failedTarget ?: return
        updateStates { states ->
            val latest = states[key] ?: return@updateStates states
            states + (key to latest.copy(desired = target, failedTarget = null))
        }
        clearFailure(postId, action)
        drain(key, generation)
    }

    private suspend fun toggle(key: ActionKey, serverValue: Boolean) {
        val generation = currentGeneration()
        updateStates { states ->
            val current = states[key]
            val confirmed = current?.confirmed ?: serverValue
            val shown = current?.desired ?: serverValue
            states + (
                key to ActionState(
                    confirmed = confirmed,
                    desired = !shown,
                    inFlight = current?.inFlight == true,
                    // A fresh intent supersedes whatever failed before it.
                    failedTarget = null,
                )
                )
        }
        clearFailure(key.postId, key.action)
        drain(key, generation)
    }

    /**
     * Sends whatever is outstanding for one lane, one request at a time.
     *
     * Loops rather than sending once: a tap that lands while a request is on
     * the wire changes `desired`, and this picks it up on the next turn. It
     * stops as soon as the server agrees with the viewer, so a like/unlike
     * pair issued during a single request costs one extra call, not two.
     *
     * On failure it stops. Retrying here would spin against a server that is
     * down; the failure is published instead and [retry] is the user's move.
     */
    private suspend fun drain(key: ActionKey, generation: Long) {
        lockFor(key).withLock {
            while (true) {
                // Every turn re-checks the fence. Clearing state on a viewer
                // change is not enough on its own: a coroutine that already
                // holds this lock keeps running, and its result would land on
                // whatever lane now occupies the same (post, action) key.
                if (!isCurrent(generation)) return
                val state = _states.value[key] ?: return
                if (state.confirmed == state.desired) {
                    setInFlight(key, false, generation)
                    return
                }
                val target = state.desired
                setInFlight(key, true, generation)

                val result = write(key, target)

                if (result is AppResult.Success) {
                    // Only `confirmed` moves. `desired` may already have been
                    // changed by another tap, and overwriting it here would
                    // discard intent the user expressed during the request.
                    updateStates(generation) { states ->
                        val latest = states[key] ?: return@updateStates states
                        states + (key to latest.copy(confirmed = target, failedTarget = null))
                    }
                    continue
                }

                val error = (result as AppResult.Failure).error
                var published = false
                updateStates(generation) { states ->
                    val latest = states[key] ?: return@updateStates states
                    published = true
                    // Roll back only if this failure is still the newest
                    // intent. If the viewer has since asked for something
                    // else, that newer wish stays and the loop would have
                    // pursued it — but a failing lane stops, so it is left for
                    // an explicit retry rather than an automatic one.
                    if (latest.desired == target) {
                        states + (
                            key to latest.copy(
                                desired = latest.confirmed,
                                inFlight = false,
                                // Retained so Retry has something to resend.
                                failedTarget = target,
                            )
                            )
                    } else {
                        states + (key to latest.copy(inFlight = false))
                    }
                }
                // Suppressed along with the state write when the viewer has
                // changed: a banner belonging to the previous account must not
                // appear on the next one's screen.
                if (published) {
                    _failures.update { it + EngagementFailure(key.postId, key.action, error) }
                }
                return
            }
        }
    }

    private suspend fun write(key: ActionKey, target: Boolean): AppResult<Unit> =
        when (key.action) {
            EngagementAction.REACTION ->
                if (target) repository.react(key.postId) else repository.unreact(key.postId)

            EngagementAction.BOOKMARK -> repository.setBookmarked(key.postId, target)

            EngagementAction.REPOST ->
                if (target) repository.repost(key.postId) else repository.removeRepost(key.postId)
        }

    private fun setInFlight(key: ActionKey, value: Boolean, generation: Long) {
        updateStates(generation) { states ->
            val latest = states[key] ?: return@updateStates states
            states + (key to latest.copy(inFlight = value))
        }
    }

    /**
     * Adopts freshly loaded server values and retires settled local intent.
     *
     * Called from feed and post-detail hydration. Without this the overlay
     * outlives its usefulness: it keeps re-applying a value the server already
     * agrees with, and it pins that value against later changes made
     * elsewhere — the author deleting the post, or moderation clearing
     * engagement.
     *
     * A lane with work outstanding is left completely alone. Retiring it
     * mid-request would flicker the control back to the pre-tap value, and
     * adopting the server's value as `confirmed` would be wrong: this response
     * was in flight before the write landed, so it does not describe it.
     */
    fun reconcile(
        postId: String,
        serverReacted: Boolean,
        serverBookmarked: Boolean,
        serverReposted: Boolean,
    ) {
        updateStates { states ->
            var next = states
            next = next.reconcileLane(ActionKey(postId, EngagementAction.REACTION), serverReacted)
            next = next.reconcileLane(ActionKey(postId, EngagementAction.BOOKMARK), serverBookmarked)
            next = next.reconcileLane(ActionKey(postId, EngagementAction.REPOST), serverReposted)
            next
        }
    }

    private fun Map<ActionKey, ActionState>.reconcileLane(
        key: ActionKey,
        serverValue: Boolean,
    ): Map<ActionKey, ActionState> {
        val state = this[key] ?: return this
        if (!state.settled) return this
        // Settled and in agreement: the overlay has nothing left to say, so it
        // is dropped and the page value renders directly.
        if (state.confirmed == serverValue) return this - key
        // Settled but different: the server has moved on since this lane last
        // wrote — another device, or a moderation action. The server wins.
        return this - key
    }

    fun clearFailure(postId: String, action: EngagementAction) {
        _failures.update { list -> list.filterNot { it.postId == postId && it.action == action } }
    }

    /**
     * The single write path for lane state.
     *
     * Overlays are recomputed here, in the same step, so no caller can move a
     * lane without the rendered value following it.
     */
    private inline fun updateStates(
        generation: Long? = null,
        block: (Map<ActionKey, ActionState>) -> Map<ActionKey, ActionState>,
    ) {
        // updateAndGet, not read-then-write. `_states.value = block(_states.value)`
        // is two steps, and a concurrently completing lane between them loses
        // its write entirely. The CAS loop retries instead.
        val next = _states.updateAndGet { current ->
            // The fence is re-tested INSIDE the CAS body, so a viewer change
            // that lands between the caller's check and the write is still
            // caught.
            if (generation != null && generation != viewerGeneration) current else block(current)
        }
        _overlays.value = next.toOverlays()
    }

    private fun Map<ActionKey, ActionState>.toOverlays(): Map<String, EngagementOverlay> {
        if (isEmpty()) return emptyMap()
        val byPost = mutableMapOf<String, EngagementOverlay>()
        for ((key, state) in this) {
            val existing = byPost[key.postId] ?: EngagementOverlay()
            byPost[key.postId] = when (key.action) {
                EngagementAction.REACTION -> existing.copy(reacted = state.desired)
                EngagementAction.BOOKMARK -> existing.copy(bookmarked = state.desired)
                EngagementAction.REPOST -> existing.copy(reposted = state.desired)
            }
        }
        return byPost
    }
}

/**
 * The reaction state to draw: local intent when present, otherwise the server.
 */
fun EngagementOverlay.reactedOr(serverReacted: Boolean): Boolean = reacted ?: serverReacted

fun EngagementOverlay.bookmarkedOr(serverBookmarked: Boolean): Boolean = bookmarked ?: serverBookmarked

fun EngagementOverlay.repostedOr(serverReposted: Boolean): Boolean = reposted ?: serverReposted

/**
 * The like count to draw.
 *
 * Derived, never accumulated. The correction is at most one in either
 * direction and is floored at zero, so no sequence of taps — however fast, and
 * whatever order the responses land in — can produce a negative or
 * double-counted number.
 */
fun EngagementOverlay.likeCountOr(serverCount: Int, serverReacted: Boolean): Int {
    val shown = reacted ?: return serverCount
    return when {
        shown == serverReacted -> serverCount
        shown -> serverCount + 1
        else -> (serverCount - 1).coerceAtLeast(0)
    }
}

/** Same derivation for reposts. */
fun EngagementOverlay.repostCountOr(serverCount: Int, serverReposted: Boolean): Int {
    val shown = reposted ?: return serverCount
    return when {
        shown == serverReposted -> serverCount
        shown -> serverCount + 1
        else -> (serverCount - 1).coerceAtLeast(0)
    }
}
