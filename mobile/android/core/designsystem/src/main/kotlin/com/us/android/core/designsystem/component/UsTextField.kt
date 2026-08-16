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
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
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
    Column(modifier = modifier) {
        OutlinedTextField(
            value = value,
            onValueChange = onValueChange,
            modifier = Modifier.fillMaxWidth(),
            enabled = enabled,
            singleLine = singleLine,
            isError = errorText != null,
            label = { Text(text = label, style = MaterialTheme.typography.bodySmall) },
            placeholder = placeholder?.let { hint ->
                {
                    Text(text = hint, style = MaterialTheme.typography.bodyMedium)
                }
            },
            shape = RoundedCornerShape(UsTheme.radii.medium),
            visualTransformation = if (isPassword) {
                PasswordVisualTransformation()
            } else {
                VisualTransformation.None
            },
            keyboardOptions = KeyboardOptions(keyboardType = keyboardType),
            textStyle = MaterialTheme.typography.bodyLarge,
            colors = OutlinedTextFieldDefaults.colors(
                focusedBorderColor = MaterialTheme.colorScheme.primary,
                unfocusedBorderColor = UsTheme.extended.borderMedium,
                focusedContainerColor = UsTheme.extended.bgCard,
                unfocusedContainerColor = UsTheme.extended.bgCard,
                focusedTextColor = UsTheme.extended.textPrimary,
                unfocusedTextColor = UsTheme.extended.textSecondary,
                unfocusedLabelColor = UsTheme.extended.textMuted,
                unfocusedPlaceholderColor = UsTheme.extended.textDim,
            ),
        )
        if (errorText != null) {
            Text(
                text = errorText,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(start = 12.dp, top = 4.dp),
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
