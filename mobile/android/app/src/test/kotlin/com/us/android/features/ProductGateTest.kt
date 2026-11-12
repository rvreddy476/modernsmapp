package com.us.android.features

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationTarget
import com.us.android.core.model.Product
import com.us.android.explore.LauncherApp
import com.us.android.explore.launcherTiles
import com.us.android.push.PushDestination
import com.us.android.push.PushDestinations
import org.junit.Test

/**
 * Founder decision C (24 Oct 2026): a product the gateway closes
 * (`GET /v1/features`) has no tile on the launcher, and a notification tap or
 * link into it lands on "Not available".
 */
class ProductGateTest {

    private fun push(type: String) = PushDestination(type = type, entityId = "e1", deepLink = "")

    private companion object {
        const val NOTE = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
    }

    @Test
    fun `every gated tile names its product, the rest none`() {
        assertThat(LauncherApp.entries.associateWith { it.product }.filterValues { it != null }).containsExactly(
            LauncherApp.ASK, Product.QA,
            LauncherApp.FEAST, Product.FOOD,
            LauncherApp.RIDE, Product.RIDER,
            LauncherApp.MATCH, Product.DATING,
            LauncherApp.DOORSTEP, Product.DOORSTEP,
            LauncherApp.ECHO, Product.ECHO,
        )
    }

    @Test
    fun `with every product closed the launcher keeps only the apps no switch closes`() {
        assertThat(launcherTiles(emptySet()).map { it.app }).containsExactly(
            LauncherApp.CHAT,
            LauncherApp.FRIENDS,
            LauncherApp.ALERTS,
            LauncherApp.LIVE,
            LauncherApp.MSTORE,
            LauncherApp.MSELLER,
            LauncherApp.TUBE,
        ).inOrder()
    }

    @Test
    fun `an open product keeps its tile, and no answer keeps every tile`() {
        val tiles = launcherTiles(setOf(Product.FOOD, Product.DATING)).map { it.app }
        assertThat(tiles).containsAtLeast(LauncherApp.FEAST, LauncherApp.MATCH)
        assertThat(tiles).containsNoneOf(LauncherApp.ASK, LauncherApp.RIDE, LauncherApp.DOORSTEP)

        // No answer keeps every tile but Echo's: Echo is closed until the gateway opens it.
        assertThat(launcherTiles(null).map { it.app })
            .containsExactlyElementsIn(LauncherApp.entries - LauncherApp.ECHO)
    }

    @Test
    fun `a push or link names the product it lands in`() {
        assertThat(productOfPush(push(PushDestinations.TYPE_DATING_MATCH))).isEqualTo(Product.DATING)
        assertThat(productOfPush(push(PushDestinations.TYPE_DATING_SCAM_ALERT))).isEqualTo(Product.DATING)
        assertThat(productOfPush(push("ride.completed"))).isEqualTo(Product.RIDER)
        assertThat(productOfPush(push("doorstep.booking.confirmed"))).isEqualTo(Product.DOORSTEP)
        assertThat(productOfPush(push(PushDestinations.TYPE_DOORSTEP_OUTSTANDING_DUE))).isEqualTo(Product.DOORSTEP)
        assertThat(productOfPush(push("food_order_status"))).isEqualTo(Product.FOOD)
    }

    @Test
    fun `every ride and doorstep push type lands in its product`() {
        // Each type, not one sample: a type the service prefix does not cover
        // reaches the gate only through these sets.
        for (type in PushDestinations.RIDE_TYPES) assertThat(productOfPush(push(type))).isEqualTo(Product.RIDER)
        for (type in PushDestinations.DOORSTEP_TYPES) assertThat(productOfPush(push(type))).isEqualTo(Product.DOORSTEP)
    }

    @Test
    fun `a push outside every parked product is never gated`() {
        for (type in listOf("order_shipped", "like", PushDestinations.TYPE_POST_LINK, PushDestinations.TYPE_CHAT_JOIN, "")) {
            assertThat(productOfPush(push(type))).isNull()
        }
    }

    @Test
    fun `Echo has a tile only while the gateway has it open`() {
        assertThat(launcherTiles(null).map { it.app }).doesNotContain(LauncherApp.ECHO)
        assertThat(launcherTiles(emptySet()).map { it.app }).doesNotContain(LauncherApp.ECHO)
        assertThat(launcherTiles(setOf(Product.FOOD)).map { it.app }).doesNotContain(LauncherApp.ECHO)
        val open = launcherTiles(setOf(Product.ECHO))
        assertThat(open.map { it.app }.last()).isEqualTo(LauncherApp.ECHO)
        assertThat(open.single { it.app == LauncherApp.ECHO }.soon).isFalse()
    }

    @Test
    fun `every way into an Echo note is behind the Echo gate`() {
        assertThat(productOfPush(push(NotificationTarget.NOTE_REPLY_TYPE))).isEqualTo(Product.ECHO)
        assertThat(productOfPush(push(NotificationTarget.NOTE_QUOTE_TYPE))).isEqualTo(Product.ECHO)
        assertThat(productOfPush(push(PushDestinations.TYPE_ECHO_NOTE_LINK))).isEqualTo(Product.ECHO)
        // A push of any type whose deep link names a note: the link decides where it lands, so the gate follows it.
        val linked = PushDestination(type = "something_new", entityId = "", deepLink = "momentum://echo/note/$NOTE")
        assertThat(productOfPush(linked)).isEqualTo(Product.ECHO)
        val web = PushDestination(type = "like", entityId = "", deepLink = "/echo/note/$NOTE")
        assertThat(productOfPush(web)).isEqualTo(Product.ECHO)
        // A note link through the App Link path.
        assertThat(
            productOfPush(PushDestinations.linkDestinationOf("https://cleestudio.com/echo/note/$NOTE")!!)
        ).isEqualTo(Product.ECHO)
        // A non-note link stays ungated.
        assertThat(productOfPush(PushDestination(type = "like", entityId = "", deepLink = "/post/$NOTE"))).isNull()
    }

    @Test
    fun `the not available screen says so plainly`() {
        assertThat(PRODUCT_UNAVAILABLE_TITLE).isEqualTo("Not available")
        assertThat(PRODUCT_UNAVAILABLE_DETAIL).isEqualTo("This part of the app is not open right now. Please check back later.")
    }
}
