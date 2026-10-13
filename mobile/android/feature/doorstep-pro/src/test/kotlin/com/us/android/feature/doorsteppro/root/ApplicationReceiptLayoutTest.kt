package com.us.android.feature.doorsteppro.root

import android.graphics.Bitmap
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.doorsteppro.apply.ApplicationReceiptScreen
import com.us.android.feature.doorsteppro.data.ProfessionalDto
import kotlinx.serialization.json.Json
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
class ApplicationReceiptLayoutTest {
    @get:Rule val compose = createComposeRule()
    @Test fun receiptShowsSubmissionNotFakeApprovalAndRefreshWorks() {
        val raw = checkNotNull(javaClass.classLoader?.getResource("contracts/pro_application_post_201.json")).readText()
        val pro = checkNotNull(Json { ignoreUnknownKeys = true }.decodeFromString<ApiEnvelope<ProfessionalDto>>(raw).data)
        var refreshed = false
        compose.setContent { UsTheme(darkTheme = false) { ApplicationReceiptScreen(pro, false) { refreshed = true } } }
        compose.onNodeWithText("Application under review").assertIsDisplayed()
        compose.onNodeWithText("Refresh status").performScrollTo().performClick()
        compose.runOnIdle { check(refreshed) }
        compose.onNodeWithText("Application under review").performScrollTo()
        val file = File("build/outputs/partner-ui-checks/partner-review.png")
        file.parentFile.mkdirs()
        file.outputStream().use { compose.onRoot().captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it) }
    }
}
