package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.error
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Text input with inline error support.
 *
 * [errorText] drives both the error styling and the message, so a caller
 * cannot show a red field with no explanation, or an explanation with no
 * red field. Passing null means "valid".
 */
@Composable
fun UsTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    placeholder: String? = null,
    errorText: String? = null,
    enabled: Boolean = true,
    singleLine: Boolean = true,
    isPassword: Boolean = false,
    keyboardType: KeyboardType = KeyboardType.Text,
) {
    var passwordVisible by rememberSaveable(label) { mutableStateOf(false) }
    Column(modifier = modifier) {
        OutlinedTextField(
            value = value,
            onValueChange = onValueChange,
            modifier = Modifier.fillMaxWidth().semantics {
                errorText?.let { error(it) }
            },
            enabled = enabled,
            singleLine = singleLine,
            isError = errorText != null,
            label = { Text(text = label, style = MaterialTheme.typography.bodyMedium) },
            placeholder = placeholder?.let { hint ->
                {
                    Text(text = hint, style = MaterialTheme.typography.bodyMedium)
                }
            },
            shape = RoundedCornerShape(UsTheme.radii.medium),
            visualTransformation = if (isPassword && !passwordVisible) {
                PasswordVisualTransformation()
            } else {
                VisualTransformation.None
            },
            keyboardOptions = KeyboardOptions(keyboardType = if (isPassword) KeyboardType.Password else keyboardType),
            trailingIcon = if (isPassword) {
                {
                    TextButton(
                        enabled = enabled,
                        onClick = { passwordVisible = !passwordVisible },
                        modifier = Modifier.semantics {
                            contentDescription = if (passwordVisible) "Hide password" else "Show password"
                        },
                    ) {
                        Text(if (passwordVisible) "Hide" else "Show", style = MaterialTheme.typography.labelMedium)
                    }
                }
            } else {
                null
            },
            textStyle = MaterialTheme.typography.bodyLarge,
            colors = OutlinedTextFieldDefaults.colors(
                focusedBorderColor = MaterialTheme.colorScheme.primary,
                unfocusedBorderColor = UsTheme.extended.borderMedium,
                focusedContainerColor = UsTheme.extended.bgCardSolid,
                unfocusedContainerColor = UsTheme.extended.bgRaised,
                focusedTextColor = UsTheme.extended.textPrimary,
                unfocusedTextColor = UsTheme.extended.textPrimary,
                unfocusedLabelColor = UsTheme.extended.textMuted,
                unfocusedPlaceholderColor = UsTheme.extended.textMuted,
            ),
        )
        if (errorText != null) {
            Text(
                text = errorText,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(start = 12.dp, top = 6.dp).semantics {
                    liveRegion = LiveRegionMode.Polite
                },
            )
        }
    }
}

@Preview(name = "Text fields", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsTextFieldPreview() {
    UsTheme {
        Column(
            modifier = Modifier
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            UsTextField(value = "", onValueChange = {}, label = "Email", placeholder = "you@example.com")
            UsTextField(value = "raghu", onValueChange = {}, label = "Username")
            UsTextField(
                value = "short",
                onValueChange = {},
                label = "Password",
                isPassword = true,
                errorText = "Password must be at least 8 characters",
            )
        }
    }
}
