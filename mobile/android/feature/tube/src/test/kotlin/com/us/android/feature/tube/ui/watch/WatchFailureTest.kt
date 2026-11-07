package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/**
 * Why a video could not be opened, said so the viewer can act on it (audit
 * L5, 2026-11-07): the age gate's three codes each have their own words,
 * and only a missing date of birth offers a way on.
 */
class WatchFailureTest {

    @Test
    fun `a signed-out viewer is asked to sign in`() {
        val failed = watchFailure(AppError.AuthFailed(code = CODE_AGE_SIGN_IN))

        assertThat(failed).isEqualTo(WatchContent.Failed(AGE_SIGN_IN))
    }

    @Test
    fun `a viewer under 18 is told the video is not for their account, with nothing to do`() {
        val failed = watchFailure(AppError.Forbidden(code = CODE_AGE_RESTRICTED))

        assertThat(failed).isEqualTo(WatchContent.Failed(AGE_RESTRICTED))
        assertThat(failed.action).isNull()
    }

    @Test
    fun `a viewer with no date of birth is offered the way to confirm it`() {
        val failed = watchFailure(AppError.Forbidden(code = CODE_AGE_UNVERIFIED))

        assertThat(failed.message).isEqualTo(AGE_UNVERIFIED)
        assertThat(failed.action).isEqualTo(WatchFailureAction.CONFIRM_BIRTH_DATE)
    }

    @Test
    fun `offline says offline`() {
        assertThat(watchFailure(AppError.NoNetwork()).message).isEqualTo(OFFLINE)
        assertThat(watchFailure(AppError.Timeout()).message).isEqualTo(OFFLINE)
    }

    @Test
    fun `anything else keeps the generic sentence`() {
        assertThat(watchFailure(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR")))
            .isEqualTo(WatchContent.Failed(LOAD_FAILED))
        assertThat(watchFailure(AppError.AuthFailed())).isEqualTo(WatchContent.Failed(LOAD_FAILED))
        assertThat(watchFailure(AppError.NotFound())).isEqualTo(WatchContent.Failed(LOAD_FAILED))
        assertThat(watchFailure(null)).isEqualTo(WatchContent.Failed(LOAD_FAILED))
    }
}
