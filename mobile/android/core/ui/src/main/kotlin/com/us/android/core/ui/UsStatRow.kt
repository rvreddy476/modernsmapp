// MatchingDeclarationName: this file's primary export is the UsStatRow
// composable; UsStat is the value type it consumes. The rule assumes a file
// with one classlike declaration is *about* that declaration, which does not
// hold for a component plus its parameter type.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme
import java.util.Locale

/** One labelled count. [onClick] is null when the figure is not navigable. */
@Immutable
data class UsStat(
    val label: String,
    val value: Int,
    val onClick: (() -> Unit)? = null,
)

/**
 * A row of counts, as seen on a profile header.
 *
 * Accessibility is the reason this is shared rather than laid out per screen.
 * Rendered naively, a screen reader walks "1", "Followers", "2", "Following"
 * as four disconnected nodes and the association is lost. Each stat is merged
 * into a single node reading "1 Followers", and tappable stats get a minimum
 * 48dp target and a Button role.
 */
@Composable
fun UsStatRow(
    stats: List<UsStat>,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxxxl),
    ) {
        stats.forEach { stat ->
            val readable = "${formatCount(stat.value)} ${stat.label}"
            Column(
                horizontalAlignment = Alignment.Start,
                modifier = Modifier
                    .sizeIn(minWidth = 48.dp, minHeight = 48.dp)
                    .then(
                        if (stat.onClick != null) {
                            Modifier.clickable(onClick = stat.onClick)
                        } else {
                            Modifier
                        },
                    )
                    .padding(vertical = UsTheme.spacing.xs)
                    .clearAndSetSemantics {
                        contentDescription = readable
                        if (stat.onClick != null) role = Role.Button
                    },
            ) {
                Text(
                    text = formatCount(stat.value),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = stat.label,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }
    }
}

/**
 * Compact count formatting: 999, 1.2K, 3.4M.
 *
 * Uses [Locale.US] deliberately. The suffixes are English and the decimal
 * separator must match them — a device set to a comma-decimal locale would
 * otherwise render "1,2K", which reads as twelve thousand in the very locales
 * where the comma means something else.
 *
 * Public because the reels rail renders the same counts as the feed card, and
 * two different roundings of the same number on two screens reads as one of
 * them being wrong.
 */
fun formatCount(value: Int): String = when {
    value < 0 -> "0"
    value < THOUSAND -> value.toString()
    value < MILLION -> compact(value / THOUSAND.toDouble(), "K")
    else -> compact(value / MILLION.toDouble(), "M")
}

private fun compact(scaled: Double, suffix: String): String {
    // Truncate rather than round: 999,999 must not display as "1.0M".
    val truncated = (scaled * TENTHS).toInt() / TENTHS.toDouble()
    return if (truncated % 1.0 == 0.0) {
        "${truncated.toInt()}$suffix"
    } else {
        String.format(Locale.US, "%.1f%s", truncated, suffix)
    }
}

private const val THOUSAND = 1_000
private const val MILLION = 1_000_000

/** One decimal place of precision, applied by truncation. */
private const val TENTHS = 10

// Preview-only sample figures, chosen to exercise every formatting branch:
// a plain count, an abbreviated thousand, and a value just under a boundary.
private const val SAMPLE_POSTS = 42
private const val SAMPLE_FOLLOWERS = 12_400
private const val SAMPLE_FOLLOWING = 318

@Preview(name = "Stat row", showBackground = true)
@Composable
private fun UsStatRowPreview() {
    UsTheme {
        Column(modifier = Modifier.padding(UsTheme.spacing.pageHorizontal)) {
            UsStatRow(
                stats = listOf(
                    UsStat("Posts", SAMPLE_POSTS),
                    UsStat("Followers", SAMPLE_FOLLOWERS, onClick = {}),
                    UsStat("Following", SAMPLE_FOLLOWING, onClick = {}),
                ),
            )
        }
    }
}

@Preview(name = "Stat row — new account", showBackground = true)
@Composable
private fun UsStatRowZeroPreview() {
    UsTheme {
        Column(modifier = Modifier.padding(UsTheme.spacing.pageHorizontal)) {
            UsStatRow(
                stats = listOf(
                    UsStat("Posts", 0),
                    UsStat("Followers", 0),
                    UsStat("Following", 0),
                ),
            )
        }
    }
}
