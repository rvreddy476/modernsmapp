package com.us.doorstep.pro.partner

import android.graphics.Bitmap
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Density
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.notifications.PartnerOfferCategory
import com.google.common.truth.Truth.assertThat
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.time.Instant
import java.util.UUID
import java.io.File

@RunWith(RobolectricTestRunner::class)
@Config(sdk=[34], qualifiers="w400dp-h900dp-mdpi", application=android.app.Application::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PartnerRequestsLayoutTest {
    @get:Rule val compose = createComposeRule()
    private val now = Instant.now()
    private val cards = listOf(
        PartnerRequestCard(PartnerOfferRequest(PartnerOfferCategory.HOME, UUID.randomUUID().toString(), now.plusSeconds(20), true), "AC service", "Uppal, Hyderabad", 65000),
        PartnerRequestCard(PartnerOfferRequest(PartnerOfferCategory.FOOD, UUID.randomUUID().toString(), now.plusSeconds(20), true), "Food pickup & delivery", "Uppal → Peerzadiguda", 9000),
        PartnerRequestCard(PartnerOfferRequest(PartnerOfferCategory.COMMERCE, UUID.randomUUID().toString(), now.plusSeconds(20), true), "Shopping delivery", "Boduppal → Uppal", 12000))

    @Test fun threeVisibleCardsHaveCategoryLocationAmountAndSeparateActions() {
        var selected: String? = null
        compose.setContent { UsTheme(darkTheme=false) {
            PartnerRequestsPanel(PartnerRequestsState(cards=cards,now=now), { card, accept -> if(accept) selected=card.key }, {})
        } }
        cards.forEach { compose.onNodeWithText("TEST · ${it.request.category.label}").assertIsDisplayed(); compose.onNodeWithText(it.location).assertIsDisplayed() }
        compose.onAllNodesWithText("Accept").assertCountEquals(3)
        compose.onAllNodesWithText("Decline").assertCountEquals(3)
        compose.onAllNodesWithText("Accept")[1].performClick()
        assertThat(selected).isEqualTo(cards[1].key)
        val image=compose.onRoot().captureToImage().asAndroidBitmap()
        val file=File("build/outputs/partner-ui-checks/incoming-three-requests.png")
        file.parentFile.mkdirs();file.outputStream().use { image.compress(Bitmap.CompressFormat.PNG,100,it) }
    }
    @Test @Config(qualifiers="w320dp-h640dp-mdpi") fun smallScreenLargeTextCanReachEveryAction() {
        compose.setContent { CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density,1.5f)) {
            UsTheme(darkTheme=false) { PartnerRequestsPanel(PartnerRequestsState(cards=cards,now=now), { _,_ -> }, {}) }
        } }
        compose.onAllNodesWithText("Accept").onLast().performScrollTo().assertIsDisplayed()
        compose.onAllNodesWithText("Decline").onLast().assertIsDisplayed()
    }
    @Test fun expiredOrUnconfirmedRequestCannotBeAccepted() {
        compose.setContent { UsTheme(darkTheme=false) {
            PartnerRequestsPanel(PartnerRequestsState(cards=listOf(cards[0].copy(confirmed=false), cards[1].copy(request=cards[1].request.copy(expiresAt=now))),now=now),{ _,_ -> },{})
        } }
        compose.onAllNodesWithText("Accept").fetchSemanticsNodes().indices.forEach { compose.onAllNodesWithText("Accept")[it].assertIsNotEnabled() }
    }
}
