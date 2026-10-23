package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.notifications.data.NotificationDto
import com.us.android.core.notifications.data.toDomain
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Test
import java.io.File

/**
 * Paid memberships' six inbox rows (2026-10-23) against notification-service's
 * golden file (`internal/http/testdata/contracts/notifications_inbox_memberships.json`),
 * copied byte for byte into `src/test/resources/contracts`.
 *
 * The creator's new-member row opens their Memberships page
 * (`/monetization/tiers`); every member row opens that one membership
 * (`/memberships/{subscription_id}`), titled by the server — "a creator"
 * where the creator's name is unknown.
 */
class MembershipNotificationsFixtureTest {

    private val app = NetworkModule.provideJson()
    private val raw = File("src/test/resources/contracts/notifications_inbox_memberships.json").readText()
    private val rows = app.decodeFromString(ApiEnvelope.serializer(ListSerializer(NotificationDto.serializer())), raw)
        .data.orEmpty().map { it.toDomain() }
    private val membership = NotificationTarget.Membership("00000000-0000-4000-8000-0000000ab001")

    @Test
    fun `the six types read as their kinds, in the server's order`() {
        assertThat(rows.map { it.kind }).containsExactly(
            NotificationKind.MembershipNewMember,
            NotificationKind.MembershipJoined,
            NotificationKind.MembershipRenewed,
            NotificationKind.MembershipRenewalDue,
            NotificationKind.MembershipPaymentFailed,
            NotificationKind.MembershipEnded,
        ).inOrder()
    }

    @Test
    fun `the creator's notice opens Memberships and every member notice opens the membership`() {
        assertThat(rows.first().target).isEqualTo(NotificationTarget.CreatorMemberships)
        assertThat(rows.drop(1).map { it.target }.distinct()).containsExactly(membership)
    }

    @Test
    fun `the rows are titled by the server, named or not`() {
        assertThat(rows[0].title).isEqualTo("Call UserB joined your membership")
        assertThat(rows[0].actorName).isEqualTo("Call UserB")
        assertThat(rows[1].title).isEqualTo("You're a member of Call UserB")
        // No actor block: the name is unknown, and the server's own words say "a creator".
        assertThat(rows[2].actorName).isEmpty()
        assertThat(rows[2].title).isEqualTo("Your membership with a creator was renewed")
        assertThat(rows[5].title).isEqualTo("Your Call UserB membership has ended")
    }

    @Test
    fun `a row whose link does not parse still lands on its membership, by type and entity`() {
        val dto = app.decodeFromString(ApiEnvelope.serializer(ListSerializer(NotificationDto.serializer())), raw)
            .data.orEmpty()
        val unlinked = dto.map { it.copy(deepLink = "") }.map { it.toDomain().target }
        assertThat(unlinked.first()).isEqualTo(NotificationTarget.CreatorMemberships)
        assertThat(unlinked.drop(1).distinct()).containsExactly(membership)
    }
}
