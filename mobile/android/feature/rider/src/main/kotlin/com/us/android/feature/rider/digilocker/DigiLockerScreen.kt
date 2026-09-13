package com.us.android.feature.rider.digilocker

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LabeledValue
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.PillTone
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderPill
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.contentPadding
import com.us.android.feature.rider.ui.humanise

/**
 * DigiLocker verification.
 *
 * The authorize URL opens in the EXTERNAL BROWSER with ACTION_VIEW.
 * TODO(custom-tabs): switch to androidx.browser Custom Tabs once
 * `androidx.browser:browser` (catalog `androidx-browser`, 1.10.0) is in the
 * offline Gradle cache: the rider stays visually inside Feast Rider and the
 * return link lands back on the same task. The flow and the state check do not
 * change.
 */
@Composable
fun DigiLockerScreen(onBack: () -> Unit, viewModel: DigiLockerViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(viewModel) {
        viewModel.openBrowser.collect { url ->
            try {
                context.startActivity(
                    Intent(Intent.ACTION_VIEW, Uri.parse(url))
                        .addCategory(Intent.CATEGORY_BROWSABLE)
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                )
            } catch (e: ActivityNotFoundException) {
                // No browser: nothing to open. The state stays pending and harmless.
            }
        }
    }

    RiderScreen(title = "Aadhaar via DigiLocker", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        if (state.loading) {
            LoadingPane()
            return@RiderScreen
        }
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(contentPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            RiderCard {
                CardHeading(
                    title = if (state.verified) "Verified" else "Verify your identity",
                    body = "DigiLocker confirms your Aadhaar — and, if they are in your DigiLocker, your driving licence and RC — " +
                        "directly with the government. Feast never sees or stores your Aadhaar number.",
                )
                if (state.verified) RiderPill("Aadhaar verified", PillTone.Positive)
                UsButton(
                    text = if (state.verified) "Verify again" else "Continue to DigiLocker",
                    onClick = viewModel::start,
                    loading = state.starting || state.completing,
                    modifier = Modifier.fillMaxWidth(),
                )
                InfoNote("DigiLocker opens in your browser. Sign in, allow access, and you'll come straight back here.")
            }
            state.checks.forEach { check ->
                RiderCard {
                    CardHeading(humanise(check.kind))
                    LabeledValue("Name on document", check.nameOnDocumentMasked)
                    check.validUntil?.let { LabeledValue("Valid until", it) }
                    RiderPill(if (check.valid) "Valid" else "Expired", if (check.valid) PillTone.Positive else PillTone.Danger)
                }
            }
        }
    }
}
