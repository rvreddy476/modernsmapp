package com.us.android.feature.notifications.permission

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * When to ask for POST_NOTIFICATIONS — Slice D, D-D2.
 *
 * ## WHY THIS RULE IS TESTED RATHER THAN INLINED
 *
 * The permission was declared in the manifest and never requested, so on
 * Android 13+ every push this platform sent was dropped by the system before it
 * reached the app. Everything else was wired. Getting the *request* right is
 * therefore the whole difference between working notifications and silence.
 *
 * And the rule is easy to write wrongly, because the platform hands you an
 * ambiguous signal — see the rationale cases below.
 */
class NotificationPermissionPolicyTest {

    private fun decide(
        sdkInt: Int = 34,
        granted: Boolean = false,
        asked: Boolean = false,
        rationale: Boolean = false,
    ) = NotificationPermissionPolicy.decide(
        sdkInt = sdkInt,
        isGranted = granted,
        hasAskedBefore = asked,
        shouldShowRationale = rationale,
    )

    // ── The platform floor ──────────────────────────────────────────────

    /**
     * Below Android 13 the permission does not exist and notifications are
     * allowed by default. Requesting it there resolves as DENIED, which would
     * make a working install look broken and could route a fine user into the
     * "turned off" state for no reason.
     */
    @Test
    fun `below android 13 there is nothing to request`() {
        for (sdk in listOf(26, 30, 31, 32)) {
            assertThat(decide(sdkInt = sdk)).isEqualTo(NotificationPermissionAction.None)
        }
    }

    @Test
    fun `android 13 is the first version that needs it`() {
        assertThat(decide(sdkInt = 33)).isEqualTo(NotificationPermissionAction.Request)
        assertThat(NotificationPermissionPolicy.FIRST_SDK_REQUIRING_PERMISSION).isEqualTo(33)
    }

    // ── Granted ─────────────────────────────────────────────────────────

    @Test
    fun `an already granted permission asks for nothing`() {
        assertThat(decide(granted = true)).isEqualTo(NotificationPermissionAction.None)
        // Even if the app has asked before and the platform still offers a
        // rationale, granted is granted.
        assertThat(decide(granted = true, asked = true, rationale = true))
            .isEqualTo(NotificationPermissionAction.None)
    }

    // ── THE RATIONALE TRAP ──────────────────────────────────────────────

    /**
     * `shouldShowRequestPermissionRationale` is FALSE in two opposite states:
     * before the first ask, and after a permanent denial. These two tests are
     * the same platform input producing opposite correct answers, which is why
     * the app persists its own "asked" flag.
     *
     * Never asked, no rationale → ASK. This is a brand-new install.
     */
    @Test
    fun `a first-time user is asked`() {
        assertThat(decide(asked = false, rationale = false))
            .isEqualTo(NotificationPermissionAction.Request)
    }

    /**
     * Asked before, no rationale → the door is shut. Requesting again shows no
     * dialog at all: the callback fires immediately as denied and nothing
     * appears on screen. Only Settings can change it now.
     */
    @Test
    fun `a permanently denied user is sent to settings, not asked again`() {
        assertThat(decide(asked = true, rationale = false))
            .isEqualTo(NotificationPermissionAction.DirectToSettings)
    }

    /**
     * Asked once, declined once, still askable — the system WILL show the
     * dialog again, and by now the user has seen enough of the app to judge.
     */
    @Test
    fun `a user who declined once but can still be asked is asked again`() {
        assertThat(decide(asked = true, rationale = true))
            .isEqualTo(NotificationPermissionAction.Request)
    }

    /**
     * The odd combination — never asked, but the platform reports a rationale —
     * should still ask. It can occur after the app's data is cleared while the
     * OS-level grant state survives, and asking is harmless: the system decides
     * whether a dialog actually appears.
     */
    @Test
    fun `a rationale without a recorded ask still asks`() {
        assertThat(decide(asked = false, rationale = true))
            .isEqualTo(NotificationPermissionAction.Request)
    }

    /**
     * The full matrix, so a future edit cannot quietly change one cell.
     *
     * Pre-13 is None throughout — including the states that would otherwise
     * send someone to Settings, because there is nothing there to turn on.
     */
    @Test
    fun `the whole decision table`() {
        data class Case(
            val sdk: Int,
            val granted: Boolean,
            val asked: Boolean,
            val rationale: Boolean,
            val expected: NotificationPermissionAction,
        )

        val cases = listOf(
            Case(30, false, false, false, NotificationPermissionAction.None),
            Case(30, false, true, false, NotificationPermissionAction.None),
            Case(30, true, true, true, NotificationPermissionAction.None),
            Case(33, true, false, false, NotificationPermissionAction.None),
            Case(33, false, false, false, NotificationPermissionAction.Request),
            Case(33, false, true, true, NotificationPermissionAction.Request),
            Case(33, false, true, false, NotificationPermissionAction.DirectToSettings),
            Case(34, false, false, false, NotificationPermissionAction.Request),
            Case(34, false, true, false, NotificationPermissionAction.DirectToSettings),
            Case(36, false, true, false, NotificationPermissionAction.DirectToSettings),
        )

        for (case in cases) {
            assertThat(
                NotificationPermissionPolicy.decide(
                    sdkInt = case.sdk,
                    isGranted = case.granted,
                    hasAskedBefore = case.asked,
                    shouldShowRationale = case.rationale,
                ),
            ).isEqualTo(case.expected)
        }
    }
}
