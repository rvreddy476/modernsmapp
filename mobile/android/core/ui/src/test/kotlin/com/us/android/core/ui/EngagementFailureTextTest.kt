package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementFailure
import org.junit.Test

/** The feed's failure bar: the server's refusal in its own sentence, else the action's words. */
class EngagementFailureTextTest {

    @Test
    fun `a refused like or repost says why`() {
        assertThat(
            engagementFailureText(EngagementFailure("p", EngagementAction.REACTION, AppError.Unknown("REACTION_NOT_ALLOWED", 400))),
        ).isEqualTo("Posts take a like only. Tap the heart to like it.")
        assertThat(
            engagementFailureText(EngagementFailure("p", EngagementAction.REPOST, AppError.Unknown("REPOST_NOT_ALLOWED", 400))),
        ).isEqualTo("Reels cannot be reposted. Share the link instead.")
    }

    @Test
    fun `any other failure names the action`() {
        assertThat(engagementFailureText(EngagementFailure("p", EngagementAction.REACTION, AppError.NoNetwork())))
            .isEqualTo("Couldn't save your like.")
        assertThat(engagementFailureText(EngagementFailure("p", EngagementAction.REPOST, AppError.NoNetwork())))
            .isEqualTo("Couldn't repost that.")
    }
}
