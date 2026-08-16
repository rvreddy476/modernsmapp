package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The app's one-time-code input.
 *
 * Rendered as separate boxes but backed by a **single** hidden text field.
 * That matters more than it looks: per-box fields break paste, break SMS/email
 * autofill, and turn backspace into a focus-management puzzle. One field with
 * a decorative box row keeps paste and autofill working and keeps the caret
 * logic trivial.
 *
 * The boxes are marked as decorative for accessibility — a screen reader reads
 * the single field and its label, not six anonymous containers.
 */
@Composable
@Suppress("LongParameterList")
fun UsOtpField(
    value: String,
    onValueChange: (String) -> Unit,
    modifier: Modifier = Modifier,
    length: Int = DEFAULT_LENGTH,
    enabled: Boolean = true,
    errorText: String? = null,
    autoFocus: Boolean = true,
    /** Fired when the last digit is entered, so the user need not hunt for a button. */
    onFilled: (String) -> Unit = {},
) {
    val focusRequester = remember { FocusRequester() }
    val keyboard = LocalSoftwareKeyboardController.current

    LaunchedEffect(autoFocus) {
        if (autoFocus) focusRequester.requestFocus()
    }

    Column(modifier = modifier) {
        BasicTextField(
            value = value,
            onValueChange = { raw ->
                // Digits only, hard-capped at [length]. Filtering here rather
                // than validating later means a pasted "Code: 123456" still
                // works instead of being rejected.
                val digits = raw.filter { it.isDigit() }.take(length)
                if (digits != value) {
                    onValueChange(digits)
                    if (digits.length == length) {
                        keyboard?.hide()
                        onFilled(digits)
                    }
                }
            },
            enabled = enabled,
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.NumberPassword,
                imeAction = ImeAction.Done,
            ),
            singleLine = true,
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(focusRequester)
                .semantics { contentDescription = "Verification code, $length digits" },
            // The real field is invisible; the boxes below are the visual.
            textStyle = TextStyle(color = Color.Transparent, fontSize = 1.sp),
            cursorBrush = androidx.compose.ui.graphics.SolidColor(Color.Transparent),
            decorationBox = {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                ) {
                    repeat(length) { index ->
                        OtpBox(
                            char = value.getOrNull(index)?.toString().orEmpty(),
                            focused = enabled && index == value.length.coerceAtMost(length - 1),
                            isError = errorText != null,
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
            },
        )

        if (errorText != null) {
            Text(
                text = errorText,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(start = 4.dp, top = UsTheme.spacing.m),
            )
        }
    }
}

@Composable
private fun OtpBox(
    char: String,
    focused: Boolean,
    isError: Boolean,
    modifier: Modifier = Modifier,
) {
    val border = when {
        isError -> MaterialTheme.colorScheme.error
        focused -> MaterialTheme.colorScheme.primary
        char.isNotEmpty() -> UsTheme.extended.borderMedium
        else -> UsTheme.extended.borderSubtle
    }
    Box(
        modifier = modifier
            .height(BOX_HEIGHT)
            .background(UsTheme.extended.bgCard, RoundedCornerShape(UsTheme.radii.medium))
            .border(
                width = if (focused) 2.dp else 1.dp,
                color = border,
                shape = RoundedCornerShape(UsTheme.radii.medium),
            ),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = char,
            style = MaterialTheme.typography.headlineSmall,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
    }
}

private const val DEFAULT_LENGTH = 6
private val BOX_HEIGHT = 60.dp

@Preview(name = "OTP field", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsOtpFieldPreview() {
    UsTheme {
        Column(
            modifier = Modifier
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            UsOtpField(value = "", onValueChange = {}, autoFocus = false)
            UsOtpField(value = "746", onValueChange = {}, autoFocus = false)
            UsOtpField(value = "746784", onValueChange = {}, autoFocus = false)
            UsOtpField(
                value = "000000",
                onValueChange = {},
                autoFocus = false,
                errorText = "That code isn't right. Check it and try again.",
            )
        }
    }
}
