package com.us.android.feature.kitchen.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SliderColors
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.SwitchColors
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

@Composable
fun kitchenSwitchColors(): SwitchColors = SwitchDefaults.colors(
    checkedThumbColor = Color.White,
    checkedTrackColor = UsTheme.extended.accentSolid,
    checkedBorderColor = UsTheme.extended.accentSolid,
    uncheckedThumbColor = UsTheme.extended.textMuted,
    uncheckedTrackColor = UsTheme.extended.bgRaised,
    uncheckedBorderColor = UsTheme.extended.borderMedium,
)

@Composable
fun kitchenSliderColors(): SliderColors = SliderDefaults.colors(
    thumbColor = UsTheme.extended.accentSolid,
    activeTrackColor = UsTheme.extended.accentSolid,
    inactiveTrackColor = UsTheme.extended.borderMedium,
    activeTickColor = Color.Transparent,
    inactiveTickColor = Color.Transparent,
)

/** A tappable row inside a card: icon, title, detail, chevron. */
@Composable
fun NavRow(
    title: String,
    detail: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
) {
    KitchenCard(modifier = modifier, onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (icon != null) {
                Icon(icon, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(20.dp))
            }
            Column(
                modifier = Modifier
                    .weight(1f)
                    .padding(horizontal = if (icon != null) UsTheme.spacing.l else 0.dp),
            ) {
                Text(text = title, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                if (detail.isNotBlank()) {
                    Text(text = detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                }
            }
            Icon(
                imageVector = UsIcons.ChevronRight,
                contentDescription = null,
                tint = UsTheme.extended.textDim,
                modifier = Modifier.size(18.dp),
            )
        }
    }
}

/** A title and a sentence, the standard head of a card. */
@Composable
fun CardHeading(title: String, body: String? = null, modifier: Modifier = Modifier, titleColor: Color? = null) {
    Column(modifier = modifier.fillMaxWidth()) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleSmall,
            color = titleColor ?: UsTheme.extended.textPrimary,
        )
        if (!body.isNullOrBlank()) {
            Text(
                text = body,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(top = 2.dp),
            )
        }
    }
}
