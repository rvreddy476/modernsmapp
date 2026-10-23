package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.ui.edit.PostEditUiState
import com.us.android.core.feed.ui.edit.gatePatch
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * The Edit sheet's "Members only" (paid memberships, 2026-10-23): read from
 * the owner's detail (post-service posts.go:131), offered when the owner has
 * an active level (or the post is still gated on one since turned off), and
 * saved in the same PATCH (post_edit.go:97): a level id sets the gate, `""`
 * clears it, and an unchanged gate sends nothing.
 */
class MembersOnlyEditTest {

    private val json = NetworkModule.provideJson()
    private val level = MembershipLevel("t1", "me", "Supporter", 9_900, emptyList(), 0, isActive = true)

    @Test
    fun `the owner's detail says which level the post is gated on`() {
        val gated = json.decodeFromString(PostEditDetailDto.serializer(), """{"id":"p1","tier_required_id":"t1"}""")
        val open = json.decodeFromString(PostEditDetailDto.serializer(), """{"id":"p1"}""")
        assertThat(gated.tierRequiredId).isEqualTo("t1")
        assertThat(open.tierRequiredId).isNull()
    }

    @Test
    fun `the picker is offered with a level, or on a post still gated, and a level change alone is dirty`() {
        assertThat(PostEditUiState().offersMembersOnly).isFalse()
        assertThat(PostEditUiState(levels = listOf(level)).offersMembersOnly).isTrue()
        assertThat(PostEditUiState(gateBase = "old-level").offersMembersOnly).isTrue()

        val unchanged = PostEditUiState(levels = listOf(level), gateBase = "t1", gate = "t1")
        assertThat(unchanged.dirty).isFalse()
        assertThat(gatePatch(unchanged).isEmpty).isTrue()
        assertThat(unchanged.copy(gate = null).dirty).isTrue()
    }

    @Test
    fun `the PATCH sets a level by id, clears with an empty string, and leaves an unchanged gate out`() {
        val open = PostEditUiState(levels = listOf(level), gateBase = null, gate = null)
        val set = gatePatch(open.copy(gate = "t1"))
        val cleared = gatePatch(open.copy(gateBase = "t1", gate = null))

        assertThat(json.encodeToString(PostEditPatch.serializer(), set)).isEqualTo("""{"tier_required_id":"t1"}""")
        assertThat(json.encodeToString(PostEditPatch.serializer(), cleared)).isEqualTo("""{"tier_required_id":""}""")
        assertThat(json.encodeToString(PostEditPatch.serializer(), gatePatch(open))).isEqualTo("{}")
        // Merged with a text edit, both ride one request.
        val both = PostEditPatch(title = "New").then(set)
        assertThat(both.title).isEqualTo("New")
        assertThat(both.tierRequiredId).isEqualTo("t1")
    }
}
