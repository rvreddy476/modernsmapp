package com.us.android.feature.commerce.home

/**
 * Where MStore's landing page can send the buyer.
 *
 * A record rather than six parameters: the screen's signature stops growing
 * with the page, and a caller that forgets one is a compile error rather than
 * a control that silently does nothing.
 */
data class StoreHomeDestinations(
    val onOpenProduct: (productId: String) -> Unit,
    val onOpenCategory: (categoryId: String, name: String) -> Unit,
    val onOpenSearch: () -> Unit,
    val onOpenFavourites: () -> Unit,
    val onOpenBag: () -> Unit,
    val onOpenProfile: () -> Unit,
)
