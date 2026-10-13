package com.us.doorstep.pro.partner

import android.graphics.Bitmap
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.Density
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.root.DeliveryWelcomeScreen
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h800dp-mdpi", application = android.app.Application::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class BusinessCardLayoutTest {
    @get:Rule val compose = createComposeRule()
    private val fixture = WorkspaceState(
        summaries = listOf(BusinessSummary(PartnerBusiness.HOME, BusinessStatus.APPROVED, true),
            BusinessSummary(PartnerBusiness.FOOD_DELIVERY, BusinessStatus.REVIEW, true),
            BusinessSummary(PartnerBusiness.COMMERCE_DELIVERY, BusinessStatus.NEW),
            BusinessSummary(PartnerBusiness.MOPED, BusinessStatus.NEW)),
        selected = setOf(PartnerBusiness.HOME, PartnerBusiness.FOOD_DELIVERY, PartnerBusiness.MOPED), refreshing = false)

    @Test fun fullPhoneHomeIsCompactAndContainsNoMerchantOrExtraPanels() {
        var opened: PartnerBusiness? = null
        var selectedTab = -1
        var add = false
        compose.setContent { UsTheme(darkTheme = false) {
            PartnerShell("Cleestudio Partner", "Test Partner", false, 0, false, { selectedTab = it }, {}, {}) {
                PartnerBusinessOverview(fixture, "Test Partner", true, { add = true }, { opened = it }, {})
            }
        } }
        compose.onNodeWithText("Home Services").assertIsDisplayed()
        compose.onNodeWithText("Food Delivery").assertIsDisplayed()
        compose.onNodeWithText("Moped").assertIsDisplayed()
        compose.onNodeWithText("Approved").assertIsDisplayed()
        compose.onNodeWithText("Under review").assertIsDisplayed()
        for (text in listOf("Food", "Selling", "Start your food business", "Help with your account", "Complete your Home Services setup")) {
            compose.onNodeWithText(text).assertDoesNotExist()
        }
        // Three normal-sized work categories fit without a scrolling feed.
        val range = compose.onNode(hasScrollAction()).fetchSemanticsNode().config[SemanticsProperties.VerticalScrollAxisRange]
        assertThat(range.maxValue()).isEqualTo(0f)
        compose.onNodeWithText("Food Delivery").performClick()
        compose.onNodeWithText("Add").performClick()
        compose.onNodeWithContentDescription("Work").performClick()
        compose.runOnIdle {
            assertThat(opened).isEqualTo(PartnerBusiness.FOOD_DELIVERY)
            assertThat(selectedTab).isEqualTo(1)
            assertThat(add).isTrue()
        }
        capture("partner-home-v04.png")
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp-mdpi")
    fun largeTextScrollsOnlyAsNeededAndKeepsNavigationVisible() {
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, 1.5f)) {
                UsTheme(darkTheme = false) {
                    PartnerShell("Cleestudio Partner", "Test Partner", false, 1, false, {}, {}, {}) {
                        PartnerBusinessOverview(fixture, "Test Partner", false, {}, {}, {})
                    }
                }
            }
        }
        compose.onNodeWithText("Moped").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("Commerce Delivery").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("Food Delivery").performScrollTo().assertIsDisplayed()
        compose.onNodeWithContentDescription("Account").assertIsDisplayed()
        compose.onNodeWithContentDescription("Home").assertIsDisplayed()
        capture("partner-businesses-large-text-v04.png")
    }

    @Test fun foodDeliveryStartsRiderSetupNotKitchenCreation() {
        var started = false
        compose.setContent { UsTheme(darkTheme = false) {
            PartnerShell("Food Delivery", "Test Partner", false, 0, false, {}, {}, {}, onBack = {}) {
                DeliveryWelcomeScreen { started = true }
            }
        } }
        compose.onNodeWithText("Set up food delivery").assertIsDisplayed()
        compose.onNodeWithText("Get started").assertIsDisplayed().performClick()
        compose.runOnIdle { assertThat(started).isTrue() }
        for (text in listOf("Create kitchen", "Kitchen or restaurant name", "Kitchen address", "Start your food business")) {
            compose.onNodeWithText(text).assertDoesNotExist()
        }
        compose.onNodeWithContentDescription("Back to businesses").assertIsDisplayed()
        compose.onNodeWithContentDescription("Account").assertIsDisplayed()
        capture("partner-food-delivery-v04.png")
    }

    @Test fun closedUnselectedMopedDoesNotProduceAHomeWarning() {
        val state = fixture.copy(
            summaries = listOf(fixture.summaries.first(), BusinessSummary(PartnerBusiness.MOPED, BusinessStatus.UNAVAILABLE)),
            selected = setOf(PartnerBusiness.HOME))
        compose.setContent { UsTheme(darkTheme = false) {
            PartnerShell("Cleestudio Partner", "Test Partner", false, 0, false, {}, {}, {}) {
                PartnerBusinessOverview(state, "Test Partner", true, {}, {}, {})
            }
        } }
        compose.onNodeWithText("Home Services").assertIsDisplayed()
        compose.onNodeWithText("Approved").assertIsDisplayed()
        compose.onNodeWithText("Moped").assertDoesNotExist()
        compose.onNodeWithText("Couldn't check Moped.").assertDoesNotExist()
        compose.onNodeWithText("Retry").assertDoesNotExist()
        capture("partner-home-moped-closed-v041.png")
    }

    @Test fun closedSelectedMopedIsNotAnActiveOnboardingLink() {
        val state = fixture.copy(summaries = fixture.summaries.map {
            if (it.business == PartnerBusiness.MOPED) it.copy(status = BusinessStatus.UNAVAILABLE) else it
        })
        compose.setContent { UsTheme(darkTheme = false) {
            PartnerBusinessOverview(state, "Test Partner", true, {}, {}, {})
        } }
        compose.onNodeWithText("Moped").assertIsDisplayed()
        compose.onNodeWithText("Temporarily unavailable").assertIsDisplayed()
        compose.onAllNodes(hasText("Moped") and hasClickAction()).assertCountEquals(0)
        compose.onNodeWithText("Couldn't check Moped.").assertDoesNotExist()
    }

    @Test fun realMopedFailureStillOffersRetry() {
        var retried = false
        val state = fixture.copy(summaries = fixture.summaries.map {
            if (it.business == PartnerBusiness.MOPED) it.copy(status = BusinessStatus.UNKNOWN) else it
        })
        compose.setContent { UsTheme(darkTheme = false) {
            PartnerBusinessOverview(state, "Test Partner", false, {}, {}, { retried = true })
        } }
        compose.onNodeWithText("Couldn't check Moped.").assertIsDisplayed()
        compose.onNodeWithText("Retry").performClick()
        compose.runOnIdle { assertThat(retried).isTrue() }
    }

    @Test fun closedMopedDeepLinkHasASafeAvailabilityScreenWithRetry() {
        var retried = false
        compose.setContent { UsTheme(darkTheme = false) {
            UnavailableBusiness(PartnerBusiness.MOPED, false) { retried = true }
        } }
        compose.onNodeWithText("Moped is currently unavailable").assertIsDisplayed()
        compose.onNodeWithText("Check again").performClick()
        compose.onNodeWithText("Under review").assertDoesNotExist()
        compose.runOnIdle { assertThat(retried).isTrue() }
    }

    @Test fun commerceRegistrationUsesWorkerFieldsAndNoMerchantFields() {
        compose.setContent { UsTheme(darkTheme = false) {
            CommerceApplicationScreen(CommerceApplicationState(loading = false, missing = true), {}, { _, _ -> }, { _, _ -> }, {})
        } }
        compose.onNodeWithText("Full name").assertIsDisplayed().performTextInput("Test Partner")
        compose.onNodeWithText("Bicycle").assertIsDisplayed().performClick()
        compose.onNodeWithText("Save details").assertIsEnabled()
        for (field in listOf("Restaurant name", "Kitchen address", "Menu", "Products", "Fish Delivery")) {
            compose.onNodeWithText(field).assertDoesNotExist()
        }
        capture("partner-commerce-onboarding-v05.png")
    }

    @Test fun commerceNetworkErrorCannotBecomePendingVerification() {
        val profile = Json.parseToJsonElement("""{"id":"profile-fixture","full_name":"Test Partner","status":"approved","vehicle_type":"bicycle","documents":[]}""").jsonObject
        compose.setContent { UsTheme(darkTheme = false) {
            CommerceApplicationScreen(CommerceApplicationState(profile, loading = false, error = "Connection interrupted"), {}, { _, _ -> }, { _, _ -> }, {})
        } }
        compose.onNodeWithText("Connection interrupted").assertIsDisplayed()
        compose.onNodeWithText("The details below are from your last successful refresh.").assertIsDisplayed()
        compose.onNodeWithText("Approved").assertIsDisplayed()
        compose.onNodeWithText("Under review").assertDoesNotExist()
        compose.onNodeWithText("Full name").assertDoesNotExist()
    }

    private fun capture(name: String) {
        val file = File("build/outputs/partner-ui-checks/$name")
        file.parentFile.mkdirs()
        file.outputStream().use { compose.onRoot().captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it) }
    }
}
