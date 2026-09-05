package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.model.SellerStatus
import com.us.android.feature.commerce.seller.guidance
import com.us.android.feature.commerce.seller.label
import com.us.android.feature.commerce.seller.notLiveReason
import org.junit.Test

/**
 * What the seller is told about their shop, and about each listing.
 *
 * The parsing matters as much as the copy. `GET /sellers/me` used to answer
 * `"status": ""` for every seller because the column was missing from the
 * SELECT, so a client could not tell a draft shop from an approved one — which
 * is precisely the question the whole seller surface branches on.
 */
class SellerStatusTest {

    @Test
    fun `every server status parses`() {
        val wire = listOf(
            "draft",
            "submitted",
            "under_review",
            "changes_required",
            "approved",
            "rejected",
            "suspended",
            "disabled",
        )
        for (raw in wire) {
            assertThat(SellerStatus.from(raw)).isNotEqualTo(SellerStatus.UNKNOWN)
        }
    }

    @Test
    fun `an unrecognised status is UNKNOWN and cannot sell`() {
        // A server that adds a state must not make this build crash, and must
        // not have it default to "approved" — telling a seller they can sell
        // when the server has decided otherwise is the worse of the two errors.
        for (raw in listOf("", "  ", "pending_review", "aproved", null)) {
            val parsed = SellerStatus.from(raw)
            assertThat(parsed).isEqualTo(SellerStatus.UNKNOWN)
            assertThat(parsed.canSell).isFalse()
        }
    }

    @Test
    fun `only APPROVED can sell`() {
        for (status in SellerStatus.entries) {
            assertThat(status.canSell).isEqualTo(status == SellerStatus.APPROVED)
        }
    }

    @Test
    fun `every status has copy, and none of it says approved by accident`() {
        for (status in SellerStatus.entries) {
            assertThat(status.label()).isNotEmpty()
        }
        assertThat(SellerStatus.UNKNOWN.label()).doesNotContain("Approved")
        assertThat(SellerStatus.REJECTED.label()).doesNotContain("Approved")
    }

    @Test
    fun `an approved seller gets no banner, everyone else does`() {
        // A banner explaining that an approved seller is approved is noise; a
        // missing banner on a shop that is not open is how a seller spends an
        // evening wondering why nothing sells.
        assertThat(SellerStatus.APPROVED.guidance()).isNull()
        for (status in SellerStatus.entries.filter { it != SellerStatus.APPROVED }) {
            assertThat(status.guidance()).isNotNull()
        }
    }

    // ─── Per-product state ─────────────────────────────────────────────

    private fun product(status: String, approval: String, reason: String? = null) =
        SellerProduct(
            id = "p1",
            title = "A thing",
            status = status,
            approvalStatus = approval,
            rejectionReason = reason,
            imageUrl = null,
        )

    @Test
    fun `a live product has no reason to show`() {
        assertThat(product("active", "approved").notLiveReason()).isNull()
        assertThat(product("active", "live").notLiveReason()).isNull()
    }

    @Test
    fun `a moderation rejection shows the moderator's own reason`() {
        // Reporting a generic "not on sale" here leaves the seller with no
        // idea what to change.
        assertThat(product("draft", "rejected", "Prohibited item").notLiveReason())
            .isEqualTo("Prohibited item")
    }

    @Test
    fun `a rejection with no stated reason still says it was rejected`() {
        assertThat(product("draft", "rejected").notLiveReason())
            .isEqualTo("Not approved by moderation")
    }

    @Test
    fun `approval state is reported ahead of the seller's own switch`() {
        // Both columns say something, and they answer different questions:
        // `status` is whether the seller switched it on, `approval_status` is
        // whether moderation let it through. A product that is switched on but
        // still under review must not read as "Paused" — the seller would go
        // looking for a toggle that was never the problem.
        assertThat(product("active", "submitted").notLiveReason()).isEqualTo("Awaiting review")
        assertThat(product("active", "under_review").notLiveReason()).isEqualTo("Awaiting review")
        assertThat(product("active", "draft").notLiveReason()).isEqualTo("Not submitted for review")
    }

    @Test
    fun `a paused or archived product says so`() {
        assertThat(product("paused", "approved").notLiveReason()).isEqualTo("Paused")
        assertThat(product("archived", "archived").notLiveReason()).isEqualTo("Archived")
    }

    @Test
    fun `an unrecognised combination is never reported as on sale`() {
        assertThat(product("something_new", "something_new").notLiveReason()).isNotNull()
    }
}
