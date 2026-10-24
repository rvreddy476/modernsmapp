package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** A suggestion row's one button (founder, 24 Oct 2026): Follow, or the state a follow left. */
class SuggestionButtonTest {

    @Test
    fun `the button follows the edge, and unknown reads as not followed`() {
        assertThat(suggestionButton(null)).isEqualTo(SuggestionButton.FOLLOW)
        assertThat(suggestionButton(FollowStatus.NONE)).isEqualTo(SuggestionButton.FOLLOW)
        assertThat(suggestionButton(FollowStatus.FOLLOWING)).isEqualTo(SuggestionButton.FOLLOWING)
        assertThat(suggestionButton(FollowStatus.REQUESTED)).isEqualTo(SuggestionButton.REQUESTED)
    }

    @Test
    fun `there is no Message, Connect or Add friend button, and only Follow is pressed`() {
        assertThat(SuggestionButton.entries.map { it.label }).containsExactly("Follow", "Following", "Requested").inOrder()
        assertThat(SuggestionButton.entries.filter { it.tappable }).containsExactly(SuggestionButton.FOLLOW)
    }
}
