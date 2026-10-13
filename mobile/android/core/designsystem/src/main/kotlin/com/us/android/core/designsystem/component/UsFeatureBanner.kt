package com.us.android.core.designsystem.component

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/** A compact introduction panel using the same tokens as our forms and cards. */
@Composable
fun UsFeatureBanner(
    title: String,
    description: String,
    icon: ImageVector,
    modifier: Modifier = Modifier,
    eyebrow: String? = null,
    action: (@Composable () -> Unit)? = null,
) {
    Surface(modifier.fillMaxWidth(), shape = RoundedCornerShape(UsTheme.radii.card), color = UsTheme.extended.bgRaised) {
        Column(Modifier.padding(UsTheme.spacing.xxxxl), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    eyebrow?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary) }
                    Text(title, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold, color = UsTheme.extended.textPrimary)
                    Text(description, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                }
                Surface(shape = RoundedCornerShape(UsTheme.radii.large), color = UsTheme.extended.bgCardSolid) {
                    Icon(icon, null, Modifier.padding(UsTheme.spacing.xxl).size(32.dp), tint = UsTheme.extended.accentSolid)
                }
            }
            action?.invoke()
        }
    }
}
