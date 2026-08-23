package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * Ordering, rollback and reconciliation for optimistic engagement.
 *
 * WHY THE FAKE HOLDS STATE
 *
 * The previous version of this file completed two deferred responses in
 * whatever order it liked and asserted on the UI. That proved nothing about
 * the defect it claimed to cover: it controlled when REPLIES arrived, but the
 * corruption happens when two REQUESTS are in flight and the server applies
 * them in the wrong order. With no authoritative state in the fake, the schedule
 *
 *   POST like dispatched -> DELETE unlike dispatched -> DELETE applied first
 *   (no-op) -> POST applied second (server ends up LIKED, UI shows UNLIKED)
 *
 * was invisible, and the test passed while the bug was live.
 *
 * [FakeServer] therefore keeps the same three booleans a real backend would,
 * applies each call when the test releases it, and records how many calls were
 * ever in flight at once. That last number is the load-bearing assertion: if it
 * is ever greater than one, the client can reorder writes and no amount of
 * response-side logic can repair it.
 *
 * Mutations are launched into `backgroundScope` because several tests
 * deliberately leave a call unreleased — that is the state under test. A plain
 * `launch` would make `runTest` wait for work the test is asserting is still
 * outstanding.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class EngagementStoreTest {

    private val postId = "p1"

    /**
     * An authoritative fake backend whose calls the test releases by hand.
     */
    private class FakeServer : EngagementWrites {

        /** Server truth, mutated only when a released call succeeds. */
        var reacted = false
        var bookmarked = false
        var reposted = false

        /** Every call in the order it reached the "server". */
        val log = mutableListOf<String>()

        /** The high-water mark of simultaneously in-flight calls. */
        var maxInFlight = 0
        private var inFlight = 0

        private val pending = ArrayDeque<Pending>()

        private class Pending(
            val name: String,
            val apply: () -> Unit,
            val gate: CompletableDeferred<AppResult<Unit>>,
        )

        val pendingCount: Int get() = pending.size

        /** Releases the oldest outstanding call with the given outcome. */
        fun release(result: AppResult<Unit>) {
            val next = pending.removeFirst()
            if (result is AppResult.Success) next.apply()
            next.gate.complete(result)
        }

        fun releaseSuccess() = release(AppResult.Success(Unit))
        fun releaseFailure() = release(AppResult.Failure(AppError.NoNetwork()))

        private suspend fun call(name: String, apply: () -> Unit): AppResult<Unit> {
            inFlight++
            if (inFlight > maxInFlight) maxInFlight = inFlight
            log += name
            val gate = CompletableDeferred<AppResult<Unit>>()
            pending += Pending(name, apply, gate)
            try {
                return gate.await()
            } finally {
                inFlight--
            }
        }

        override suspend fun react(postId: String, reaction: String) =
            call("react") { reacted = true }

        override suspend fun unreact(postId: String) =
            call("unreact") { reacted = false }

        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) =
            call("bookmark=$bookmarked") { this.bookmarked = bookmarked }

        override suspend fun repost(postId: String) =
            call("repost") { reposted = true }

        override suspend fun removeRepost(postId: String) =
            call("unrepost") { reposted = false }
    }

    private fun storeWith(server: FakeServer) = EngagementStore(server)

    // ── Serialization ───────────────────────────────────────────────────

    /**
     * THE core guarantee. Two taps, and the second request must not leave the
     * client until the first has settled.
     */
    @Test
    fun `a second write cannot start before the first settles`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()

        assertThat(server.maxInFlight).isEqualTo(1)
        assertThat(server.pendingCount).isEqualTo(1)
        assertThat(server.log).containsExactly("react")

        server.releaseSuccess()
        runCurrent()

        // Only now may the second call go out.
        assertThat(server.log).containsExactly("react", "unreact").inOrder()
        assertThat(server.maxInFlight).isEqualTo(1)
    }

    @Test
    fun `success then success leaves the server holding the latest intent`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()

        server.releaseSuccess()
        runCurrent()
        server.releaseSuccess()
        runCurrent()

        assertThat(server.reacted).isFalse()
        assertThat(store.overlayFor(postId).reactedOr(false)).isFalse()
        assertThat(server.maxInFlight).isEqualTo(1)
    }

    /**
     * The newest write fails. The UI must fall back to the last value the
     * server actually acknowledged — not to the page snapshot, which is what
     * the version-only implementation did.
     */
    @Test
    fun `success then failure rolls back to the confirmed value, not the page`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseSuccess()
        runCurrent()
        assertThat(server.reacted).isTrue()

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseFailure()
        runCurrent()

        // Confirmed state is "reacted"; the failed unlike returns there.
        assertThat(server.reacted).isTrue()
        assertThat(store.overlayFor(postId).reactedOr(false)).isTrue()
        assertThat(store.failures.value.single().action).isEqualTo(EngagementAction.REACTION)
    }

    @Test
    fun `failure then success leaves both server and UI on the latest intent`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseFailure()
        runCurrent()
        assertThat(store.overlayFor(postId).reactedOr(false)).isFalse()

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseSuccess()
        runCurrent()

        assertThat(server.reacted).isTrue()
        assertThat(store.overlayFor(postId).reactedOr(false)).isTrue()
    }

    /** A failed bookmark must not disturb a reaction on the same post. */
    @Test
    fun `a failure rolls back only its own action`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        backgroundScope.launch { store.toggleBookmark(postId, serverBookmarked = false) }
        runCurrent()

        // Different lanes, so both may be in flight at once.
        assertThat(server.pendingCount).isEqualTo(2)

        server.releaseFailure() // the reaction
        runCurrent()

        assertThat(store.overlayFor(postId).reactedOr(false)).isFalse()
        assertThat(store.overlayFor(postId).bookmarked).isTrue()
    }

    // ── Counts ──────────────────────────────────────────────────────────

    @Test
    fun `counts are derived, never accumulated`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        repeat(6) {
            backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
            runCurrent()
        }

        val overlay = store.overlayFor(postId)
        assertThat(overlay.likeCountOr(serverCount = 10, serverReacted = false)).isIn(listOf(10, 11))
    }

    @Test
    fun `an unlike at zero floors at zero`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = true) }
        runCurrent()

        val overlay = store.overlayFor(postId)
        assertThat(overlay.likeCountOr(serverCount = 0, serverReacted = true)).isEqualTo(0)
    }

    // ── Production reconciliation ───────────────────────────────────────

    /**
     * The overlay must be RETIRED once a refresh confirms it. Without a
     * production caller for this, a successful like stayed pinned for the
     * process lifetime and kept overriding later server truth.
     */
    @Test
    fun `reconcile retires a settled overlay the server now agrees with`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseSuccess()
        runCurrent()
        assertThat(store.overlayFor(postId).reacted).isTrue()

        store.reconcile(postId, serverReacted = true, serverBookmarked = false, serverReposted = false)

        assertThat(store.overlayFor(postId).reacted).isNull()
        assertThat(store.overlays.value).doesNotContainKey(postId)
    }

    /** A refresh landing mid-request must not flicker the control back. */
    @Test
    fun `reconcile leaves an in-flight lane alone`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()

        store.reconcile(postId, serverReacted = false, serverBookmarked = false, serverReposted = false)

        assertThat(store.overlayFor(postId).reacted).isTrue()

        server.releaseSuccess()
        runCurrent()
        assertThat(server.reacted).isTrue()
    }

    /**
     * The server moved on independently — another device, or moderation. The
     * settled overlay is dropped and the server's value renders.
     */
    @Test
    fun `reconcile drops a settled overlay the server disagrees with`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseSuccess()
        runCurrent()

        store.reconcile(postId, serverReacted = false, serverBookmarked = false, serverReposted = false)

        assertThat(store.overlays.value).doesNotContainKey(postId)
    }

    // ── Session isolation ───────────────────────────────────────────────

    /**
     * Overlays are private per-viewer data keyed only by post id. A sign-out
     * and sign-in inside one process must not leave one account's bookmarks
     * and reactions visible to — or actionable by — the next.
     */
    @Test
    fun `switching viewer clears every overlay and failure`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)
        store.setViewer("viewer-a")

        backgroundScope.launch { store.toggleBookmark(postId, serverBookmarked = false) }
        runCurrent()
        server.releaseFailure()
        runCurrent()

        assertThat(store.overlays.value).isNotEmpty()
        assertThat(store.failures.value).isNotEmpty()

        store.setViewer("viewer-b")

        assertThat(store.overlays.value).isEmpty()
        assertThat(store.failures.value).isEmpty()
    }

    @Test
    fun `re-emitting the same viewer keeps state`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)
        store.setViewer("viewer-a")

        backgroundScope.launch { store.toggleBookmark(postId, serverBookmarked = false) }
        runCurrent()
        server.releaseSuccess()
        runCurrent()

        store.setViewer("viewer-a")

        assertThat(store.overlayFor(postId).bookmarked).isTrue()
    }

    @Test
    fun `signing out clears state`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)
        store.setViewer("viewer-a")

        backgroundScope.launch { store.toggleBookmark(postId, serverBookmarked = false) }
        runCurrent()
        server.releaseSuccess()
        runCurrent()

        store.setViewer(null)

        assertThat(store.overlays.value).isEmpty()
    }

    // ── Retry ───────────────────────────────────────────────────────────

    /**
     * PROOF 1 — the visible Retry control actually re-sends.
     *
     * The previous version of this test called `toggleReaction` a second time
     * and asserted the server ended up liked. That proved a second TAP works.
     * It never called `retry()`, and `retry()` was a no-op: the failure path
     * had already rolled `desired` back to `confirmed`, so `drain` saw nothing
     * outstanding and returned without touching the repository. This calls the
     * real entry point.
     */
    @Test
    fun `retry re-sends the retained failed target`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseFailure()
        runCurrent()

        assertThat(store.failures.value).hasSize(1)
        assertThat(server.log).containsExactly("react")
        // Rolled back for rendering...
        assertThat(store.overlayFor(postId).reactedOr(false)).isFalse()

        backgroundScope.launch { store.retry(postId, EngagementAction.REACTION) }
        runCurrent()

        // ...but the failed target was retained, so retry re-issues the LIKE
        // rather than an unlike.
        assertThat(server.log).containsExactly("react", "react").inOrder()

        server.releaseSuccess()
        runCurrent()

        assertThat(server.reacted).isTrue()
        assertThat(store.overlayFor(postId).reactedOr(false)).isTrue()
        assertThat(store.failures.value).isEmpty()
    }

    /** A second Retry while the first is in flight must not open a parallel write. */
    @Test
    fun `a second retry while one is in flight does not start a parallel write`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseFailure()
        runCurrent()

        backgroundScope.launch { store.retry(postId, EngagementAction.REACTION) }
        runCurrent()
        backgroundScope.launch { store.retry(postId, EngagementAction.REACTION) }
        runCurrent()

        assertThat(server.maxInFlight).isEqualTo(1)
        assertThat(server.pendingCount).isEqualTo(1)

        server.releaseSuccess()
        runCurrent()

        // The second retry found nothing outstanding and issued no call.
        assertThat(server.log).containsExactly("react", "react").inOrder()
        assertThat(server.reacted).isTrue()
    }

    /**
     * NEGATIVE CONTROL for proof 1. With no retained target there is nothing
     * to resend, and Retry must not invent one by flipping the lane.
     */
    @Test
    fun `retry on a healthy lane sends nothing`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        server.releaseSuccess()
        runCurrent()
        assertThat(server.log).containsExactly("react")

        backgroundScope.launch { store.retry(postId, EngagementAction.REACTION) }
        runCurrent()

        assertThat(server.log).containsExactly("react")
        assertThat(store.overlayFor(postId).reactedOr(false)).isTrue()
    }

    // ── Viewer generation fence ─────────────────────────────────────────

    /**
     * PROOF 3 — an old viewer's in-flight completion cannot touch the new
     * viewer's lane.
     *
     * Clearing the maps on sign-in is not enough: A's coroutine is already
     * running and holds a lock this store no longer tracks. Without a fence
     * its late success writes `confirmed = true` into B's lane, and B's own
     * failure then "rolls back" to A's confirmation — showing B as having
     * liked a post their request failed to like.
     */
    @Test
    fun `an old viewer's late success cannot confirm the new viewer's lane`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)
        store.setViewer("viewer-a")

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        assertThat(server.pendingCount).isEqualTo(1)

        // Account switch while A's write is still on the wire.
        store.setViewer("viewer-b")
        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()

        // A completes successfully, late.
        server.releaseSuccess()
        runCurrent()
        // B's write then fails.
        server.releaseFailure()
        runCurrent()

        // B asked to like and failed, so B must render as NOT liked. If A's
        // success had leaked in as `confirmed`, the rollback would land on
        // true and B would see a like they never got.
        assertThat(store.overlayFor(postId).reactedOr(false)).isFalse()
    }

    @Test
    fun `an old viewer's late failure cannot roll back or flag the new viewer`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)
        store.setViewer("viewer-a")

        backgroundScope.launch { store.toggleBookmark(postId, serverBookmarked = false) }
        runCurrent()

        store.setViewer("viewer-b")
        backgroundScope.launch { store.toggleBookmark(postId, serverBookmarked = false) }
        runCurrent()

        server.releaseFailure() // viewer A's
        runCurrent()

        // No banner belonging to the previous account.
        assertThat(store.failures.value).isEmpty()
        // B's optimistic bookmark is untouched and still in flight.
        assertThat(store.overlayFor(postId).bookmarked).isTrue()

        server.releaseSuccess() // viewer B's
        runCurrent()
        assertThat(store.overlayFor(postId).bookmarked).isTrue()
    }

    /**
     * NEGATIVE CONTROL for proof 3. Same schedule, but the viewer never
     * changes, so the fence must NOT engage and the late result must apply.
     * If this stops applying, the fence has become over-broad and is
     * discarding legitimate results.
     */
    @Test
    fun `without a viewer change the late completion still applies`() = runTest {
        val server = FakeServer()
        val store = storeWith(server)
        store.setViewer("viewer-a")

        backgroundScope.launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()

        server.releaseSuccess()
        runCurrent()

        assertThat(server.reacted).isTrue()
        assertThat(store.overlayFor(postId).reacted).isTrue()
    }
}
