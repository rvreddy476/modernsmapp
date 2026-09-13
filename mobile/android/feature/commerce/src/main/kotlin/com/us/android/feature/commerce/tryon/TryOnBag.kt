package com.us.android.feature.commerce.tryon

import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.Variant

/**
 * Turning "the shade on the face" into "the thing in the bag".
 *
 * ## WHY THIS IS A FUNCTION AND NOT AN `addToCart(selectedId)` CALL
 *
 * A try-on that cannot buy is a toy, so the try-on screen has an Add to bag
 * button — and the moment it does, the identity of the shade and the identity
 * of the purchasable variant become one join that has to be right. The server
 * contract says `try_on.variants[].id` IS the catalogue variant id, but a
 * contract is a claim about the server, not about this build: a seller who
 * configures try-on shades that do not correspond to variants, or a shade
 * whose variant has since sold out, must produce a disabled button and one
 * honest line — never a cart row for something that cannot ship.
 *
 * Four answers, not a nullable id, because each one has different words on the
 * button and each is a different thing to tell the shopper.
 */
sealed interface TryOnBagTarget {
    /** This shade is a real, in-stock variant. */
    data class Purchasable(val variantId: String, val price: Paise) : TryOnBagTarget

    /** Nothing is chosen — the product listed no shades, or none is selected yet. */
    data object NoShade : TryOnBagTarget

    /**
     * The shade exists on the try-on descriptor but matches no catalogue
     * variant. A seller configuration error, and the only honest answer is
     * that this shade cannot be bought.
     */
    data object NotPurchasable : TryOnBagTarget

    /** A real variant, out of stock. Still worth trying on; not worth a cart row. */
    data object OutOfStock : TryOnBagTarget
}

/**
 * The bag target for the currently worn shade.
 *
 * [tryOnVariantId] is the id from the try-on descriptor; [variants] are the
 * product's purchasable ones. Matched on id exactly — no trimming, no
 * case-folding, no "close enough": an id is an opaque server key, and a fuzzy
 * match here would put the wrong lipstick in someone's cart.
 */
fun tryOnBagTarget(tryOnVariantId: String?, variants: List<Variant>): TryOnBagTarget {
    val wanted = tryOnVariantId?.takeIf { it.isNotBlank() } ?: return TryOnBagTarget.NoShade
    val variant = variants.firstOrNull { it.id == wanted } ?: return TryOnBagTarget.NotPurchasable
    // Both conditions, because a server may say `in_stock: true` with a zero
    // quantity during a race with another buyer's checkout, and the quantity
    // is the one that decides whether the add will actually succeed.
    if (!variant.inStock || variant.availableQty <= 0) return TryOnBagTarget.OutOfStock
    return TryOnBagTarget.Purchasable(variant.id, variant.sellingPrice)
}

/**
 * The one line to show when the button is disabled, or null when it is not.
 *
 * Each says what is wrong and nothing else. None of them names a variant id, a
 * descriptor or a seller: those are our problems, not the shopper's.
 */
fun TryOnBagTarget.blockedReason(): String? = when (this) {
    is TryOnBagTarget.Purchasable -> null
    TryOnBagTarget.NoShade -> "Choose a shade to add it to your bag."
    TryOnBagTarget.NotPurchasable -> "This shade is not on sale yet."
    TryOnBagTarget.OutOfStock -> "This shade is out of stock."
}
