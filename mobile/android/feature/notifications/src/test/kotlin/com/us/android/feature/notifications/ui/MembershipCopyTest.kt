package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import org.junit.Test

/**
 * The membership rows (paid memberships, 2026-10-23): the server's inbox
 * title first (notification-service inbox_copy.go), and the same words from
 * the type when a row has none — "a creator" when the name is unknown,
 * never "Your  membership".
 */
class MembershipCopyTest {

    private fun row(kind: NotificationKind, actorName: String, title: String? = null) = Notification(
        id = "n1",
        bucket = 1,
        ts = "ts",
        kind = kind,
        actorUserId = "u1",
        actorName = actorName,
        entityType = "membership",
        entityId = SUB,
        target = NotificationTarget.Membership(SUB),
        isRead = false,
        createdAt = "2026-10-23T10:00:00Z",
        title = title,
    )

    @Test
    fun `the server's title is the row`() {
        assertThat(row(NotificationKind.MembershipJoined, "Asha", title = "You're a member of Asha").describe())
            .isEqualTo("You're a member of Asha")
    }

    @Test
    fun `without a title each type says its own sentence, naming the creator`() {
        assertThat(
            row(NotificationKind.MembershipNewMember, "Ravi").describe()
        ).isEqualTo("Ravi joined your membership")
        assertThat(row(NotificationKind.MembershipJoined, "Asha").describe()).isEqualTo("You're a member of Asha")
        assertThat(
            row(NotificationKind.MembershipRenewed, "Asha").describe()
        ).isEqualTo("Your Asha membership was renewed")
        assertThat(row(NotificationKind.MembershipRenewalDue, "Asha").describe())
            .isEqualTo("Your Asha membership ends soon — renew to keep access")
        assertThat(row(NotificationKind.MembershipPaymentFailed, "Asha").describe())
            .isEqualTo("Your Asha membership payment didn't go through")
        assertThat(row(NotificationKind.MembershipEnded, "Asha").describe()).isEqualTo("Your Asha membership has ended")
    }

    @Test
    fun `an unnamed creator is a creator, and an unnamed member is Someone`() {
        assertThat(
            row(NotificationKind.MembershipRenewed, "").describe()
        ).isEqualTo("Your membership with a creator was renewed")
        assertThat(
            row(NotificationKind.MembershipEnded, "").describe()
        ).isEqualTo("Your membership with a creator has ended")
        assertThat(row(NotificationKind.MembershipNewMember, "").describe()).isEqualTo("Someone joined your membership")
        listOf(
            NotificationKind.MembershipJoined,
            NotificationKind.MembershipRenewed,
            NotificationKind.MembershipRenewalDue,
            NotificationKind.MembershipPaymentFailed,
            NotificationKind.MembershipEnded,
        ).forEach { kind -> assertThat(row(kind, "").describe()).doesNotContain("  ") }
    }

    private companion object {
        const val SUB = "00000000-0000-4000-8000-0000000ab001"
    }
}
