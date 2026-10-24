package com.us.android.feature.feed.ui

import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.isEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FollowStatus
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Founder, 24 Oct 2026: Home's People you may know shows ONLY Follow /
 * Following (Requested for a private account) per person, plus the dismiss.
 * No Message, no Connect, no Add friend. A tap on the row opens the profile.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class PeopleYouMayKnowFollowOnlyTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val people = listOf(
        SuggestedPerson(userId = "a", name = "Asha", username = "asha", avatarMediaId = null, line = "2 mutual friends"),
        SuggestedPerson(userId = "b", name = "Bo", username = "bo", avatarMediaId = null, line = "@bo"),
        SuggestedPerson(userId = "c", name = "Cy", username = "cy", avatarMediaId = null, line = "@cy"),
    )

    @Test
    fun `each person carries one follow-state button and a dismiss, nothing else`() {
        val events = mutableListOf<String>()
        composeRule.setContent {
            UsTheme {
                PeopleYouMayKnowPage(
                    people = people,
                    edges = mapOf("b" to FollowStatus.FOLLOWING, "c" to FollowStatus.REQUESTED),
                    topInset = 0.dp,
                    onOpenProfile = { events += "open:$it" },
                    onFollow = { events += "follow:$it" },
                    onHide = { events += "hide:$it" },
                )
            }
        }

        composeRule.onAllNodesWithText("Follow").assertCountEquals(1)
        composeRule.onAllNodesWithText("Following").assertCountEquals(1)
        composeRule.onAllNodesWithText("Requested").assertCountEquals(1)
        for (word in listOf("Message", "Connect", "Connected", "Add friend", "Accept", "Decline")) {
            composeRule.onAllNodesWithText(word, ignoreCase = true).assertCountEquals(0)
        }
        // Per person: the row (opens the profile), Follow when not followed, and ×.
        // Following / Requested are drawn disabled, so they are not presses.
        composeRule.onAllNodes(hasClickAction() and isEnabled()).assertCountEquals(people.size * 2 + 1)

        composeRule.onNodeWithText("Follow").performClick()
        composeRule.onNodeWithContentDescription("Not interested in Asha").performClick()
        composeRule.onNodeWithTag("feed_suggestion:b").performClick()

        assertThat(events).containsExactly("follow:a", "hide:a", "open:b").inOrder()
    }
}
