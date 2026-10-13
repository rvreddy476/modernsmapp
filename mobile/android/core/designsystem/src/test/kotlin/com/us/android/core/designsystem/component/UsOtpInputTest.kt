package com.us.android.core.designsystem.component

import androidx.compose.runtime.*
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w360dp-h800dp")
class UsOtpInputTest {
    @get:Rule val compose = createComposeRule()
    @Test fun pasteFiltersAndCapsCodeInOneAccessibleField() {
        var entered = ""
        compose.setContent {
            var code by remember { mutableStateOf("") }
            UsTheme { UsOtpField(code, { code = it; entered = it }) }
        }
        compose.onNodeWithContentDescription("Verification code, 6 digits").performTextInput("Code: 1234567")
        compose.runOnIdle { assertThat(entered).isEqualTo("123456") }
        compose.onAllNodes(hasSetTextAction()).assertCountEquals(1)
    }
}
