package com.us.android.core.designsystem.component

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w1000dp-h800dp")
class UsFormLayoutTest {
    @get:Rule
    val compose = createComposeRule()

    @Test
    fun `password visibility is reversible without modifying the value`() {
        compose.setContent {
            UsTheme { UsTextField("secret", {}, "Password", isPassword = true) }
        }
        compose.onNodeWithContentDescription("Show password").performClick()
        compose.onNodeWithContentDescription("Hide password").assertIsDisplayed().performClick()
        compose.onNodeWithContentDescription("Show password").assertIsDisplayed()
    }

    @Test
    fun `validation is visible and exposed to accessibility`() {
        val message = "Enter a valid email address"
        compose.setContent {
            UsTheme { UsTextField("bad", {}, "Email", errorText = message) }
        }
        compose.onNodeWithText(message).assertIsDisplayed()
        compose.onNode(SemanticsMatcher.expectValue(SemanticsProperties.Error, message)).assertIsDisplayed()
    }

    @Test
    fun `tablet content stays centered within the reading width`() {
        compose.setContent {
            UsTheme {
                UsScaffold { padding ->
                    Box(Modifier.fillMaxSize().padding(padding).testTag("content"))
                }
            }
        }
        val bounds = compose.onNodeWithTag("content").fetchSemanticsNode().boundsInRoot
        val density = compose.density.density
        assertThat(bounds.width / density).isAtMost(720f)
        assertThat(bounds.left / density).isGreaterThan(0f)
        assertThat(bounds.center.x / density).isWithin(1f).of(500f)
    }
}
