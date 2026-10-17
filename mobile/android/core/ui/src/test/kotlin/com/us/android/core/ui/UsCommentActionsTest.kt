package com.us.android.core.ui

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.CommentNotice
import com.us.android.core.engagement.data.CommentReportStatus
import com.us.android.core.engagement.data.CommentRow
import com.us.android.core.engagement.data.CommentsUiState
import com.us.android.core.engagement.data.CommentsViewer
import com.us.android.core.engagement.data.MentionCandidate
import com.us.android.core.engagement.data.ReplyTarget
import com.us.android.core.engagement.data.ReplyThread
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The comment row actions on screen (2026-10-14, audit T-17): Reply, the
 * like heart, "View N replies", "Replying to …", the ⋮ and its rows, and
 * the `@` suggestions — state in, the right callback out.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class UsCommentActionsTest {

    @get:Rule
    val composeRule = createComposeRule()

    private class Recorder {
        val events = mutableListOf<String>()
        val callbacks = UsCommentsCallbacks(
            onDraftChange = {},
            onSubmit = {},
            onQuickReaction = {},
            onLoadMore = {},
            onRetryAppend = {},
            onRetryRefresh = {},
            onToggleReplies = { events += "toggleReplies:$it" },
            onLoadMoreReplies = { events += "moreReplies:$it" },
            onReply = { events += "reply:$it" },
            onCancelReply = { events += "cancelReply" },
            onToggleLike = { events += "like:$it" },
            onDelete = { events += "delete:$it" },
            onTogglePin = { events += "pin:$it" },
            onToggleHeart = { events += "heart:$it" },
            onReport = { id, reason, _ -> events += "report:$id:$reason" },
            onPickMention = { events += "mention:${it.username}" },
        )
    }

    private val viewer = CommentsViewer(id = "me", name = "Me")

    private fun comment(id: String, author: String = "bob", replies: Int = 0, liked: Boolean = false) = CommentRow(
        id = id,
        authorId = author,
        body = "body $id",
        createdAt = "",
        authorName = "Name $id",
        replyCount = replies,
        likeCount = 2,
        viewerLiked = liked,
    )

    private fun render(state: CommentsUiState): Recorder {
        val recorder = Recorder()
        composeRule.setContent {
            UsTheme { UsCommentsPanel(state = state, callbacks = recorder.callbacks) }
        }
        composeRule.waitForIdle()
        return recorder
    }

    @Test
    fun `Reply and the like heart reach the owner with the comment id`() {
        val recorder = render(CommentsUiState(rows = listOf(comment("c1")), viewer = viewer))

        composeRule.onNodeWithText("Reply").performClick()
        composeRule.onNodeWithContentDescription("Like comment").performClick()

        assertThat(recorder.events).containsExactly("reply:c1", "like:c1").inOrder()
    }

    @Test
    fun `a liked comment offers Unlike and shows its count`() {
        render(CommentsUiState(rows = listOf(comment("c1", liked = true)), viewer = viewer))

        composeRule.onNodeWithContentDescription("Unlike comment").assertIsDisplayed()
        composeRule.onNodeWithContentDescription("2 likes").assertIsDisplayed()
    }

    @Test
    fun `a comment with replies offers View N replies, and an open thread offers Hide`() {
        val recorder = render(CommentsUiState(rows = listOf(comment("c1", replies = 3)), viewer = viewer))
        composeRule.onNodeWithText("View 3 replies").performClick()
        assertThat(recorder.events).containsExactly("toggleReplies:c1")
    }

    @Test
    fun `an open thread lists its replies with more and hide controls`() {
        val reply = comment("r1").copy(parentId = "c1")
        render(
            CommentsUiState(
                rows = listOf(comment("c1", replies = 3)),
                viewer = viewer,
                threads = mapOf("c1" to ReplyThread(expanded = true, replies = listOf(reply), loaded = true, nextCursor = "x")),
            ),
        )

        composeRule.onNodeWithText("body r1").assertIsDisplayed()
        composeRule.onNodeWithText("View more replies").assertExists()
        composeRule.onNodeWithText("Hide replies").assertExists()
    }

    @Test
    fun `the composer says who is being answered and can cancel`() {
        val recorder = render(
            CommentsUiState(
                rows = listOf(comment("c1")),
                viewer = viewer,
                replyingTo = ReplyTarget(parentId = "c1", targetId = "c1", name = "@bob"),
            ),
        )

        composeRule.onNodeWithText("Replying to @bob").assertIsDisplayed()
        composeRule.onNodeWithContentDescription("Cancel reply").performClick()
        assertThat(recorder.events).containsExactly("cancelReply")
    }

    @Test
    fun `your own comment's menu offers Delete and asks before deleting`() {
        val recorder = render(CommentsUiState(rows = listOf(comment("c1", author = "me")), viewer = viewer))

        composeRule.onNodeWithContentDescription("Comment options").performClick()
        composeRule.onNodeWithTag("comment_menu_row:delete").performClick()
        composeRule.onNodeWithTag("comment_delete_confirm").performClick()
        composeRule.waitForIdle()

        assertThat(recorder.events).contains("delete:c1")
    }

    /** The walk (2026-10-17) found the menu still up under the confirmation; it goes first now. */
    @Test
    fun `delete takes the menu away before it asks, and cancel closes both`() {
        val recorder = render(CommentsUiState(rows = listOf(comment("c1", author = "me")), viewer = viewer))

        composeRule.onNodeWithContentDescription("Comment options").performClick()
        composeRule.onNodeWithTag("comment_menu_row:delete").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithTag("comment_delete_dialog").assertIsDisplayed()
        composeRule.onNodeWithTag("comment_menu_sheet:c1").assertDoesNotExist()
        composeRule.onNodeWithText("Cancel").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithTag("comment_delete_dialog").assertDoesNotExist()
        assertThat(recorder.events).doesNotContain("delete:c1")
    }

    @Test
    fun `the creator's menu offers Heart and Pin on another person's comment`() {
        val recorder = render(
            CommentsUiState(rows = listOf(comment("c1")), viewer = viewer, postAuthorId = "me"),
        )

        composeRule.onNodeWithContentDescription("Comment options").performClick()
        composeRule.onNodeWithTag("comment_menu_row:report").assertIsDisplayed()
        composeRule.onNodeWithTag("comment_menu_row:delete").assertDoesNotExist()
        composeRule.onNodeWithTag("comment_menu_row:pin").performClick()
        composeRule.waitForIdle()

        assertThat(recorder.events).contains("pin:c1")
    }

    @Test
    fun `report steps into the shared reasons and files a comment report`() {
        val recorder = render(CommentsUiState(rows = listOf(comment("c1")), viewer = viewer))

        composeRule.onNodeWithContentDescription("Comment options").performClick()
        composeRule.onNodeWithTag("comment_menu_row:report").performClick()
        composeRule.onNodeWithText("Why are you reporting this comment?").assertIsDisplayed()
        composeRule.onNodeWithTag("post_more_reason:spam").performScrollTo().performClick()
        composeRule.onNodeWithTag("post_more_report_submit").performScrollTo().performClick()
        composeRule.waitForIdle()

        assertThat(recorder.events).containsExactly("report:c1:spam")
    }

    @Test
    fun `a comment still being sent has no options`() {
        render(CommentsUiState(rows = listOf(comment("c1").copy(pending = true)), viewer = viewer))

        composeRule.onNodeWithContentDescription("Comment options").assertDoesNotExist()
    }

    @Test
    fun `at-suggestions replace the quick reactions and complete on a tap`() {
        val recorder = render(
            CommentsUiState(
                viewer = viewer,
                draft = "@ra",
                mentionQuery = "ra",
                mentions = listOf(MentionCandidate("u1", "raghu", "Raghu")),
            ),
        )

        composeRule.onNodeWithContentDescription("React with ❤️").assertDoesNotExist()
        composeRule.onNodeWithContentDescription("Mention @raghu").performClick()
        assertThat(recorder.events).containsExactly("mention:raghu")
    }

    @Test
    fun `labels and messages`() {
        assertThat(repliesLabel(1)).isEqualTo("View 1 reply")
        assertThat(repliesLabel(4)).isEqualTo("View 4 replies")
        assertThat(replyingToLabel("@bob")).isEqualTo("Replying to @bob")
        assertThat(replyingToLabel("")).isEqualTo("Replying to a comment")
        assertThat(commentNoticeMessage(CommentNotice.LIKE)).isEqualTo("Your like wasn't saved. Try again.")
        assertThat(CommentReportStatus.ALREADY_REPORTED.asReportState()).isEqualTo(UsPostReportState.AlreadyReported)
        assertThat(CommentReportStatus.SENDING.asReportState()).isEqualTo(UsPostReportState.Sending)
    }
}
