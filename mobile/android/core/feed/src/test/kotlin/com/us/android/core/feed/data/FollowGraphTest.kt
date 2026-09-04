package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FollowStatus
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * When a post offers "Follow", and what the graph does about it.
 *
 * The button is the one control on the Instagram card that depends on a
 * SECOND request, so the rules that matter are the ones that keep it honest:
 * never on the viewer's own post, never while the answer is unknown, never
 * once the viewer already follows or has asked to — and one relationship
 * lookup per author, however many rows they have on screen.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class FollowGraphTest {

    // ── The rule the card and the reel overlay both read ─────────────

    @Test
    fun `follow is offered only for a known not-followed other author`() {
        assertThat(offersFollow(ownId = "me", authorId = "ada", edge = FollowStatus.NONE)).isTrue()
    }

    @Test
    fun `own posts never offer follow`() {
        assertThat(offersFollow(ownId = "me", authorId = "me", edge = FollowStatus.NONE)).isFalse()
    }

    @Test
    fun `a followed or requested author does not offer follow`() {
        assertThat(offersFollow(ownId = "me", authorId = "ada", edge = FollowStatus.FOLLOWING)).isFalse()
        assertThat(offersFollow(ownId = "me", authorId = "ada", edge = FollowStatus.REQUESTED)).isFalse()
    }

    /** A Follow that appears and then vanishes when the real answer lands is worse than one that arrives late. */
    @Test
    fun `an unknown edge does not offer follow`() {
        assertThat(offersFollow(ownId = "me", authorId = "ada", edge = null)).isFalse()
    }

    @Test
    fun `a row with no author id offers nothing`() {
        assertThat(offersFollow(ownId = "me", authorId = "", edge = FollowStatus.NONE)).isFalse()
    }

    // ── Learning the edges ───────────────────────────────────────────

    @Test
    fun `each author is asked for once, never the viewer`() = runTest {
        val api = RecordingGraphApi(follows = mutableMapOf("bob" to true))
        val graph = followGraph(api)

        graph.ensureKnown(listOf("ada", "bob", "ada", "me", ""))
        graph.ensureKnown(listOf("ada", "bob"))

        assertThat(api.relationshipRequests).containsExactly("me" to "ada", "me" to "bob")
        assertThat(graph.edges.value).containsExactly("ada", FollowStatus.NONE, "bob", FollowStatus.FOLLOWING)
    }

    @Test
    fun `nothing is asked before the session resolves`() = runTest {
        val api = RecordingGraphApi()
        val graph = followGraph(api, session = FakeSession(userId = null))

        graph.ensureKnown(listOf("ada"))

        assertThat(api.relationshipRequests).isEmpty()
        assertThat(graph.ownId).isEmpty()
    }

    // ── Following ────────────────────────────────────────────────────

    @Test
    fun `a follow flips the edge and sends the request`() = runTest {
        val api = RecordingGraphApi()
        val graph = followGraph(api)
        graph.ensureKnown(listOf("ada"))

        graph.follow("ada")

        assertThat(api.followRequests).containsExactly("ada")
        assertThat(graph.edges.value["ada"]).isEqualTo(FollowStatus.FOLLOWING)
    }

    /** A private account answers "requested"; the button must hide without claiming a follow. */
    @Test
    fun `a private account records a request, not a follow`() = runTest {
        val api = RecordingGraphApi(followAnswer = "requested")
        val graph = followGraph(api)

        graph.follow("ada")

        assertThat(graph.edges.value["ada"]).isEqualTo(FollowStatus.REQUESTED)
        assertThat(offersFollow("me", "ada", graph.edges.value["ada"])).isFalse()
    }

    @Test
    fun `a failed follow puts the edge back`() = runTest {
        val api = RecordingGraphApi(followFails = true)
        val graph = followGraph(api)
        graph.ensureKnown(listOf("ada"))

        graph.follow("ada")

        assertThat(graph.edges.value["ada"]).isEqualTo(FollowStatus.NONE)
    }

    @Test
    fun `an unfollow clears the edge`() = runTest {
        val api = RecordingGraphApi(follows = mutableMapOf("ada" to true))
        val graph = followGraph(api)
        graph.ensureKnown(listOf("ada"))

        graph.unfollow("ada")

        assertThat(api.unfollowRequests).containsExactly("ada")
        assertThat(graph.edges.value["ada"]).isEqualTo(FollowStatus.NONE)
    }
}
