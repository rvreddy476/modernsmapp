package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.feature.doorsteppro.deeplink.DigiLockerReturn
import com.us.android.feature.doorsteppro.deeplink.DigiLockerReturnPolicy
import com.us.android.feature.doorsteppro.deeplink.ProDeepLink
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinks
import com.us.android.feature.doorsteppro.deeplink.ProPushTypes
import com.us.android.feature.doorsteppro.deeplink.ReturnCheck
import org.junit.Test

/** Deep links, push routing, push channels and the DigiLocker return. */
class ProDeepLinksTest {

    private val id = "9b3f0c55-0000-4000-8000-000000000000"

    @Test
    fun `every registry deep link parses to its screen`() {
        assertThat(ProDeepLinks.parse("doorstep-pro://jobs/$id")).isEqualTo(ProDeepLink.Job(id))
        assertThat(ProDeepLinks.parse("doorstep-pro://jobs/$id/chat")).isEqualTo(ProDeepLink.JobChat(id))
        assertThat(ProDeepLinks.parse("doorstep-pro://offers/$id")).isEqualTo(ProDeepLink.Offer(id))
        assertThat(ProDeepLinks.parse("doorstep-pro://offers")).isEqualTo(ProDeepLink.Offers)
        assertThat(ProDeepLinks.parse("doorstep-pro://home")).isEqualTo(ProDeepLink.Home)
        assertThat(ProDeepLinks.parse("doorstep-pro://onboarding")).isEqualTo(ProDeepLink.Onboarding)
        assertThat(ProDeepLinks.parse("doorstep-pro://onboarding/police-certificate")).isEqualTo(ProDeepLink.PoliceCertificate)
        assertThat(ProDeepLinks.parse("doorstep-pro://account")).isEqualTo(ProDeepLink.Account)
        assertThat(ProDeepLinks.parse("doorstep-pro://earnings")).isEqualTo(ProDeepLink.Earnings)
        assertThat(ProDeepLinks.parse("  doorstep-pro://jobs/$id  ")).isEqualTo(ProDeepLink.Job(id))
    }

    @Test
    fun `anything unexpected is refused, never forwarded into navigation`() {
        listOf(
            null,
            "",
            "doorstep-pro://jobs/",
            "doorstep-pro://jobs/abc%2F..%2Fx",
            "doorstep-pro://jobs/$id/delete",
            "doorstep-pro://bookings/$id",
            "momentum://doorstep/bookings/$id",
            "https://evil.example/jobs/$id",
            "javascript:alert(1)",
            "doorstep-pro://jobs/${"a".repeat(65)}",
        ).forEach { link -> assertThat(ProDeepLinks.parse(link)).isNull() }
    }

    @Test
    fun `the DigiLocker code comes from the app link, and from the custom scheme only in dev builds`() {
        val appLink = "https://api-dev.example.app/doorstep-pro/digilocker?code=mock-female&state=s%2B1"
        val parsed = ProDeepLinks.parse(appLink) as ProDeepLink.DigiLocker
        assertThat(parsed.link).isEqualTo(DigiLockerReturn(code = "mock-female", state = "s+1", error = null, errorDescription = null))

        val custom = "doorstep-pro://digilocker?code=abc&state=s1"
        assertThat(ProDeepLinks.parse(custom, allowCustomSchemeDigiLocker = false)).isNull()
        assertThat(ProDeepLinks.parse(custom, allowCustomSchemeDigiLocker = true)).isInstanceOf(ProDeepLink.DigiLocker::class.java)
        // Another path on the same host is not the return.
        assertThat(ProDeepLinks.parse("https://api-dev.example.app/doorstep-pro/digilocker/evil?code=x&state=y")).isNull()
    }

    @Test
    fun `the code is posted only for the state this device started`() {
        val link = DigiLockerReturn(code = "c", state = "s1", error = null, errorDescription = null)
        assertThat(DigiLockerReturnPolicy.check(link, "s1")).isEqualTo(ReturnCheck.Complete(state = "s1", code = "c"))
        assertThat(DigiLockerReturnPolicy.check(link, null)).isEqualTo(ReturnCheck.NothingPending)
        assertThat(DigiLockerReturnPolicy.check(link, "other")).isEqualTo(ReturnCheck.StateMismatch)
        assertThat(DigiLockerReturnPolicy.check(link.copy(code = null), "s1")).isEqualTo(ReturnCheck.MissingCode)
        assertThat(DigiLockerReturnPolicy.check(link.copy(error = "access_denied"), "s1")).isEqualTo(ReturnCheck.Declined("access_denied"))
        // A mismatched state is refused before its error is believed.
        assertThat(DigiLockerReturnPolicy.check(link.copy(state = "x", error = "access_denied"), "s1")).isEqualTo(ReturnCheck.StateMismatch)
    }

    @Test
    fun `a tapped push opens its screen from the deep link, or from its type and ids`() {
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.OFFER_NEW, "deep_link" to "doorstep-pro://offers/$id")))
            .isEqualTo(ProDeepLink.Offer(id))
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.OFFER_NEW, "offer_id" to id))).isEqualTo(ProDeepLink.Offer(id))
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.OFFER_EXPIRED))).isEqualTo(ProDeepLink.Offers)
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.JOB_CANCELLED, "entity_id" to id))).isEqualTo(ProDeepLink.Job(id))
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.EXTRAS_APPROVED, "booking_id" to id))).isEqualTo(ProDeepLink.Job(id))
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.MESSAGE_NEW, "booking_id" to id))).isEqualTo(ProDeepLink.JobChat(id))
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.APPLICATION_APPROVED))).isEqualTo(ProDeepLink.Home)
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.DOCUMENT_REVIEWED))).isEqualTo(ProDeepLink.Onboarding)
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.ACCOUNT_SUSPENDED))).isEqualTo(ProDeepLink.Account)
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.BACKGROUND_CHECK_EXPIRING))).isEqualTo(ProDeepLink.PoliceCertificate)
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.SETTLEMENT_COMPUTED))).isEqualTo(ProDeepLink.Earnings)
        // Momentum's types and unknown keys are not the pro app's business; a push never carries a DigiLocker return.
        assertThat(ProDeepLinks.fromPush(mapOf("type" to "doorstep.booking.assigned", "booking_id" to id))).isNull()
        assertThat(ProDeepLinks.fromPush(mapOf("type" to ProPushTypes.JOB_REMINDER, "booking_id" to "../x"))).isNull()
        assertThat(
            ProDeepLinks.fromPush(
                mapOf("type" to ProPushTypes.JOB_REMINDER, "booking_id" to id, "deep_link" to "https://h/doorstep-pro/digilocker?code=c&state=s"),
            ),
        ).isEqualTo(ProDeepLink.Job(id))
    }

    @Test
    fun `every professional push type lands on a channel the pro app registers`() {
        assertThat(ProPushTypes.ALL).hasSize(17)
        val expected = mapOf(
            NotificationChannelSpec.DOORSTEP_PRO_OFFERS to setOf(ProPushTypes.OFFER_NEW, ProPushTypes.OFFER_EXPIRED),
            NotificationChannelSpec.DOORSTEP_PRO_EARNINGS to setOf(ProPushTypes.SETTLEMENT_COMPUTED),
            NotificationChannelSpec.DOORSTEP_PRO_ACCOUNT to setOf(
                ProPushTypes.APPLICATION_APPROVED, ProPushTypes.APPLICATION_REJECTED, ProPushTypes.ACCOUNT_SUSPENDED,
                ProPushTypes.ACCOUNT_REINSTATED, ProPushTypes.DOCUMENT_REVIEWED, ProPushTypes.BACKGROUND_CHECK_EXPIRING,
            ),
        )
        ProPushTypes.ALL.forEach { type ->
            val channel = NotificationChannelSpec.forType(type)
            assertThat(NotificationChannelSpec.DOORSTEP_PRO).contains(channel)
            val want = expected.entries.firstOrNull { type in it.value }?.key ?: NotificationChannelSpec.DOORSTEP_PRO_JOBS
            assertThat(channel).isEqualTo(want)
        }
        // Only offers sound the alarm.
        assertThat(NotificationChannelSpec.DOORSTEP_PRO.filter { it.alertSound }).containsExactly(NotificationChannelSpec.DOORSTEP_PRO_OFFERS)
    }
}
