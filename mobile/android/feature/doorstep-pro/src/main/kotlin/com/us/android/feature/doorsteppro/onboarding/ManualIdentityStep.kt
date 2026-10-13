package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.listPadding

@Composable
fun ManualIdentityStepScreen(onBack: () -> Unit) {
    ProScreen(title = "Identity review", onBack = onBack) { padding ->
        Column(Modifier.fillMaxSize().padding(listPadding(padding))) {
            ProCard {
                CardHeading("Reviewed by your verification team", "Save your PAN number and submit a clear selfie from the checklist.")
                InfoNote("Aadhaar and DigiLocker are not required. PAN is collected as a number only; entering it is not government verification.")
                InfoNote("An admin must approve identity and review salon eligibility. Experience and course certificates support skill review, but do not unlock jobs automatically.")
            }
        }
    }
}
