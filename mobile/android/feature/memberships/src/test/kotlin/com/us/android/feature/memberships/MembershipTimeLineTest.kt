package com.us.android.feature.memberships

import com.google.common.truth.Truth.assertThat
import com.us.android.core.memberships.data.LevelChange
import com.us.android.core.memberships.data.MembershipStatus
import com.us.android.core.memberships.data.MyMembership
import com.us.android.feature.memberships.mine.membershipDate
import com.us.android.feature.memberships.mine.sentence
import com.us.android.feature.memberships.mine.timeLine
import org.junit.Test
import java.time.ZoneId

/** The member's list and detail say when access ends, in the viewer's zone, in words (2026-10-23). */
class MembershipTimeLineTest {

    private val ist = ZoneId.of("Asia/Kolkata")

    private fun membership(status: MembershipStatus, accessUntil: String?) = MyMembership(
        subscriptionId = "sub-1",
        creatorId = "c1",
        levelId = "t1",
        levelName = "Supporter",
        pricePaise = 9_900,
        status = status,
        currentPeriodEnd = "2026-11-22T20:00:00Z",
        accessUntil = accessUntil,
        renewAvailable = true,
    )

    @Test
    fun `dates are the viewer's day, and anything unparseable is shown as sent`() {
        // 20:00 UTC on the 22nd is the 23rd in India.
        assertThat(membershipDate("2026-11-22T20:00:00Z", ist)).isEqualTo("23 Nov 2026")
        assertThat(membershipDate("", ist)).isEmpty()
        assertThat(membershipDate(null, ist)).isEmpty()
        assertThat(membershipDate("soon", ist)).isEqualTo("soon")
    }

    @Test
    fun `each status says what it means for access`() {
        val until = "2026-11-22T20:00:00Z"
        assertThat(
            membership(MembershipStatus.Active, until).timeLine(ist)
        ).isEqualTo("Access until 23 Nov 2026 · renew by then")
        assertThat(membership(MembershipStatus.CancelledAtPeriodEnd, until).timeLine(ist))
            .isEqualTo("Cancelled · access until 23 Nov 2026")
        assertThat(
            membership(MembershipStatus.Grace, until).timeLine(ist)
        ).isEqualTo("Renew by 23 Nov 2026 to keep access")
        assertThat(
            membership(MembershipStatus.PendingPayment, null).timeLine(ist)
        ).isEqualTo("Waiting for your payment")
        assertThat(membership(MembershipStatus.Expired, null).timeLine(ist)).isEqualTo("Ended 23 Nov 2026")
        // A status that grants nothing never reads as access, even with a date.
        assertThat(membership(MembershipStatus.Expired, until).timeLine(ist)).isEqualTo("Ended 23 Nov 2026")
    }

    @Test
    fun `a level change with nothing to pay says what happened`() {
        assertThat(LevelChange.Upgraded.sentence()).isEqualTo("You're on the new level now.")
        assertThat(LevelChange.Unchanged.sentence()).isEqualTo("You're already on that level.")
        assertThat(LevelChange.Scheduled("").sentence()).isEqualTo("Your new level starts next month.")
        assertThat(LevelChange.Scheduled("2026-11-22T20:00:00Z").sentence()).contains("Nothing is charged now.")
    }
}
