package com.us.android.feature.chat.ui.home

import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.PersonSuggestion
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FollowStatus
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Founder, 24 Oct 2026: a "People you may know" card carries ONLY Follow /
 * Following (Requested for a private account) and its dismiss ×. No Message,
 * no Connect, no Add friend.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SuggestionsFollowOnlyTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val person = PersonSuggestion(
        userId = "p1",
        displayName = "Asha",
        score = 1.0,
        explainText = "",
        mutualFriendCount = 2,
        reasonCodes = emptyList(),
    )

    private fun show(edge: FollowStatus?, events: MutableList<String> = mutableListOf()) {
        composeRule.setContent {
            UsTheme {
                PersonCard(
                    person = person,
                    edge = edge,
                    busy = false,
                    onFollow = { events += "follow" },
                    onDismiss = { events += "dismiss" },
                )
            }
        }
    }

    private fun assertNoOtherAction() {
        for (word in listOf("Message", "Connect", "Connected", "Add friend", "Accept", "Decline")) {
            composeRule.onAllNodesWithText(word, substring = false, ignoreCase = true).assertCountEquals(0)
        }
        composeRule.onNodeWithTag("chat_home_person_message:p1").assertDoesNotExist()
    }

    @Test
    fun `not followed, Follow and dismiss, nothing else`() {
        val events = mutableListOf<String>()
        show(edge = null, events = events)

        composeRule.onNodeWithTag("chat_home_person_follow:p1").performClick()
        composeRule.onNodeWithTag("chat_home_person_dismiss:p1").performClick()

        assertThat(events).containsExactly("follow", "dismiss").inOrder()
        assertNoOtherAction()
        // Exactly two things to press on the card: Follow and ×.
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(2)
    }

    @Test
    fun `followed, Following and dismiss, nothing else`() {
        show(edge = FollowStatus.FOLLOWING)

        composeRule.onNodeWithText("Following").assertExists()
        composeRule.onNodeWithTag("chat_home_person_follow:p1").assertDoesNotExist()
        assertNoOtherAction()
    }

    @Test
    fun `requested, Requested and dismiss, nothing else`() {
        show(edge = FollowStatus.REQUESTED)

        composeRule.onNodeWithText("Requested").assertExists()
        assertNoOtherAction()
    }
}
