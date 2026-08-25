package com.us.android.feature.notifications.permission

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Asks for the notification permission, in the one place it makes sense —
 * Slice D, D-D2.
 *
 * ## WHY HERE, AND NOT AT FIRST LAUNCH
 *
 * `POST_NOTIFICATIONS` was declared in the manifest but never requested, so on
 * Android 13 and above every push this platform sends was dropped by the system
 * before reaching the app. Everything else in the notification path was wired
 * and working; this was the missing step.
 *
 * The prompt fires when the user OPENS THE NOTIFICATION INBOX. That is the
 * strongest moment available: they have just said, by navigating here, that
 * they care about notifications. Asking on first launch — before anyone knows
 * what the app does — is the single most common way to get permanently denied,
 * and Android only gives you two refusals before the dialog stops appearing at
 * all.
 *
 * ## AFTER A PERMANENT DENIAL
 *
 * Requesting again does nothing: the callback fires immediately as "denied" and
 * no dialog appears. So this shows an inline row explaining the state and a
 * button to app settings, rather than a control that silently does nothing.
 * See [NotificationPermissionPolicy] for the decision itself.
 */
@Composable
fun NotificationPermissionPrompt(
    modifier: Modifier = Modifier,
    viewModel: NotificationPermissionViewModel = hiltViewModel(),
) {
    val context = LocalContext.current
    val hasAsked by viewModel.hasAsked.collectAsStateWithLifecycle()

    // Re-read after the result rather than trusting the callback's boolean:
    // the user can also change this in Settings while the app is backgrounded.
    var granted by remember { mutableStateOf(context.hasNotificationPermission()) }

    val launcher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted = context.hasNotificationPermission() }

    val action = NotificationPermissionPolicy.decide(
        sdkInt = Build.VERSION.SDK_INT,
        isGranted = granted,
        // Not yet loaded: treat as "already asked" so nothing is prompted on a
        // guess. The effect below re-runs once the real value arrives.
        hasAskedBefore = hasAsked ?: true,
        shouldShowRationale = context.shouldShowNotificationRationale(),
    )

    LaunchedEffect(action, hasAsked) {
        // Wait for the persisted flag before prompting anyone.
        if (hasAsked != null && action == NotificationPermissionAction.Request) {
            viewModel.onPermissionRequested()
            launcher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    if (action == NotificationPermissionAction.DirectToSettings) {
        Column(
            modifier = modifier
                .fillMaxWidth()
                .padding(
                    horizontal = UsTheme.spacing.pageHorizontal,
                    vertical = UsTheme.spacing.m,
                ),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            Text(
                text = "Notifications are turned off",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "You'll still see everything here, but nothing will reach " +
                    "your lock screen until you turn them on in Settings.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            UsSecondaryButton(
                text = "Open settings",
                onClick = { context.openAppNotificationSettings() },
            )
        }
    }
}

private fun Context.hasNotificationPermission(): Boolean =
    if (Build.VERSION.SDK_INT < NotificationPermissionPolicy.FIRST_SDK_REQUIRING_PERMISSION) {
        true
    } else {
        ContextCompat.checkSelfPermission(
            this,
            Manifest.permission.POST_NOTIFICATIONS,
        ) == PackageManager.PERMISSION_GRANTED
    }

/**
 * False both BEFORE the first request and AFTER a permanent denial. That
 * ambiguity is the entire reason the app persists its own "asked" flag.
 */
private fun Context.shouldShowNotificationRationale(): Boolean {
    if (Build.VERSION.SDK_INT < NotificationPermissionPolicy.FIRST_SDK_REQUIRING_PERMISSION) return false
    val activity = findActivity() ?: return false
    return ActivityCompat.shouldShowRequestPermissionRationale(
        activity,
        Manifest.permission.POST_NOTIFICATIONS,
    )
}

private tailrec fun Context.findActivity(): android.app.Activity? = when (this) {
    is android.app.Activity -> this
    is android.content.ContextWrapper -> baseContext.findActivity()
    else -> null
}

/**
 * Opens this app's notification settings, falling back to the app detail page.
 *
 * The dedicated notification screen does not exist before Android 8, and some
 * OEM builds do not honour it; landing on app details is worse but still gets
 * the user somewhere they can act.
 */
private fun Context.openAppNotificationSettings() {
    val intent = Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
        .putExtra(Settings.EXTRA_APP_PACKAGE, packageName)
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    val fallback = Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS)
        .setData(Uri.fromParts("package", packageName, null))
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    runCatching { startActivity(intent) }
        .recoverCatching { startActivity(fallback) }
}
