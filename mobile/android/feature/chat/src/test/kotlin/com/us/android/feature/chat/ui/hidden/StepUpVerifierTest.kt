package com.us.android.feature.chat.ui.hidden

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.hidden.StepUpOutcome
import com.us.android.core.chat.hidden.StepUpStart
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The hidden-chats reset's "Confirm it's you" (founder, 10 Nov 2026): an
 * emailed-code account is mailed a code when the step opens and answers with
 * the challenge and the code; an authenticator account is unchanged.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class StepUpVerifierTest {

    private val starts = ArrayDeque<StepUpStart>()
    private var begins = 0
    private var resends = 0
    private val emailVerifies = mutableListOf<Pair<String, String>>()
    private val otpVerifies = mutableListOf<String>()
    private var emailAnswer: StepUpOutcome = StepUpOutcome.Ok("su-e")

    private fun verifier(scope: CoroutineScope) = StepUpVerifier(
        scope = scope,
        begin = {
            begins++
            starts.removeFirst()
        },
        sendAgain = {
            resends++
            starts.removeFirst()
        },
        verifyEmail = { challenge, code ->
            emailVerifies += challenge to code
            emailAnswer
        },
        verifyAuthenticator = { otp ->
            otpVerifies += otp
            StepUpOutcome.Ok("su-t")
        },
    )

    private fun TestScope.scope() = CoroutineScope(UnconfinedTestDispatcher(testScheduler))

    private fun TestScope.waitOut(seconds: Int) {
        advanceTimeBy(seconds * 1_000L)
        runCurrent()
    }

    @Test
    fun `an emailed-code account is mailed a code and verifies with the challenge`() = runTest {
        starts += StepUpStart.Email("ch-1", "r***@gmail.com")
        val v = verifier(scope())

        v.open()
        assertThat(v.state.value.method).isEqualTo(StepUpMethod.Email)
        assertThat(v.state.value.emailMasked).isEqualTo("r***@gmail.com")
        assertThat(v.state.value.resendInSeconds).isEqualTo(StepUpVerifyState.RESEND_COOLDOWN_SECONDS)

        assertThat(v.verify("123456")).isEqualTo(StepUpOutcome.Ok("su-e"))
        assertThat(emailVerifies.single()).isEqualTo("ch-1" to "123456")
        assertThat(otpVerifies).isEmpty()
    }

    @Test
    fun `an authenticator account sends the otp as before`() = runTest {
        starts += StepUpStart.Authenticator
        val v = verifier(scope())

        v.open()
        assertThat(v.state.value.method).isEqualTo(StepUpMethod.Authenticator)
        assertThat(v.state.value.canResend).isFalse()

        assertThat(v.verify("654321")).isEqualTo(StepUpOutcome.Ok("su-t"))
        assertThat(otpVerifies.single()).isEqualTo("654321")
        assertThat(emailVerifies).isEmpty()
    }

    @Test
    fun `no two-step verification offers neither code`() = runTest {
        starts += StepUpStart.NoTwoFactor
        val v = verifier(scope())

        v.open()

        assertThat(v.state.value.method).isEqualTo(StepUpMethod.None)
    }

    @Test
    fun `Resend waits for the timer and swaps in the new challenge`() = runTest {
        starts += StepUpStart.Email("ch-1", "r***@gmail.com")
        starts += StepUpStart.Email("ch-2", "r***@gmail.com")
        val v = verifier(scope())
        v.open()

        v.resend()
        assertThat(resends).isEqualTo(0)
        waitOut(60)
        assertThat(v.state.value.canResend).isTrue()

        v.resend()
        assertThat(resends).isEqualTo(1)
        assertThat(v.state.value.notice).isEqualTo(StepUpNotice.CodeSent)
        assertThat(v.state.value.resendInSeconds).isEqualTo(StepUpVerifyState.RESEND_COOLDOWN_SECONDS)

        v.verify("111111")
        assertThat(emailVerifies.single().first).isEqualTo("ch-2")
    }

    @Test
    fun `a 429 holds Resend for its Retry-After`() = runTest {
        starts += StepUpStart.RateLimited(25)
        val v = verifier(scope())

        v.open()

        assertThat(v.state.value.method).isEqualTo(StepUpMethod.Email)
        assertThat(v.state.value.notice).isEqualTo(StepUpNotice.TooSoon)
        assertThat(v.state.value.resendInSeconds).isEqualTo(25)
    }

    @Test
    fun `a wrong code and a spent challenge have their own notices`() = runTest {
        starts += StepUpStart.Email("ch-1", "")
        val v = verifier(scope())
        v.open()

        emailAnswer = StepUpOutcome.WrongCode
        v.verify("000000")
        assertThat(v.state.value.notice).isEqualTo(StepUpNotice.WrongCode)

        emailAnswer = StepUpOutcome.ChallengeExpired
        v.verify("000000")
        assertThat(v.state.value.notice).isEqualTo(StepUpNotice.CodeExpired)

        // The spent challenge is forgotten: nothing more is sent until a new code.
        assertThat(v.verify("123456")).isEqualTo(StepUpOutcome.ChallengeExpired)
        assertThat(emailVerifies).hasSize(2)
    }

    @Test
    fun `a failed send stays on the email path with Resend open`() = runTest {
        starts += StepUpStart.EmailUnavailable
        val v = verifier(scope())

        v.open()

        assertThat(v.state.value.notice).isEqualTo(StepUpNotice.EmailUnavailable)
        assertThat(v.state.value.canResend).isTrue()
    }

    @Test
    fun `closing forgets the challenge`() = runTest {
        starts += StepUpStart.Email("ch-1", "")
        val v = verifier(scope())
        v.open()

        v.close()

        assertThat(v.verify("123456")).isEqualTo(StepUpOutcome.ChallengeExpired)
        assertThat(emailVerifies).isEmpty()
    }
}
