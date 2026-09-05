package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.InviteFailure
import com.us.android.core.chat.data.InviteLink
import com.us.android.core.chat.data.InviteLinkState
import com.us.android.core.chat.data.InvitePreview
import com.us.android.core.chat.data.inviteCodeFrom
import com.us.android.core.chat.data.inviteLinkState
import com.us.android.core.chat.data.invitePreviewState
import com.us.android.core.chat.data.toInviteFailure
import com.us.android.core.common.error.AppError
import org.junit.Test
import java.time.Instant

/**
 * Pins the invite-link vocabulary: what an owner's link is (live, expired,
 * exhausted), what a viewer's preview is (member, not live), how a pasted
 * link yields its code, and how the contract's error codes read.
 */
class InviteLinkStateTest {

    private val now: Instant = Instant.parse("2026-09-05T12:00:00Z")

    private fun link(expiresAt: String? = null, maxUses: Int = 0, uses: Int = 0) =
        InviteLink("abc123", "https://atpost.app/chat/join/abc123", "conv-1", expiresAt, maxUses, uses)

    private fun preview(isLive: Boolean = true, isMember: Boolean = false, expiresAt: String? = null) =
        InvitePreview("abc123", "conv-1", "Riders", "", null, 12, expiresAt, isLive, isMember)

    @Test
    fun `a link with no expiry and no cap is live`() {
        assertThat(inviteLinkState(link(), now)).isEqualTo(InviteLinkState.Live)
    }

    @Test
    fun `a link past its expiry is expired even with uses left`() {
        assertThat(inviteLinkState(link(expiresAt = "2026-09-01T00:00:00Z", maxUses = 10, uses = 1), now))
            .isEqualTo(InviteLinkState.Expired)
    }

    @Test
    fun `a link that reached its cap is exhausted`() {
        assertThat(inviteLinkState(link(maxUses = 5, uses = 5), now)).isEqualTo(InviteLinkState.Exhausted)
    }

    @Test
    fun `a link under its cap and before its expiry is live`() {
        assertThat(inviteLinkState(link(expiresAt = "2026-09-30T00:00:00Z", maxUses = 5, uses = 4), now))
            .isEqualTo(InviteLinkState.Live)
    }

    @Test
    fun `an unparseable expiry does not kill the link`() {
        assertThat(inviteLinkState(link(expiresAt = "soon"), now)).isEqualTo(InviteLinkState.Live)
    }

    @Test
    fun `a preview for a member says member before anything else`() {
        assertThat(invitePreviewState(preview(isLive = false, isMember = true), now))
            .isEqualTo(InviteLinkState.Member)
    }

    @Test
    fun `a preview the server calls not live is not live`() {
        assertThat(invitePreviewState(preview(isLive = false), now)).isEqualTo(InviteLinkState.NotLive)
    }

    @Test
    fun `a preview whose expiry has passed is expired`() {
        assertThat(invitePreviewState(preview(expiresAt = "2026-09-04T00:00:00Z"), now))
            .isEqualTo(InviteLinkState.Expired)
    }

    @Test
    fun `a live preview is live`() {
        assertThat(invitePreviewState(preview(expiresAt = "2026-09-06T00:00:00Z"), now))
            .isEqualTo(InviteLinkState.Live)
    }

    @Test
    fun `the code is read out of a full link, a trailing slash, a query, or a bare code`() {
        assertThat(inviteCodeFrom("https://atpost.app/chat/join/abc123")).isEqualTo("abc123")
        assertThat(inviteCodeFrom("https://atpost.app/chat/join/abc123/")).isEqualTo("abc123")
        assertThat(inviteCodeFrom("https://atpost.app/chat/join/abc-123?ref=x")).isEqualTo("abc-123")
        assertThat(inviteCodeFrom("  abc_123 ")).isEqualTo("abc_123")
    }

    @Test
    fun `text that is not a code yields nothing`() {
        assertThat(inviteCodeFrom("")).isNull()
        assertThat(inviteCodeFrom("hello world")).isNull()
        assertThat(inviteCodeFrom("https://atpost.app/chat/join/")).isNull()
    }

    @Test
    fun `the contract's codes map to the vocabulary`() {
        assertThat(AppError.NotFound().toInviteFailure()).isEqualTo(InviteFailure.NotFound)
        assertThat(AppError.Unknown("INVITE_NOT_LIVE", 410).toInviteFailure()).isEqualTo(InviteFailure.NotLive)
        assertThat(AppError.Unknown(null, 410).toInviteFailure()).isEqualTo(InviteFailure.NotLive)
        assertThat(AppError.Forbidden("JOIN_NOT_ALLOWED").toInviteFailure()).isEqualTo(InviteFailure.NotAllowed)
        assertThat(AppError.Unknown("GROUP_FULL", 409).toInviteFailure()).isEqualTo(InviteFailure.GroupFull)
        assertThat(AppError.Timeout().toInviteFailure()).isEqualTo(InviteFailure.Other)
    }
}
