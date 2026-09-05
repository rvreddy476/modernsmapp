package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CommunityRules
import org.junit.Test

/** Pins the form rules to the contract: handle `^[a-z0-9_]{3,30}$`, name ≤ 60, description ≤ 300, body ≤ 2000. */
class CommunityRulesTest {

    @Test
    fun `a well-formed handle passes`() {
        assertThat(CommunityRules.handleProblem("riders_01")).isNull()
        assertThat(CommunityRules.isHandleValid("abc")).isTrue()
        assertThat(CommunityRules.isHandleValid("a".repeat(30))).isTrue()
    }

    @Test
    fun `a short, long, uppercase or punctuated handle is refused`() {
        assertThat(CommunityRules.handleProblem("ab")).isNotNull()
        assertThat(CommunityRules.handleProblem("a".repeat(31))).isNotNull()
        assertThat(CommunityRules.handleProblem("Riders")).isNotNull()
        assertThat(CommunityRules.handleProblem("ride-rs")).isNotNull()
        assertThat(CommunityRules.handleProblem("")).isNotNull()
    }

    @Test
    fun `typed text is coerced toward a legal handle`() {
        assertThat(CommunityRules.normaliseHandle("Weekend Riders!")).isEqualTo("weekend_riders")
        assertThat(CommunityRules.normaliseHandle("a".repeat(40))).hasLength(30)
    }

    @Test
    fun `the name must be present and at most sixty characters`() {
        assertThat(CommunityRules.nameProblem("")).isNotNull()
        assertThat(CommunityRules.nameProblem("Riders")).isNull()
        assertThat(CommunityRules.nameProblem("n".repeat(60))).isNull()
        assertThat(CommunityRules.nameProblem("n".repeat(61))).isNotNull()
    }

    @Test
    fun `the description may be empty and is capped at three hundred`() {
        assertThat(CommunityRules.descriptionProblem("")).isNull()
        assertThat(CommunityRules.descriptionProblem("d".repeat(300))).isNull()
        assertThat(CommunityRules.descriptionProblem("d".repeat(301))).isNotNull()
    }

    @Test
    fun `an update body must be present and is capped at two thousand`() {
        assertThat(CommunityRules.bodyProblem("   ")).isNotNull()
        assertThat(CommunityRules.bodyProblem("b".repeat(2000))).isNull()
        assertThat(CommunityRules.bodyProblem("b".repeat(2001))).isNotNull()
    }

    @Test
    fun `the group description shares the community cap`() {
        assertThat(CommunityRules.GROUP_DESCRIPTION_MAX).isEqualTo(300)
    }
}
