package com.us.android.feature.auth.partner

import android.graphics.Bitmap
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Density
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import com.us.android.core.auth.dto.PartnerPhoneChallengeDto
import com.us.android.core.designsystem.theme.UsTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w412dp-h915dp-mdpi", application = android.app.Application::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PartnerPhoneLayoutTest {
    @get:Rule val compose = createComposeRule()
    @Test fun enteredCodeEnablesVerificationAndChangeNumberWorks() {
        var verified = false
        var changed = false
        compose.setContent { UsTheme(darkTheme = false) {
            var state by remember { mutableStateOf(PartnerPhoneState(phone = "9876543210",
                challenge = PartnerPhoneChallengeDto("test-challenge", "development_log", 300, 30))) }
            PartnerPhoneScreen(state, { update -> state = update(state) }, {}, { verified = true }, { changed = true }, {}, {})
        } }
        compose.onNodeWithContentDescription("Verification code, 6 digits").performTextInput("123456")
        compose.onNodeWithText("Verify & continue").assertIsEnabled().performClick()
        compose.onNodeWithText("Change number").performClick()
        compose.runOnIdle { check(verified && changed) }
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp-mdpi")
    fun smallPhoneWithLargeTextKeepsActionsReachable() {
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, 1.5f)) {
                UsTheme(darkTheme = false) {
                    PartnerPhoneScreen(PartnerPhoneState(phone = "9876543210",
                        challenge = PartnerPhoneChallengeDto("test-challenge", "development_log", 300, 30)),
                        {}, {}, {}, {}, {}, {})
                }
            }
        }
        compose.onNodeWithText("Verify & continue").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("Change number").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("Need help signing in?").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("Verify your mobile number").performScrollTo()
        val file = File("build/outputs/partner-ui-checks/partner-otp-small-large-text.png")
        file.parentFile.mkdirs()
        file.outputStream().use { compose.onRoot().captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test fun otpUsesSharedInputAndDoesNotClaimSmsInDevelopment() {
        compose.setContent { UsTheme(darkTheme = false) {
            PartnerPhoneScreen(PartnerPhoneState(phone = "9876543210", challenge = PartnerPhoneChallengeDto("test-challenge", "development_log", 300, 30), cooldown = 30),
                {}, {}, {}, {}, {}, {})
        } }
        compose.onNodeWithText("Verify your mobile number").assertIsDisplayed()
        compose.onNodeWithText("Verify & continue").assertIsNotEnabled()
        compose.onNodeWithText("Resend in 30s").assertIsNotEnabled()
        compose.onNodeWithText("Development testing: the code is in server logs. No SMS was sent.").assertIsDisplayed()
        val file = File("build/outputs/partner-ui-checks/partner-otp.png")
        file.parentFile.mkdirs()
        file.outputStream().use { compose.onRoot().captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it) }
    }
}
