package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * Protects the go-live gate's decisions (live-eligibility contract,
 * 2026-10-02): which of the form, the pilot notice and the "not yet" list an
 * eligibility answer opens, which button helps, and how the same list is read
 * out of a refused create or start.
 */
class LiveEligibilityTest {

    private val json = NetworkModule.provideJson()

    private fun requirement(key: String, met: Boolean?) = LiveRequirementDto(key = key, met = met)

    @Test
    fun `eligible opens the form, with the viewer cap when one applies`() {
        assertThat(liveGateOf(LiveEligibilityDto(mode = "open", eligible = true)))
            .isEqualTo(LiveGate.Open(viewerCap = 0))
        assertThat(liveGateOf(LiveEligibilityDto(mode = "open", eligible = true, viewerCap = 200)))
            .isEqualTo(LiveGate.Open(viewerCap = 200))
    }

    @Test
    fun `eligible wins over unmet requirements - the pilot list is always eligible`() {
        val answer = LiveEligibilityDto(
            mode = "pilot",
            eligible = true,
            requirements = listOf(requirement(REQ_ACTIVITY, false)),
        )

        assertThat(liveGateOf(answer)).isEqualTo(LiveGate.Open())
    }

    @Test
    fun `not eligible in the pilot is the pilot notice, by the flag or by the mode`() {
        val unmet = listOf(requirement(REQ_ACTIVITY, false))

        assertThat(liveGateOf(LiveEligibilityDto(mode = "pilot", pilotOnly = true, requirements = unmet)))
            .isEqualTo(LiveGate.PilotOnly)
        assertThat(liveGateOf(LiveEligibilityDto(mode = "pilot", requirements = unmet)))
            .isEqualTo(LiveGate.PilotOnly)
        assertThat(liveGateOf(LiveEligibilityDto(mode = "open", pilotOnly = true, requirements = unmet)))
            .isEqualTo(LiveGate.PilotOnly)
    }

    @Test
    fun `not eligible in open mode lists every requirement, in the server's order`() {
        val requirements = listOf(
            requirement(REQ_PHONE_VERIFIED, true),
            requirement(REQ_ADULT, true),
            requirement(REQ_ACCOUNT_AGE, false),
            requirement(REQ_ACTIVITY, false),
            requirement(REQ_GOOD_STANDING, null),
        )

        val gate = liveGateOf(LiveEligibilityDto(mode = "open", requirements = requirements)) as LiveGate.NotYet

        assertThat(gate.requirements).isEqualTo(requirements)
        assertThat(gate.action).isEqualTo(LiveGateAction.CreatePost)
    }

    @Test
    fun `not eligible with nothing to explain opens the form - the server still decides`() {
        assertThat(liveGateOf(LiveEligibilityDto(mode = "open"))).isEqualTo(LiveGate.Open())
        assertThat(liveGateOf(LiveEligibilityDto())).isEqualTo(LiveGate.Open())
        assertThat(
            liveGateOf(LiveEligibilityDto(mode = "open", requirements = listOf(requirement(REQ_ADULT, true)))),
        ).isEqualTo(LiveGate.Open())
        // A row with no key says nothing.
        assertThat(liveGateOf(LiveEligibilityDto(mode = "open", requirements = listOf(requirement("", false)))))
            .isEqualTo(LiveGate.Open())
    }

    @Test
    fun `the button helps the first unmet requirement the user can act on`() {
        val table = listOf(
            listOf(requirement(REQ_PHONE_VERIFIED, false), requirement(REQ_ACTIVITY, false)) to
                LiveGateAction.VerifyPhone,
            listOf(requirement(REQ_ACTIVITY, false), requirement(REQ_PHONE_VERIFIED, false)) to
                LiveGateAction.CreatePost,
            // Account age has no action: the next one that has is offered.
            listOf(requirement(REQ_ACCOUNT_AGE, false), requirement(REQ_ACTIVITY, false)) to
                LiveGateAction.CreatePost,
            // A met or an unknown requirement is not something to act on.
            listOf(requirement(REQ_PHONE_VERIFIED, true), requirement(REQ_ACTIVITY, false)) to
                LiveGateAction.CreatePost,
            listOf(requirement(REQ_PHONE_VERIFIED, null), requirement(REQ_ACTIVITY, false)) to
                LiveGateAction.CreatePost,
            // Nothing to do but wait, or nothing could be checked: ask again.
            listOf(requirement(REQ_ACCOUNT_AGE, false), requirement(REQ_ADULT, false)) to
                LiveGateAction.CheckAgain,
            listOf(requirement(REQ_PHONE_VERIFIED, null)) to LiveGateAction.CheckAgain,
            listOf(requirement("something_new", false)) to LiveGateAction.CheckAgain,
        )

        for ((requirements, expected) in table) {
            assertThat(gateActionFor(requirements)).isEqualTo(expected)
        }
    }

    @Test
    fun `met is read as true, false, or unknown when null or absent`() {
        assertThat(requirement("k", true).state).isEqualTo(RequirementState.Met)
        assertThat(requirement("k", false).state).isEqualTo(RequirementState.Unmet)
        assertThat(requirement("k", null).state).isEqualTo(RequirementState.Unknown)
        assertThat(LiveRequirementDto(key = "k").state).isEqualTo(RequirementState.Unknown)
    }

    @Test
    fun `a refused create carries the list in details requirements`() {
        val error = AppError.Forbidden(
            code = CODE_LIVE_NOT_ELIGIBLE,
            details = mapOf(
                "requirements" to """[{"key":"phone_verified","met":false},{"key":"adult","met":null}]""",
            ),
        )

        val gate = notYetFromRefusal(error, json)

        assertThat(gate?.requirements?.map { it.key to it.met })
            .containsExactly("phone_verified" to false, "adult" to null).inOrder()
        assertThat(gate?.action).isEqualTo(LiveGateAction.VerifyPhone)
    }

    @Test
    fun `any other refusal, or one with nothing readable, has no list`() {
        val requirements = mapOf("requirements" to """[{"key":"activity","met":false}]""")

        assertThat(notYetFromRefusal(AppError.Forbidden(CODE_LIVE_NOT_ENABLED, requirements), json)).isNull()
        assertThat(notYetFromRefusal(AppError.Forbidden(CODE_LIVE_NOT_ELIGIBLE), json)).isNull()
        assertThat(notYetFromRefusal(AppError.Forbidden(CODE_LIVE_NOT_ELIGIBLE, mapOf("requirements" to "")), json))
            .isNull()
        assertThat(
            notYetFromRefusal(AppError.Forbidden(CODE_LIVE_NOT_ELIGIBLE, mapOf("requirements" to "not json")), json),
        ).isNull()
        assertThat(notYetFromRefusal(AppError.Forbidden(CODE_LIVE_NOT_ELIGIBLE, mapOf("requirements" to "[]")), json))
            .isNull()
        assertThat(notYetFromRefusal(AppError.NoNetwork(), json)).isNull()
    }
}
