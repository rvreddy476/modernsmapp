package com.us.android.push

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Dating pushes route by type, and a message push trusts only the deep link's match id. */
class PushDestinationsDatingTest {

    private fun target(type: String, entityId: String = "", deepLink: String = "") =
        PushDestinations.datingTargetOf(PushDestination(type, entityId, deepLink))

    @Test
    fun `a spark opens incoming sparks`() {
        assertThat(target("dating.spark.created", "spark-1", "/dating/sparks/incoming?spark_id=spark-1"))
            .isEqualTo(DatingPushTarget.IncomingSparks)
    }

    @Test
    fun `a formed match opens that match, from the deep link or the entity id`() {
        assertThat(target("dating.match.formed", "m-entity", "/dating/matches/m-1"))
            .isEqualTo(DatingPushTarget.Match("m-1", openChat = false))
        assertThat(target("dating.match.formed", "m-2", ""))
            .isEqualTo(DatingPushTarget.Match("m-2", openChat = false))
        assertThat(target("dating.match.formed", " ", "")).isNull()
    }

    @Test
    fun `a message opens the match's chat, never a conversation id taken for a match id`() {
        assertThat(target("dating.match.new_message", "conversation-9", "/dating/matches/m-3#latest"))
            .isEqualTo(DatingPushTarget.Match("m-3", openChat = true))
        // entity_id may be a conversation id: without a deep link there is nothing safe to open.
        assertThat(target("dating.match.new_message", "conversation-9", "")).isNull()
        assertThat(target("dating.match.first_message", "", "/dating/matches/m-4"))
            .isEqualTo(DatingPushTarget.Match("m-4", openChat = true))
    }

    @Test
    fun `a scam alert opens the safety page`() {
        assertThat(target("dating_scam_alert", "m-1", "/dating/safety")).isEqualTo(DatingPushTarget.Safety)
    }

    @Test
    fun `a date check-in opens the match with its sheet`() {
        assertThat(target("dating_date_checkin", "m-entity", "/dating/matches/m-5?checkin=1"))
            .isEqualTo(DatingPushTarget.Match("m-5", openChat = false, checkIn = true))
        assertThat(target("dating_date_checkin", "m-6", ""))
            .isEqualTo(DatingPushTarget.Match("m-6", openChat = false, checkIn = true))
        assertThat(target("dating_date_checkin", " ", "")).isNull()
    }

    @Test
    fun `other types and malformed links route nowhere`() {
        assertThat(target("dm", "c-1", "/dating/matches/m-1")).isNull()
        assertThat(PushDestinations.datingMatchIdOf("/dating/matches/")).isNull()
        assertThat(PushDestinations.datingMatchIdOf("/dating/matches/m-1/extra")).isNull()
        assertThat(PushDestinations.datingMatchIdOf("/chat/matches/m-1")).isNull()
        assertThat(PushDestinations.datingMatchIdOf(null)).isNull()
    }

    // ── Dating audit H1: a trusted contact's safety pushes ──────────────────

    private val alertId = "0b6f3c1e-8a2d-4c7e-9f10-2a3b4c5d6e7f"
    private val shareId = "5d6e7f0b-6f3c-4c7e-8a2d-9f102a3b4c1e"

    private fun safetyId(link: String?) = datingSafetyIdOf(link, PushDestinations.ALERTS_SEGMENT)

    @Test
    fun `a panic alert opens that alert, by the id in its deep link`() {
        assertThat(target(PANIC, alertId, "/dating/safety/alerts/$alertId"))
            .isEqualTo(DatingPushTarget.PanicAlert(alertId))
        // Query and fragment are ignored; an upper-case id is the same id.
        assertThat(target(PANIC, "", "/dating/safety/alerts/${alertId.uppercase()}?from=push#top"))
            .isEqualTo(DatingPushTarget.PanicAlert(alertId))
        // No deep link: the entity id, which notification-service sets to the same incident id.
        assertThat(target(PANIC, alertId, ""))
            .isEqualTo(DatingPushTarget.PanicAlert(alertId))
    }

    @Test
    fun `a shared location opens that share`() {
        assertThat(target(SHARED, shareId, "/dating/safety/shared-locations/$shareId"))
            .isEqualTo(DatingPushTarget.SharedLocation(shareId))
        assertThat(target(SHARED, shareId, ""))
            .isEqualTo(DatingPushTarget.SharedLocation(shareId))
    }

    @Test
    fun `a safety push whose id is not a UUID, or names the other kind, opens nothing`() {
        // The old panic link carried coordinates and no id.
        assertThat(target(PANIC, "", "/dating/safety?lat=17.440000&lng=78.390000")).isNull()
        assertThat(target(PANIC, "../matches", "/dating/safety/alerts/../matches")).isNull()
        assertThat(target(PANIC, "abc", "/dating/safety/alerts/abc")).isNull()
        assertThat(target(PANIC, "", "/dating/safety/alerts/$alertId/extra")).isNull()
        // A link of the other kind is not trusted for this type, and the bad entity id adds nothing.
        assertThat(target(PANIC, "x", "/dating/safety/shared-locations/$shareId")).isNull()
        assertThat(target(SHARED, "x", "/dating/safety/alerts/$alertId")).isNull()
        assertThat(target(SHARED, "", "/dating/other/shared-locations/$shareId")).isNull()
        assertThat(safetyId(null)).isNull()
        assertThat(safetyId("/dating/safety/alerts/")).isNull()
        assertThat(safetyId("/dating/safety/alerts/1-2-3-4-5")).isNull()
    }
}

private const val PANIC = "dating.safety.panic.trusted_contact"
private const val SHARED = "dating.safety.location_shared"
