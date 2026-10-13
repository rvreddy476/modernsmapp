package com.us.android.core.designsystem.component

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w320dp-h800dp")
class UsNavigationLayoutTest {
    @get:Rule
    val compose = createComposeRule()

    @Test
    fun `all five tabs and create fit a narrow phone with full touch targets`() {
        var selection = -1
        var creates = 0
        compose.setContent {
            UsTheme {
                Box(Modifier.width(320.dp)) {
                    UsNavigationBar(UsDefaultNavItems, 4, { selection = it }, centerAction = { creates++ })
                }
            }
        }
        val density = compose.density.density
        val nodes = (UsDefaultNavItems.map { it.contentDescription } + "Create").map {
            compose.onNodeWithContentDescription(it).fetchSemanticsNode().boundsInRoot
        }
        nodes.forEach { bounds ->
            assertThat(bounds.left).isAtLeast(0f)
            assertThat(bounds.right).isAtMost(320f * density)
            assertThat(bounds.height).isAtLeast(48f * density)
            assertThat(bounds.width).isAtLeast(48f * density)
        }
        compose.onNodeWithContentDescription("My profile").assertIsSelected()
        compose.onNodeWithContentDescription("Messages").performClick()
        compose.onNodeWithContentDescription("Create").performClick()
        assertThat(selection).isEqualTo(1)
        assertThat(creates).isEqualTo(1)
    }

    @Test
    fun `large text keeps accessible destinations without splitting visual labels`() {
        compose.setContent {
            val density = LocalDensity.current.density
            CompositionLocalProvider(LocalDensity provides Density(density, fontScale = 2f)) {
                UsTheme { UsNavigationBar(UsDefaultNavItems, 0, {}, centerAction = {}) }
            }
        }
        UsDefaultNavItems.forEach { item ->
            val node = compose.onNodeWithContentDescription(item.contentDescription).fetchSemanticsNode()
            assertThat(node.size.height).isAtLeast((64 * compose.density.density).toInt())
            compose.onNodeWithText(item.label).assertDoesNotExist()
        }
    }
}
