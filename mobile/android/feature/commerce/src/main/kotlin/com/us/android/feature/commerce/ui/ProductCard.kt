package com.us.android.feature.commerce.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.toggleableState
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.ProductSummary
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * A product, as MStore draws it everywhere: the image with its placeholder,
 * the brand, the title over two lines, then the price, the struck-through MRP
 * and the saving — and a heart on the image that toggles.
 *
 * One card for the shelves, the grid, the favourites page and the search
 * results, because a product that looks different in two rows of the same
 * shop reads as two different products.
 *
 * The saving comes from [ProductSummary.discountPct], which the server states
 * and the domain only derives when it does not. The card performs no
 * arithmetic on money at all.
 */
@Composable
fun ProductCard(
    product: ProductSummary,
    onClick: () -> Unit,
    onToggleFavourite: (() -> Unit)?,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .testTag("product_card:${product.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Box {
            CommerceImage(
                url = product.thumbnailUrl ?: product.imageUrl,
                contentDescription = product.title,
                modifier = Modifier.fillMaxWidth(),
            )
            if (onToggleFavourite != null) {
                FavouriteHeart(
                    productId = product.id,
                    on = product.favourite,
                    onToggle = onToggleFavourite,
                    modifier = Modifier.align(Alignment.TopEnd),
                )
            }
        }
        product.brandName?.takeIf { it.isNotBlank() }?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(
            text = product.title,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            maxLines = TITLE_LINES,
            overflow = TextOverflow.Ellipsis,
        )
        PriceLine(price = product.fromPrice, mrp = product.mrp, discountPct = product.discountPct)
        if (!product.inStock) {
            Text(
                text = "Out of stock",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.statusDanger,
            )
        }
    }
}

/** The same card at a fixed width, for a horizontal shelf. */
@Composable
fun ShelfProductCard(
    product: ProductSummary,
    onClick: () -> Unit,
    onToggleFavourite: (() -> Unit)?,
    modifier: Modifier = Modifier,
    width: Dp = SHELF_CARD_WIDTH,
) = ProductCard(
    product = product,
    onClick = onClick,
    onToggleFavourite = onToggleFavourite,
    modifier = modifier.width(width),
)

/**
 * Price, struck-through MRP, and the saving.
 *
 * The percentage is shown only when the server (or, failing that, the one
 * derivation in the domain) produced one, so a card never advertises a
 * discount of 0%, and the strike-through and the percentage can never
 * disagree — both key off the same figures.
 */
@Composable
fun PriceLine(
    price: Paise,
    mrp: Paise,
    discountPct: Int?,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = price.formatWithSymbol(),
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
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
        if (discountPct != null) {
            Text(
                text = "$discountPct% off",
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.statusSuccess,
            )
        }
    }
}

/**
 * The heart on a card: a translucent disc so it reads over any photograph,
 * filled when saved.
 *
 * Announced as a toggle with its state, not as two different buttons — a
 * screen-reader user needs to know whether the product is already saved
 * before deciding what the tap will do.
 */
@Composable
fun FavouriteHeart(
    productId: String,
    on: Boolean,
    onToggle: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .padding(HEART_INSET)
            .size(HEART_TARGET)
            .background(UsTheme.extended.glassBg, CircleShape)
            .pressScale(onToggle)
            .semantics {
                role = Role.Switch
                toggleableState = if (on) ToggleableState.On else ToggleableState.Off
                contentDescription = if (on) "Saved. Remove from favourites" else "Save to favourites"
            }
            .testTag("favourite_heart:$productId"),
    ) {
        Icon(
            imageVector = if (on) UsIcons.HeartFilled else UsIcons.HeartOutline,
            contentDescription = null,
            tint = if (on) UsTheme.extended.accentSolid else Color.White,
            modifier = Modifier.size(HEART_GLYPH),
        )
    }
}

private const val TITLE_LINES = 2
private val HEART_TARGET = 32.dp
private val HEART_GLYPH = 18.dp
private val HEART_INSET = 6.dp
private val SHELF_CARD_WIDTH = 148.dp

@Preview(showBackground = true, backgroundColor = 0xFF041122)
@Composable
@Suppress("MagicNumber")
private fun ProductCardPreview() {
    UsTheme {
        ShelfProductCard(
            product = ProductSummary(
                id = "p1",
                title = "A product with a title long enough to wrap onto two lines",
                brandName = "Brand",
                primaryImageMediaId = null,
                fromPrice = Paise(118000),
                mrp = Paise(149000),
                avgRating = 4.3f,
                reviewCount = 27,
                inStock = true,
                discountPct = 20,
                favourite = true,
            ),
            onClick = {},
            onToggleFavourite = {},
        )
    }
}
