package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.MessageDecision
import com.us.android.core.chat.data.MessagePermission
import com.us.android.core.common.error.AppError
import org.junit.Test

/** DM consent (contract §4): the profile's Message button from graph-service's decision and reason. */
class MessageButtonTest {

    private fun deny(reason: String?) = MessagePermission(MessageDecision.Deny, reason)

    @Test
    fun `allow is Message, and so is a decision not read yet`() {
        assertThat(messageButtonFor(MessagePermission(MessageDecision.Allow), "@ana"))
            .isEqualTo(MessageButtonState.Enabled(MessageButtonKind.Message))
        assertThat(messageButtonFor(null, "@ana")).isEqualTo(MessageButtonState.Enabled(MessageButtonKind.Message))
    }

    @Test
    fun `a request is Send request, or Requested when the viewer's is already waiting`() {
        val request = MessagePermission(MessageDecision.Request)
        assertThat((messageButtonFor(request, "@ana") as MessageButtonState.Enabled).label).isEqualTo("Send request")
        assertThat((messageButtonFor(request, "@ana", hasPendingRequest = true) as MessageButtonState.Enabled).label)
            .isEqualTo("Requested")
    }

    @Test
    fun `each deny reason is a disabled Message with its own words`() {
        fun line(reason: String) = (messageButtonFor(deny(reason), "@ana") as MessageButtonState.Disabled).explanation
        assertThat(line("messages_off")).isEqualTo("@ana isn't taking messages")
        assertThat(line("no_one")).isEqualTo("@ana doesn't accept messages")
        assertThat(line("followers_only")).isEqualTo("Follow @ana to send a request")
        assertThat(line("people_i_follow")).isEqualTo("@ana only accepts messages from people they follow")
        assertThat(line("you_paused")).isEqualTo("Turn your messages on to send")
        assertThat((messageButtonFor(deny("no_one"), "@ana") as MessageButtonState.Disabled).label).isEqualTo("Message")
    }

    @Test
    fun `unavailable hides the button, so a block is never revealed`() {
        assertThat(messageButtonFor(deny("unavailable"), "@ana")).isEqualTo(MessageButtonState.Hidden)
    }

    @Test
    fun `a deny without a reason still explains, plainly`() {
        assertThat((messageButtonFor(deny(null), "@ana") as MessageButtonState.Disabled).explanation)
            .isEqualTo("You can't message @ana right now")
    }

    @Test
    fun `the handle is the username, else the name`() {
        assertThat(profileHandle("ana", "Ana R")).isEqualTo("@ana")
        assertThat(profileHandle("@ana", "Ana R")).isEqualTo("@ana")
        assertThat(profileHandle("", "Ana R")).isEqualTo("Ana R")
    }

    @Test
    fun `a refused start is explained by its reason, the rate limit and the cooldown`() {
        assertThat(startRefusalText(AppError.Forbidden("MESSAGING_NOT_ALLOWED", mapOf("reason" to "no_one")), "@ana"))
            .isEqualTo("@ana doesn't accept messages")
        assertThat(startRefusalText(AppError.Forbidden("MESSAGING_NOT_ALLOWED"), "@ana"))
            .isEqualTo("@ana isn't accepting messages from you.")
        assertThat(consentRefusalOrNull(AppError.RateLimited(retryAfterSeconds = null), "@ana"))
            .isEqualTo("You've sent a lot of requests today. Try again tomorrow.")
        val cooldown = consentRefusalOrNull(AppError.Forbidden("REQUEST_COOLDOWN"), "@ana")
        assertThat(cooldown).isEqualTo("You can't send @ana another request yet. Try again in a few days.")
        // The requester is never told their request was deleted.
        assertThat(cooldown).doesNotContain("delete")
        assertThat(consentRefusalOrNull(AppError.NoNetwork(), "@ana")).isNull()
    }

    @Test
    fun `a limiter outage asks for a moment, anything else is a plain failure`() {
        assertThat(startFailureText(AppError.Server(statusCode = 503, code = "RETRY_LATER"))).isEqualTo("Try again in a moment.")
        assertThat(consentRefusalOrNull(AppError.Server(statusCode = 503, code = "RETRY_LATER"), "@ana")).isNull()
        assertThat(startFailureText(AppError.NoNetwork())).isEqualTo("We couldn't open that conversation.")
    }
}
