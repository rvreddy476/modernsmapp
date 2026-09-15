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
    fun `other types and malformed links route nowhere`() {
        assertThat(target("dm", "c-1", "/dating/matches/m-1")).isNull()
        assertThat(PushDestinations.datingMatchIdOf("/dating/matches/")).isNull()
        assertThat(PushDestinations.datingMatchIdOf("/dating/matches/m-1/extra")).isNull()
        assertThat(PushDestinations.datingMatchIdOf("/chat/matches/m-1")).isNull()
        assertThat(PushDestinations.datingMatchIdOf(null)).isNull()
    }
}
