package com.us.android.feature.commerce.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PriceBreakdown
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Shared commerce UI parts.
 *
 * These live here rather than in `:core:designsystem` because they encode
 * COMMERCE rules, not visual ones — how a price is allowed to be displayed,
 * and how a total is allowed to be assembled. A design-system component
 * should not have an opinion about GST.
 */

/**
 * A product image.
 *
 * It takes a URL, not a media id, and that is the whole change: commerce used
 * to hand the client a bare media UUID and nothing else, so this composable
 * had no way to draw anything and every product image on every screen was a
 * permanent grey box. `:core:commerce` has no dependency on `:core:media`
 * (where the resolver lives) and giving it one would pull the whole ExoPlayer
 * stack into a module that needs a URL string, so the server resolves it
 * instead — one fix for Android and iOS both.
 *
 * The frame and neutral fill stay for the two states that are not errors: a
 * product genuinely without an image, which is normal for a fresh listing, and
 * a URL the server could not resolve because media-service was unreachable.
 * The read path fails soft by design, so the correct rendering of "no URL" is
 * a placeholder, never a failure.
 */
@Composable
fun CommerceImage(
    url: String?,
    contentDescription: String?,
    modifier: Modifier = Modifier,
    aspect: Float = 1f,
) {
    Box(
        modifier = modifier
            .aspectRatio(aspect)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            // The description was previously accepted and dropped, which
            // detekt caught as an unused parameter — and which meant every
            // product image on every commerce screen was invisible to a
            // screen reader. A null description marks the image decorative;
            // a non-null one is announced.
            .semantics {
                contentDescription?.let { this.contentDescription = it }
            },
        contentAlignment = Alignment.Center,
    ) {
        if (url.isNullOrBlank()) {
            Text(
                text = "No image",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        } else {
            AsyncImage(
                model = url,
                // Null: the Box above already carries the description, and
                // announcing it twice makes a screen reader read every
                // product name in the grid two times over.
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

/**
 * Selling price with the MRP struck through when there is a genuine saving.
 *
 * No discount percentage. It would be the only figure on screen the client
 * derived rather than received, and a rounding difference from the server's
 * own number is precisely the disagreement that turns into a support ticket.
 */
@Composable
fun PriceRow(
    price: Paise,
    mrp: Paise,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = price.formatWithSymbol(),
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
        )
        if (mrp > price) {
            Text(
                text = mrp.formatWithSymbol(),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
                textDecoration = TextDecoration.LineThrough,
            )
        }
    }
}

/**
 * The order total, exactly as the server computed it.
 *
 * D1 — catalogue prices are GST-INCLUSIVE. Tax is a component already
 * contained in the subtotal, so it is shown as an "includes" line and is
 * NEVER added into the total here. Rendering it as "+ GST" would overstate
 * every order by the tax amount, and a customer who adds the visible rows up
 * would get a different number from the one being charged.
 *
 * Nothing on this component is calculated. Every figure is read from
 * [PriceBreakdown], which the server produced inside the checkout
 * transaction.
 */
@Composable
fun PriceBreakdownCard(
    breakdown: PriceBreakdown,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        AmountLine("Subtotal", breakdown.subtotal)
        if (breakdown.discount > Paise.ZERO) {
            AmountLine("Discount", Paise.ZERO - breakdown.discount)
        }
        AmountLine(
            label = "Delivery",
            amount = breakdown.shipping,
            // A free delivery is a fact worth stating plainly rather than
            // rendering as "₹0.00", which reads like a missing value.
            overrideText = "Free".takeIf { breakdown.shipping == Paise.ZERO },
        )
        // The app's hairline: a 1dp rule in the subtle border token, the same
        // line the sheets and settings rows draw. Material's HorizontalDivider
        // brings its own inset and thickness rules with it.
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = UsTheme.spacing.xs)
                .height(HAIRLINE)
                .background(UsTheme.extended.borderSubtle),
        )
        AmountLine("Total", breakdown.total, emphasise = true)
        if (breakdown.tax > Paise.ZERO) {
            Text(
                text = "Includes ${breakdown.tax.formatWithSymbol()} GST",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}

@Composable
private fun AmountLine(
    label: String,
    amount: Paise,
    emphasise: Boolean = false,
    overrideText: String? = null,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(
            text = label,
            style = if (emphasise) {
                MaterialTheme.typography.titleSmall
            } else {
                MaterialTheme.typography.bodyMedium
            },
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = overrideText ?: amount.formatWithSymbol(),
            style = if (emphasise) {
                MaterialTheme.typography.titleSmall
            } else {
                MaterialTheme.typography.bodyMedium
            },
            fontWeight = if (emphasise) FontWeight.SemiBold else null,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/** A non-blocking inline notice. Used for price and stock warnings. */
@Composable
fun CommerceNotice(
    text: String,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .padding(UsTheme.spacing.m),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textPrimary,
        )
    }
}

@Preview(showBackground = true)
@Composable
@Suppress("MagicNumber")
private fun PriceBreakdownPreview() {
    UsTheme {
        PriceBreakdownCard(
            breakdown = PriceBreakdown(
                subtotal = Paise(118000),
                discount = Paise(5000),
                shipping = Paise(7000),
                tax = Paise(18000),
                total = Paise(120000),
            ),
            modifier = Modifier.padding(UsTheme.spacing.pageHorizontal),
        )
    }
}

private val HAIRLINE = 1.dp
