package com.us.android.feature.post

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.MembersOnly
import com.us.android.core.model.Post
import com.us.android.core.model.PostCounts
import com.us.android.core.model.PostMediaRef
import com.us.android.core.model.PostViewerState
import com.us.android.feature.post.createhub.ReelPublishViewModel
import com.us.android.feature.post.ui.postIsLocked
import org.junit.Test

/**
 * Members only in `:feature:post` (paid memberships, 2026-10-23): the post
 * page draws the lock for a body the server withheld, and the reel / video
 * form hands the worker only a level the creator still offers.
 */
class MembersOnlyPostTest {

    private fun post(membersOnly: MembersOnly, text: String = "", media: List<PostMediaRef> = emptyList()) = Post(
        id = "p1",
        authorId = "a1",
        text = text,
        visibility = "public",
        postType = "text",
        createdAt = "",
        counts = PostCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = PostViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        allowsComments = true,
        allowsReactions = true,
        isRepostable = false,
        isPinned = false,
        media = media,
        membersOnly = membersOnly,
    )

    @Test
    fun `a redacted post is the lock, a member's post and an open post are not`() {
        assertThat(postIsLocked(post(MembersOnly(tierRequiredId = "t1", bodyRedacted = true)))).isTrue()
        assertThat(postIsLocked(post(MembersOnly(tierRequiredId = "t1")))).isTrue()
        assertThat(postIsLocked(post(MembersOnly(tierRequiredId = "t1"), text = "for members"))).isFalse()
        assertThat(
            postIsLocked(post(MembersOnly(tierRequiredId = "t1"), media = listOf(PostMediaRef("m1", "image"))))
        ).isFalse()
        assertThat(postIsLocked(post(MembersOnly.None))).isFalse()
    }

    @Test
    fun `the record carries the picked level only while the creator offers it`() {
        val offered = ReelPublishViewModel.ReelUiState(
            videoUri = "content://v",
            membersLevels = listOf(level("tier-1")),
            membersLevelId = "tier-1",
        )
        assertThat(ReelPublishViewModel.buildPending(offered, "key-1", null).membersLevelId).isEqualTo("tier-1")
        assertThat(
            ReelPublishViewModel.buildPending(offered.copy(membersLevels = emptyList()), "key-1", null).membersLevelId
        )
            .isNull()
        assertThat(ReelPublishViewModel.buildPending(offered.copy(membersLevelId = null), "key-1", null).membersLevelId)
            .isNull()
    }
}
