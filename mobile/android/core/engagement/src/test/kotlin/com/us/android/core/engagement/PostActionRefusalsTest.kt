package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.engagement.data.REACTION_NOT_ALLOWED
import com.us.android.core.engagement.data.REPOST_NOT_ALLOWED
import com.us.android.core.engagement.data.isPostActionRefusal
import com.us.android.core.engagement.data.postActionRefusalText
import org.junit.Test

/** post-service's 400 REACTION_NOT_ALLOWED / REPOST_NOT_ALLOWED (commit 8370981c4) as plain sentences. */
class PostActionRefusalsTest {

    @Test
    fun `the two refusals are plain sentences, whatever error class carries the code`() {
        val like = "Posts take a like only. Tap the heart to like it."
        val repost = "Reels cannot be reposted. Share the link instead."
        assertThat(postActionRefusalText(AppError.Unknown(code = REACTION_NOT_ALLOWED, statusCode = 400))).isEqualTo(like)
        assertThat(postActionRefusalText(AppError.Unknown(code = REPOST_NOT_ALLOWED, statusCode = 400))).isEqualTo(repost)
        assertThat(postActionRefusalText(AppError.Forbidden(code = REPOST_NOT_ALLOWED))).isEqualTo(repost)
        assertThat(postActionRefusalText(AppError.Server(statusCode = 500, code = REACTION_NOT_ALLOWED))).isEqualTo(like)
        assertThat(postActionRefusalText(AppError.NotFound(code = REPOST_NOT_ALLOWED))).isEqualTo(repost)
        assertThat(REACTION_NOT_ALLOWED).isEqualTo("REACTION_NOT_ALLOWED")
        assertThat(REPOST_NOT_ALLOWED).isEqualTo("REPOST_NOT_ALLOWED")
    }

    @Test
    fun `anything else is not a refusal, so the caller keeps its own words and Retry`() {
        assertThat(postActionRefusalText(AppError.NoNetwork())).isNull()
        assertThat(postActionRefusalText(AppError.Unknown(code = "INTERNAL_ERROR", statusCode = 500))).isNull()
        assertThat(isPostActionRefusal(AppError.NoNetwork())).isFalse()
        assertThat(isPostActionRefusal(AppError.Unknown(code = REPOST_NOT_ALLOWED, statusCode = 400))).isTrue()
    }
}
