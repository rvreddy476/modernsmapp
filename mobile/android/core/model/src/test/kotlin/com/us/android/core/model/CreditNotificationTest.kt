package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Video credits' two notifications (2026-10-23): their kinds, the requests deep link, and the fallbacks. */
class CreditNotificationTest {

    @Test
    fun `the two credit types are kinds of their own`() {
        assertThat(NotificationKind.fromWire("credit_request")).isEqualTo(NotificationKind.CreditRequest)
        assertThat(NotificationKind.fromWire("credit_accepted")).isEqualTo(NotificationKind.CreditAccepted)
    }

    @Test
    fun `the requests deep link opens the requests screen, with or without a query`() {
        assertThat(NotificationTarget.parse("/credits/requests")).isEqualTo(NotificationTarget.CreditRequests)
        assertThat(NotificationTarget.parse("/credits/requests?from=push")).isEqualTo(NotificationTarget.CreditRequests)
        assertThat(NotificationTarget.parse("/credits")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("credits/requests")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("/credits/other")).isEqualTo(NotificationTarget.None)
    }

    @Test
    fun `with no usable link a request opens the screen and an acceptance opens the video`() {
        assertThat(NotificationTarget.creditFallback("credit_request", "p1")).isEqualTo(NotificationTarget.CreditRequests)
        assertThat(NotificationTarget.creditFallback("credit_accepted", " p1 ")).isEqualTo(NotificationTarget.Video("p1"))
        assertThat(NotificationTarget.creditFallback("credit_accepted", "")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.creditFallback("comment", "p1")).isEqualTo(NotificationTarget.None)
    }

    @Test
    fun `an acceptance linked to the watch page opens the video`() {
        assertThat(NotificationTarget.parse("/tube/watch/p1")).isEqualTo(NotificationTarget.Video("p1"))
    }

    @Test
    fun `status reads from the wire, and an unknown status is no status`() {
        assertThat(CreditStatus.fromWire("pending")).isEqualTo(CreditStatus.PENDING)
        assertThat(CreditStatus.fromWire(" Accepted ")).isEqualTo(CreditStatus.ACCEPTED)
        assertThat(CreditStatus.fromWire("declined")).isEqualTo(CreditStatus.DECLINED)
        assertThat(CreditStatus.fromWire("unlinked")).isEqualTo(CreditStatus.UNLINKED)
        assertThat(CreditStatus.fromWire("weird")).isNull()
        assertThat(CreditStatus.fromWire(null)).isNull()
    }

    @Test
    fun `credits with nothing in them are empty`() {
        assertThat(VideoCredits.None.isEmpty).isTrue()
        assertThat(VideoCredits(releaseYear = 2024).isEmpty).isFalse()
        assertThat(VideoCredits(productionHouse = "  ").isEmpty).isTrue()
    }
}
