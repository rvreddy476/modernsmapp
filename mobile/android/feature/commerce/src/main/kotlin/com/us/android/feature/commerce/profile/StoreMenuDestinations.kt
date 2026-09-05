package com.us.android.feature.commerce.profile

/**
 * Where MStore's profile-menu rows go.
 *
 * A record rather than seven parameters, so a caller that forgets one is a
 * compile error and the sheet's signature does not grow every time the menu
 * does.
 *
 * Two of these leave commerce entirely and are resolved by `:app`:
 * [onSettings], because the app's settings live in `:feature:profile` and a
 * `:feature:*` → `:feature:*` edge is forbidden; and [onSeller], the switch
 * into the other mini-app, so neither graph has to know the other's route
 * type.
 */
data class StoreMenuDestinations(
    val onOrders: () -> Unit,
    val onFavourites: () -> Unit,
    val onAddresses: () -> Unit,
    val onPayments: () -> Unit,
    val onPurchaseHistory: () -> Unit,
    val onSettings: () -> Unit,
    val onSeller: () -> Unit,
)
