package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Instant

class PartnerOfferInboxTest {
    @Test fun acceptClearsOtherCardsAndBlocksLaterPracticeWithoutBlockingLive() {
        val inbox=PartnerOfferInbox()
        val now=Instant.now()
        fun data(id:String,test:Boolean)=mapOf("type" to "food_delivery_offer","offer_id" to id,"expires_at" to now.plusSeconds(20).toString(),"test_only" to test.toString())
        repeat(5){inbox.receive(data("test-$it",true),now)}
        inbox.receive(data("real",false),now)
        inbox.accepted(true)
        inbox.receive(data("late-test",true),now)
        assertThat(inbox.offers.value.map {it["offer_id"]}).containsExactly("real")
        assertThat(inbox.isBlocked(true)).isTrue()
        assertThat(inbox.isBlocked(false)).isFalse()
        inbox.resetPractice()
        inbox.receive(data("new-test",true),now)
        assertThat(inbox.offers.value.size).isEqualTo(2)
    }
    @Test fun staleAvailabilityCannotUndoAcceptanceAndOnlyReadbackResumesLive() {
        val inbox=PartnerOfferInbox()
        val checkpoint=inbox.checkpoint()
        inbox.accepted(false)
        inbox.reconcileLive(false,checkpoint)
        assertThat(inbox.isBlocked(false)).isTrue()
        inbox.clear() // Leaving the screen cannot mark the partner available.
        assertThat(inbox.isBlocked(false)).isTrue()
        inbox.reconcileLive(false,inbox.checkpoint())
        assertThat(inbox.isBlocked(false)).isFalse()
    }
    @Test fun simultaneousOffersAreRetainedDeduplicatedAndExpireIndependently() {
        val inbox = PartnerOfferInbox()
        val now = Instant.parse("2026-10-11T10:00:00Z")
        fun offer(id: String) = mapOf("type" to "doorstep.pro.offer.new", "entity_id" to id, "expires_at" to now.plusSeconds(20).toString())
        for (id in listOf("one", "two", "three", "one")) inbox.receive(offer(id), now)
        assertThat(inbox.offers.value.size).isEqualTo(3)
        inbox.remove("two")
        assertThat(inbox.offers.value.map { it["entity_id"] }).containsExactly("one", "three")
        inbox.receive(offer("three"), now.plusSeconds(21))
        assertThat(inbox.offers.value).isEmpty()
    }
}
