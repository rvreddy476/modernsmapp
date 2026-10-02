package com.us.android.feature.live.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.feature.live.data.ChatRole
import com.us.android.feature.live.data.LiveChatAuthorDto
import com.us.android.feature.live.data.LiveGateAction
import com.us.android.feature.live.data.LiveProgressDto
import com.us.android.feature.live.data.LiveRequirementDto
import com.us.android.feature.live.data.RequirementState
import org.junit.Test

/**
 * Protects the words of the "not yet" screen and of chat authorship
 * (live-eligibility contract, 2026-10-02): each requirement in plain words,
 * with its progress; the button labels; the role tags; and that a person the
 * moderation sheets list is named, never shown as a piece of an id.
 */
class LiveGateWordsTest {

    private fun line(requirement: LiveRequirementDto): RequirementLine = requirementLines(listOf(requirement)).single()

    @Test
    fun `the contract's examples read as written`() {
        assertThat(line(LiveRequirementDto(key = "phone_verified", met = false)).text)
            .isEqualTo("Verify your phone number")
        assertThat(
            line(LiveRequirementDto(key = "account_age", met = false, current = 2, needed = 7, unit = "days")).text,
        ).isEqualTo("Your account must be 7 days old (5 days to go)")
        assertThat(
            line(
                LiveRequirementDto(
                    key = "activity",
                    met = false,
                    posts = LiveProgressDto(current = 1, needed = 3),
                    followers = LiveProgressDto(current = 4, needed = 10),
                ),
            ).text,
        ).isEqualTo("Publish 3 posts or reach 10 followers (1 of 3 posts, 4 of 10 followers)")
    }

    @Test
    fun `the email row reads verified, to verify, or could not be checked`() {
        assertThat(line(LiveRequirementDto(key = "email_verified", met = true)).text).isEqualTo("Email verified")
        assertThat(line(LiveRequirementDto(key = "email_verified", met = false)).text)
            .isEqualTo("Verify your email address")
        assertThat(line(LiveRequirementDto(key = "email_verified", met = null)).text)
            .isEqualTo("Email address: we couldn't check this just now")
        assertThat(line(LiveRequirementDto(key = "email_verified")).state).isEqualTo(RequirementState.Unknown)
    }

    @Test
    fun `Learn more asks for a verified email address, not a phone number`() {
        assertThat(LIVE_GATE_LEARN_MORE).contains("a verified email address")
        assertThat(LIVE_GATE_LEARN_MORE.lowercase()).doesNotContain("phone")
    }

    @Test
    fun `account age counts what is left, in the server's unit, singular for one`() {
        fun age(current: Int, needed: Int, unit: String = "days") =
            line(LiveRequirementDto(key = "account_age", met = false, current = current, needed = needed, unit = unit))
                .text

        assertThat(age(6, 7)).isEqualTo("Your account must be 7 days old (1 day to go)")
        assertThat(age(0, 1)).isEqualTo("Your account must be 1 day old (1 day to go)")
        assertThat(age(100, 168, unit = "hours")).isEqualTo("Your account must be 168 hours old (68 hours to go)")
        // No unit sent: days. Nothing left by the numbers: no "to go".
        assertThat(age(2, 7, unit = "")).isEqualTo("Your account must be 7 days old (5 days to go)")
        assertThat(age(7, 7)).isEqualTo("Your account must be 7 days old")
        // No numbers at all (Go's zero values): no invented figure.
        assertThat(age(0, 0)).isEqualTo("Your account needs to be a little older")
    }

    @Test
    fun `activity names only the paths the server offers`() {
        fun activity(posts: LiveProgressDto?, followers: LiveProgressDto?) =
            line(LiveRequirementDto(key = "activity", met = false, posts = posts, followers = followers)).text

        assertThat(activity(LiveProgressDto(0, 1), LiveProgressDto(0, 1)))
            .isEqualTo("Publish 1 post or reach 1 follower (0 of 1 post, 0 of 1 follower)")
        assertThat(activity(LiveProgressDto(1, 3), null)).isEqualTo("Publish 3 posts (1 of 3 posts)")
        assertThat(activity(null, LiveProgressDto(4, 10))).isEqualTo("Reach 10 followers (4 of 10 followers)")
        assertThat(activity(LiveProgressDto(1, 0), LiveProgressDto(4, 0)))
            .isEqualTo("Publish a few posts or gain some followers")
        assertThat(activity(null, null)).isEqualTo("Publish a few posts or gain some followers")
    }

    @Test
    fun `every known requirement has a met line, a needed line and a could-not-check line`() {
        val keys = listOf("email_verified", "phone_verified", "adult", "account_age", "activity", "good_standing")

        for (key in keys) {
            val met = line(LiveRequirementDto(key = key, met = true))
            val unmet = line(LiveRequirementDto(key = key, met = false))
            val unknown = line(LiveRequirementDto(key = key, met = null))

            assertThat(met.state).isEqualTo(RequirementState.Met)
            assertThat(unmet.state).isEqualTo(RequirementState.Unmet)
            assertThat(unknown.state).isEqualTo(RequirementState.Unknown)
            assertThat(setOf(met.text, unmet.text, unknown.text)).hasSize(3)
            assertThat(unknown.text).endsWith("we couldn't check this just now")
            listOf(met, unmet, unknown).forEach { assertThat(it.text).isNotEmpty() }
        }
    }

    @Test
    fun `a requirement this build does not know is listed while unmet and silent once met`() {
        assertThat(requirementLines(listOf(LiveRequirementDto(key = "something_new", met = true)))).isEmpty()
        assertThat(line(LiveRequirementDto(key = "something_new", met = false)).text)
            .isEqualTo("One more requirement isn't met yet")
        assertThat(line(LiveRequirementDto(key = "something_new", met = null)).text)
            .isEqualTo("One requirement: we couldn't check this just now")
    }

    @Test
    fun `the rows keep the server's order`() {
        val lines = requirementLines(
            listOf(
                LiveRequirementDto(key = "good_standing", met = true),
                LiveRequirementDto(key = "phone_verified", met = false),
                LiveRequirementDto(key = "adult", met = true),
            ),
        )

        assertThat(lines.map { it.key }).containsExactly("good_standing", "phone_verified", "adult").inOrder()
    }

    @Test
    fun `each action has its own button label`() {
        assertThat(gateActionLabel(LiveGateAction.CreatePost)).isEqualTo("Create a post")
        assertThat(gateActionLabel(LiveGateAction.VerifyEmail)).isEqualTo("Verify email")
        assertThat(gateActionLabel(LiveGateAction.VerifyPhone)).isEqualTo("Verify phone number")
        assertThat(gateActionLabel(LiveGateAction.CheckAgain)).isEqualTo("Check again")
    }

    @Test
    fun `the viewer cap note appears only while a cap applies`() {
        assertThat(viewerCapNote(200)).isEqualTo("Your first streams can have up to 200 viewers at a time.")
        assertThat(viewerCapNote(0)).isNull()
        assertThat(viewerCapNote(-1)).isNull()
    }

    @Test
    fun `a requirement the server could not check is a retryable refusal, not a ban`() {
        val refusal = goLiveRefusal(AppError.Server(statusCode = 503, code = "AUTHORITY_UNAVAILABLE"))

        assertThat(refusal.canRetry).isTrue()
        assertThat(refusal.message).isEqualTo("We couldn't check your account just now. Try again in a moment.")
    }

    @Test
    fun `a full stream says so, and is not read as a ban`() {
        assertThat(watchJoinFailure(AppError.Forbidden(code = "STREAM_FULL")))
            .isEqualTo("This stream is full right now. Try again in a little while.")
        assertThat(watchJoinFailure(AppError.Forbidden(code = "BANNED_FROM_STREAM")))
            .isEqualTo("You can't watch this stream.")
    }

    @Test
    fun `host and moderators are tagged, a viewer is not`() {
        assertThat(chatRoleLabel(ChatRole.Host)).isEqualTo("Host")
        assertThat(chatRoleLabel(ChatRole.Moderator)).isEqualTo("Mod")
        assertThat(chatRoleLabel(ChatRole.Viewer)).isNull()
    }

    @Test
    fun `a listed person is named from the chat, and never by a piece of the id`() {
        val id = "5f0c2a9e-1111-2222"
        val people = mapOf(id to LiveChatAuthorDto(userId = id, handle = "asha"))

        assertThat(personName(id, people)).isEqualTo("@asha")
        assertThat(personName(id, emptyMap())).isEqualTo("Viewer")
        assertThat(personName(id, emptyMap())).doesNotContain("5f0c2a")
    }
}
