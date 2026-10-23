package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Paid memberships' six notifications (2026-10-23): their kinds, the two deep
 * links notification-service writes (membership_notify.go — the creator's
 * `/monetization/tiers`, every member's `/memberships/{subscription_id}`),
 * the contract's `/monetization/members` and `/memberships` spellings, and
 * the fallbacks for a row whose link does not parse.
 */
class MembershipNotificationTest {

    private val sub = "00000000-0000-4000-8000-0000000ab001"

    @Test
    fun `the six membership types are kinds of their own`() {
        assertThat(NotificationKind.fromWire("membership_new_member")).isEqualTo(NotificationKind.MembershipNewMember)
        assertThat(NotificationKind.fromWire("membership_joined")).isEqualTo(NotificationKind.MembershipJoined)
        assertThat(NotificationKind.fromWire("membership_renewed")).isEqualTo(NotificationKind.MembershipRenewed)
        assertThat(NotificationKind.fromWire("membership_renewal_due")).isEqualTo(NotificationKind.MembershipRenewalDue)
        assertThat(
            NotificationKind.fromWire("membership_payment_failed")
        ).isEqualTo(NotificationKind.MembershipPaymentFailed)
        assertThat(NotificationKind.fromWire("membership_ended")).isEqualTo(NotificationKind.MembershipEnded)
        assertThat(NotificationKind.fromWire("membership_something_new"))
            .isEqualTo(NotificationKind.Unknown("membership_something_new"))
        assertThat(NotificationTarget.MEMBERSHIP_TYPES).hasSize(6)
    }

    @Test
    fun `a member's link opens that membership, and only a UUID does`() {
        assertThat(NotificationTarget.parse("/memberships/$sub")).isEqualTo(NotificationTarget.Membership(sub))
        assertThat(
            NotificationTarget.parse("/memberships/$sub?from=push")
        ).isEqualTo(NotificationTarget.Membership(sub))
        assertThat(NotificationTarget.parse("/memberships/not-a-uuid")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("/memberships/$sub/extra")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("memberships/$sub")).isEqualTo(NotificationTarget.None)
        assertThat(
            NotificationTarget.parse("https://cleestudio.com/memberships/$sub")
        ).isEqualTo(NotificationTarget.None)
    }

    @Test
    fun `the list and the creator's page each have their link`() {
        assertThat(NotificationTarget.parse("/memberships")).isEqualTo(NotificationTarget.MyMemberships)
        assertThat(NotificationTarget.parse("/monetization/tiers")).isEqualTo(NotificationTarget.CreatorMemberships)
        assertThat(NotificationTarget.parse("/monetization/members")).isEqualTo(NotificationTarget.CreatorMemberships)
        assertThat(NotificationTarget.parse("/monetization/payouts")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("/monetization")).isEqualTo(NotificationTarget.None)
    }

    @Test
    fun `with no usable link the creator's notice opens the page and a member's opens the membership`() {
        assertThat(NotificationTarget.membershipFallback("membership_new_member", sub))
            .isEqualTo(NotificationTarget.CreatorMemberships)
        NotificationTarget.MEMBER_NOTICE_TYPES.forEach { type ->
            assertThat(
                NotificationTarget.membershipFallback(type, " $sub ")
            ).isEqualTo(NotificationTarget.Membership(sub))
            // An entity that is not a subscription id still lands somewhere useful: the list.
            assertThat(NotificationTarget.membershipFallback(type, "")).isEqualTo(NotificationTarget.MyMemberships)
        }
        assertThat(NotificationTarget.membershipFallback("comment", sub)).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.membershipOf("x")).isEqualTo(NotificationTarget.None)
    }
}
