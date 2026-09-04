package com.us.android.core.ui

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.CommentsUiState
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * State → UI for the shared comments panel — the rules the founder's
 * Instagram reference sets and a reader of the source cannot verify:
 *
 *  - the send control EXISTS only once there is a draft (not disabled: absent);
 *  - a quick-reaction tap reaches the owner with that emoji;
 *  - the empty state is the invitation, not the old "be the first" copy.
 *
 * Robolectric on the unit source set so it runs on the gate's
 * `testDebugUnitTest`, like [PostMediaAccessibilityTest].
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class UsCommentsPanelTest {

    @get:Rule
    val composeRule = createComposeRule()

    private class Recorder {
        val reactions = mutableListOf<String>()
        var submits = 0
        val callbacks = UsCommentsCallbacks(
            onDraftChange = {},
            onSubmit = { submits++ },
            onQuickReaction = { reactions += it },
            onLoadMore = {},
            onRetryAppend = {},
            onRetryRefresh = {},
        )
    }

    private fun render(state: CommentsUiState): Recorder {
        val recorder = Recorder()
        composeRule.setContent {
            UsTheme { UsCommentsPanel(state = state, callbacks = recorder.callbacks) }
        }
        composeRule.waitForIdle()
        return recorder
    }

    @Test
    fun `no send control while the draft is empty`() {
        render(CommentsUiState(draft = ""))

        composeRule.onNodeWithContentDescription("Send comment").assertDoesNotExist()
    }

    @Test
    fun `the send control appears with a draft and sends it`() {
        val recorder = render(CommentsUiState(draft = "hello"))

        composeRule.onNodeWithContentDescription("Send comment")
            .assertIsDisplayed()
            .assertIsEnabled()
            .performClick()

        assertThat(recorder.submits).isEqualTo(1)
    }

    /** Whitespace shows the disc but cannot send: the control does not blink. */
    @Test
    fun `a blank draft shows a disabled send rather than none`() {
        render(CommentsUiState(draft = "   "))

        composeRule.onNodeWithContentDescription("Send comment").assertIsNotEnabled()
    }

    @Test
    fun `a quick reaction tap hands the emoji to the owner`() {
        val recorder = render(CommentsUiState())

        composeRule.onNodeWithContentDescription("React with ❤️").performClick()

        assertThat(recorder.reactions).containsExactly("❤️")
    }

    @Test
    fun `every quick reaction is offered in order`() {
        render(CommentsUiState())

        QUICK_REACTIONS.forEach { emoji ->
            composeRule.onNodeWithContentDescription("React with $emoji").assertIsDisplayed()
        }
        assertThat(QUICK_REACTIONS).containsExactly("❤️", "🙌", "🔥", "👏", "😢", "😍", "😮", "😂").inOrder()
    }

    @Test
    fun `the empty state invites rather than reports`() {
        render(CommentsUiState())

        composeRule.onNodeWithText("No comments yet").assertIsDisplayed()
        composeRule.onNodeWithText("Start the conversation.").assertIsDisplayed()
        composeRule.onNodeWithText("Be the first to say something.").assertDoesNotExist()
    }

    @Test
    fun `no attachment controls exist because the API has none`() {
        render(CommentsUiState(draft = "hi"))

        composeRule.onNodeWithContentDescription("Add image").assertDoesNotExist()
        composeRule.onNodeWithContentDescription("Add GIF").assertDoesNotExist()
    }

    @Test
    fun `the send rule follows the draft not its validity`() {
        assertThat(CommentsUiState(draft = "").showsSend()).isFalse()
        assertThat(CommentsUiState(draft = " ").showsSend()).isTrue()
        assertThat(CommentsUiState(draft = "x").showsSend()).isTrue()
    }
}
