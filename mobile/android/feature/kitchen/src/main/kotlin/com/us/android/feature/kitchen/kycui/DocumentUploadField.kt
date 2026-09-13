package com.us.android.feature.kitchen.kycui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/*
 * LIFTABLE into :core:kyc-ui. Stateless: the caller owns the upload and
 * passes its state; the field only draws it and reports taps. No kitchen,
 * food or media type appears here, so Feast Rider's DL/RC uploads can use it
 * unchanged once it moves.
 */

sealed interface DocumentUploadUi {
    data object Idle : DocumentUploadUi

    data class Uploading(val progress: Float) : DocumentUploadUi

    data class Uploaded(val mediaId: String) : DocumentUploadUi

    data class Failed(val message: String) : DocumentUploadUi
}

@Composable
fun DocumentUploadField(
    label: String,
    state: DocumentUploadUi,
    onPick: () -> Unit,
    modifier: Modifier = Modifier,
    errorText: String? = null,
) {
    val shape = RoundedCornerShape(UsTheme.radii.panel)
    val uploaded = state is DocumentUploadUi.Uploaded
    val failed = state is DocumentUploadUi.Failed
    val borderColor = when {
        errorText != null || failed -> UsTheme.extended.statusDanger
        uploaded -> UsTheme.extended.statusSuccess
        else -> UsTheme.extended.borderMedium
    }
    Column(modifier = modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(shape)
                .background(UsTheme.extended.bgCardSolid)
                .border(1.dp, borderColor, shape)
                .clickable(enabled = state !is DocumentUploadUi.Uploading, onClick = onPick)
                .padding(14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(
                modifier = Modifier
                    .size(40.dp)
                    .background(UsTheme.extended.bgRaised, RoundedCornerShape(10.dp)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = if (uploaded) UsIcons.Check else UsIcons.Upload,
                    contentDescription = null,
                    tint = if (uploaded) UsTheme.extended.statusSuccess else UsTheme.extended.accentSolid,
                    modifier = Modifier.size(20.dp),
                )
            }
            Column(modifier = Modifier.weight(1f).padding(start = 12.dp)) {
                Text(label, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                Text(
                    text = when (state) {
                        DocumentUploadUi.Idle -> "Tap to choose a photo"
                        is DocumentUploadUi.Uploading -> "Uploading… ${(state.progress * PERCENT).toInt()}%"
                        is DocumentUploadUi.Uploaded -> "Uploaded · tap to replace"
                        is DocumentUploadUi.Failed -> state.message
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = if (failed) UsTheme.extended.statusDanger else UsTheme.extended.textMuted,
                )
            }
        }
        if (state is DocumentUploadUi.Uploading) {
            LinearProgressIndicator(
                progress = { state.progress },
                modifier = Modifier.fillMaxWidth().padding(top = 6.dp),
                color = UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.borderSubtle,
            )
        }
        if (errorText != null) {
            Text(
                text = errorText,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
                modifier = Modifier.padding(top = 4.dp),
            )
        }
    }
}

private const val PERCENT = 100
