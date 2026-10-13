package com.us.android.core.designsystem.component

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/** One quiet content surface, shared by mini-apps; no blur or stacked shadows. */
@Composable
fun UsSurfaceCard(
    modifier: Modifier = Modifier,
    contentPadding: PaddingValues = PaddingValues(UsTheme.spacing.xl),
    onClick: (() -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    Surface(
        modifier = modifier.clip(RoundedCornerShape(UsTheme.radii.card))
            .then(if (onClick != null) Modifier.clickable(role = Role.Button, onClick = onClick) else Modifier),
        shape = RoundedCornerShape(UsTheme.radii.card),
        color = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        border = BorderStroke(1.dp, UsTheme.extended.borderSubtle),
    ) {
        Column(
            modifier = Modifier.padding(contentPadding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            content = content,
        )
    }
}

@Preview
@Composable
private fun SurfaceCardPreview() {
    UsTheme { UsSurfaceCard { Text("A little more room for what matters.") } }
}
