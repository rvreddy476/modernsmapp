package com.us.android.feature.doorsteppro.ui

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Matrix
import android.media.ExifInterface
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.JobStatus
import com.us.android.feature.doorsteppro.domain.ProStatus

/*
 * Small pieces the professional's screens share, on top of ProUi.kt. Tokens
 * only: every colour is a UsTheme role, so light and dark both read right.
 */

/** A card's title and an optional muted line under it. */
@Composable
fun CardHeading(title: String, subtitle: String? = null, modifier: Modifier = Modifier) {
    Column(modifier = modifier) {
        Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.textPrimary)
        if (!subtitle.isNullOrBlank()) {
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(top = 2.dp),
            )
        }
    }
}

/** "Label    value" on one line. */
@Composable
fun LabeledValue(label: String, value: String, modifier: Modifier = Modifier) {
    Row(modifier = modifier.fillMaxWidth(), verticalAlignment = Alignment.Top) {
        Text(label, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted, modifier = Modifier.weight(0.4f))
        Text(value, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary, modifier = Modifier.weight(0.6f))
    }
}

/** A tappable row with an icon, a title, a line under it and a trailing pill. */
@Composable
fun ActionRow(
    icon: ImageVector,
    title: String,
    subtitle: String?,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    onClick: (() -> Unit)? = null,
    trailing: @Composable () -> Unit = {},
) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgCardSolid)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .then(if (onClick != null) Modifier.clickable(enabled = enabled, onClick = onClick) else Modifier)
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier.size(36.dp).background(UsTheme.extended.bgRaised, RoundedCornerShape(10.dp)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(18.dp))
        }
        Column(modifier = Modifier.weight(1f).padding(horizontal = UsTheme.spacing.l)) {
            Text(title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
            if (!subtitle.isNullOrBlank()) {
                Text(subtitle, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        trailing()
    }
}

/** The upload state of one photo or document field. */
sealed interface UploadUi {
    data object Idle : UploadUi

    data class Uploading(val progress: Float) : UploadUi

    data class Uploaded(val mediaId: String) : UploadUi

    data class Failed(val message: String) : UploadUi
}

/** A "tap to choose a photo" field with its upload progress (Feast Rider's DocumentUploadField, copied). */
@Composable
fun UploadField(label: String, state: UploadUi, onPick: () -> Unit, modifier: Modifier = Modifier, hint: String = "Tap to choose a photo") {
    val shape = RoundedCornerShape(UsTheme.radii.panel)
    val uploaded = state is UploadUi.Uploaded
    val failed = state is UploadUi.Failed
    val border = when {
        failed -> UsTheme.extended.statusDanger
        uploaded -> UsTheme.extended.statusSuccess
        else -> UsTheme.extended.borderMedium
    }
    Column(modifier = modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(shape)
                .background(UsTheme.extended.bgCardSolid)
                .border(1.dp, border, shape)
                .clickable(enabled = state !is UploadUi.Uploading, onClick = onPick)
                .padding(14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(
                modifier = Modifier.size(40.dp).background(UsTheme.extended.bgRaised, RoundedCornerShape(10.dp)),
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
                        UploadUi.Idle -> hint
                        is UploadUi.Uploading -> "Uploading… ${(state.progress * PERCENT).toInt()}%"
                        is UploadUi.Uploaded -> "Uploaded · tap to replace"
                        is UploadUi.Failed -> state.message
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = if (failed) UsTheme.extended.statusDanger else UsTheme.extended.textMuted,
                )
            }
        }
        if (state is UploadUi.Uploading) {
            LinearProgressIndicator(
                progress = { state.progress },
                modifier = Modifier.fillMaxWidth().padding(top = 6.dp),
                color = UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.borderSubtle,
            )
        }
    }
}

/** A small, upright preview of a captured photo; the upload sends the original file. */
@Composable
fun ShotPreview(path: String, modifier: Modifier = Modifier, description: String = "Your photo") {
    val bitmap = remember(path) { decodeUpright(path) }
    if (bitmap != null) {
        Image(
            bitmap = bitmap.asImageBitmap(),
            contentDescription = description,
            contentScale = ContentScale.Crop,
            modifier = modifier.clip(RoundedCornerShape(UsTheme.radii.large)),
        )
    }
}

private fun decodeUpright(path: String): Bitmap? = runCatching {
    val bitmap = BitmapFactory.decodeFile(path, BitmapFactory.Options().apply { inSampleSize = PREVIEW_SAMPLE }) ?: return null
    val degrees = when (ExifInterface(path).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL)) {
        ExifInterface.ORIENTATION_ROTATE_90 -> ROTATE_90
        ExifInterface.ORIENTATION_ROTATE_180 -> ROTATE_180
        ExifInterface.ORIENTATION_ROTATE_270 -> ROTATE_270
        else -> 0f
    }
    if (degrees == 0f) bitmap else Bitmap.createBitmap(bitmap, 0, 0, bitmap.width, bitmap.height, Matrix().apply { postRotate(degrees) }, true)
}.getOrNull()

/** A vertical stack of cards with the screen's standard spacing. */
@Composable
fun CardStack(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(modifier = modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) { content() }
}

fun ProError.asMessage(): UsMessage = UsMessage(userMessage(), UsMessageType.Error)

fun warningMessage(text: String): UsMessage = UsMessage(text = text, type = UsMessageType.Warning)

/** The account status as a professional reads it. */
fun ProStatus.label(): String = when (this) {
    ProStatus.DRAFT -> "Finish your checklist"
    ProStatus.PENDING_VERIFICATION -> "Under review"
    ProStatus.APPROVED -> "Approved"
    ProStatus.SUSPENDED -> "Paused by Doorstep"
    ProStatus.REJECTED -> "Not approved"
    ProStatus.BLOCKED -> "Blocked"
    ProStatus.UNKNOWN -> "Checking"
}

fun ProStatus.tone(): Tone = when (this) {
    ProStatus.APPROVED -> Tone.Positive
    ProStatus.PENDING_VERIFICATION, ProStatus.DRAFT -> Tone.Warning
    ProStatus.SUSPENDED, ProStatus.REJECTED, ProStatus.BLOCKED -> Tone.Danger
    ProStatus.UNKNOWN -> Tone.Neutral
}

/** Progress is the accent, done is success, off-ramps are danger. */
fun JobStatus.tone(): Tone = when (this) {
    JobStatus.COMPLETED -> Tone.Positive
    JobStatus.CANCELLED, JobStatus.EXPIRED, JobStatus.CUSTOMER_NO_SHOW, JobStatus.PRO_NO_SHOW -> Tone.Danger
    JobStatus.AWAITING_EXTRAS_PAYMENT, JobStatus.PENDING_PAYMENT -> Tone.Warning
    JobStatus.UNKNOWN -> Tone.Neutral
    else -> Tone.Accent
}

private const val PERCENT = 100
private const val PREVIEW_SAMPLE = 4
private const val ROTATE_90 = 90f
private const val ROTATE_180 = 180f
private const val ROTATE_270 = 270f
