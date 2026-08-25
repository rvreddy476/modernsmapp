package com.us.android.core.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The three states every data-backed screen has, in one place.
 *
 * These exist as shared components because they encode two rules that must not
 * diverge per screen: a retry is always offered when a failure is retryable,
 * and a loading indicator always announces itself to a screen reader. Screens
 * that hand-roll these reliably forget the second one.
 */

/** Full-surface loading. */
@Composable
fun UsLoadingState(
    modifier: Modifier = Modifier,
    label: String = "Loading",
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .semantics { contentDescription = label },
        contentAlignment = Alignment.Center,
    ) {
        CircularProgressIndicator(
            modifier = Modifier.size(32.dp),
            color = MaterialTheme.colorScheme.primary,
        )
    }
}

/**
 * A failure the user can do something about.
 *
 * [onRetry] is nullable rather than defaulted to a no-op: a screen with no
 * retry path should show no button, and a button that silently does nothing is
 * worse than its absence.
 */
@Composable
fun UsErrorState(
    message: String,
    modifier: Modifier = Modifier,
    onRetry: (() -> Unit)? = null,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = message,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textSecondary,
            textAlign = TextAlign.Center,
        )
        if (onRetry != null) {
            UsButton(
                text = "Try again",
                onClick = onRetry,
                modifier = Modifier
                    .padding(top = UsTheme.spacing.xxl)
                    .fillMaxWidth(),
            )
        }
    }
}

/** A successful load that returned nothing. Not an error — do not style it as one. */
@Composable
fun UsEmptyState(
    title: String,
    modifier: Modifier = Modifier,
    detail: String? = null,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        if (detail != null) {
            Text(
                text = detail,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = UsTheme.spacing.m),
            )
        }
    }
}

@Preview(name = "Loading", showBackground = true, heightDp = 200)
@Composable
private fun LoadingPreview() = UsTheme { UsLoadingState() }

@Preview(name = "Error with retry", showBackground = true, heightDp = 260)
@Composable
private fun ErrorPreview() = UsTheme {
    UsErrorState(message = "We couldn't load this profile.", onRetry = {})
}

@Preview(name = "Error without retry", showBackground = true, heightDp = 200)
@Composable
private fun ErrorNoRetryPreview() = UsTheme {
    UsErrorState(message = "This profile is no longer available.")
}

@Preview(name = "Empty", showBackground = true, heightDp = 200)
@Composable
private fun EmptyPreview() = UsTheme {
    UsEmptyState(title = "Nothing here yet", detail = "Posts will show up once they're shared.")
}
