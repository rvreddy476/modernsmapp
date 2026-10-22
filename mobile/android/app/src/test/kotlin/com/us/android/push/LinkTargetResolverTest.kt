package com.us.android.push

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.model.Channel
import com.us.android.core.profile.data.UsernameLookupRepository
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * A profile or channel link names a handle; the screens take a user id
 * (2026-10-22). The resolver asks the right service for each, and answers
 * null (Search with the handle typed, in the nav host) when there is none.
 */
class LinkTargetResolverTest {

    private val usernames = mockk<UsernameLookupRepository>()
    private val channels = mockk<ChannelRepository>()
    private val resolver = LinkTargetResolver(usernames, channels)

    @Test
    fun `a profile link resolves the username through the user lookup`() = runTest {
        coEvery { usernames.userIdOf("ada") } returns "u-1"
        assertThat(resolver.userIdFor(PushDestination(PushDestinations.TYPE_PROFILE_LINK, "@ada", ""))).isEqualTo("u-1")
        coVerify(exactly = 0) { channels.channel(any()) }
    }

    @Test
    fun `a channel link resolves the handle through the channel read`() = runTest {
        coEvery { channels.channel("ada") } returns AppResult.Success(Channel(userId = "u-1", name = "Ada", handle = "ada"))
        assertThat(resolver.userIdFor(PushDestination(PushDestinations.TYPE_CHANNEL_LINK, "ada", ""))).isEqualTo("u-1")
    }

    @Test
    fun `an unknown handle, a failed read or another type answers null`() = runTest {
        coEvery { usernames.userIdOf(any()) } returns null
        coEvery { channels.channel(any()) } returns AppResult.Failure(AppError.NotFound())
        assertThat(resolver.userIdFor(PushDestination(PushDestinations.TYPE_PROFILE_LINK, "nobody", ""))).isNull()
        assertThat(resolver.userIdFor(PushDestination(PushDestinations.TYPE_CHANNEL_LINK, "nobody", ""))).isNull()
        assertThat(resolver.userIdFor(PushDestination(PushDestinations.TYPE_REEL_LINK, "ada", ""))).isNull()
        assertThat(resolver.userIdFor(PushDestination(PushDestinations.TYPE_PROFILE_LINK, " ", ""))).isNull()
    }
}
