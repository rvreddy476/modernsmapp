// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the two graph extensions that use them. Naming it after
// one route would hide the extensions callers actually import.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.commerce.navigation

import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import androidx.navigation.toRoute
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.feature.commerce.address.AddAddressScreen
import com.us.android.feature.commerce.address.AddressScreen
import com.us.android.feature.commerce.bag.BagScreen
import com.us.android.feature.commerce.browse.StoreBrowseScreen
import com.us.android.feature.commerce.checkout.CheckoutScreen
import com.us.android.feature.commerce.favourites.FavouritesScreen
import com.us.android.feature.commerce.home.StoreHomeDestinations
import com.us.android.feature.commerce.home.StoreHomeScreen
import com.us.android.feature.commerce.orders.OrderDetailScreen
import com.us.android.feature.commerce.orders.OrderScope
import com.us.android.feature.commerce.orders.OrdersScreen
import com.us.android.feature.commerce.payments.PaymentsScreen
import com.us.android.feature.commerce.product.ProductScreen
import com.us.android.feature.commerce.profile.StoreMenuDestinations
import com.us.android.feature.commerce.profile.StoreProfileSheet
import com.us.android.feature.commerce.profile.StoreProfileViewModel
import com.us.android.feature.commerce.tryon.TryOnScreen
import kotlinx.serialization.Serializable

/**
 * The two commerce mini-apps.
 *
 * MStore (the buyer app) and MSeller (the seller app) are TWO graphs, not one
 * (founder, 2026-09-05). They share a module because they share a repository
 * and a design, but they are separate products: one person can be both, and
 * moving between them is a switch through MStore's profile menu, never a
 * different account.
 *
 * Two nested graphs rather than one flat route table is what makes Back
 * behave: popping inside MSeller walks MSeller's own stack, and reaching its
 * root and going back again leaves the whole seller app at once instead of
 * surfacing halfway up a buyer journey.
 *
 * Every destination is a type-safe route. The one that carries checkout state
 * ([CheckoutRoute]) does so as ARGUMENTS rather than through a shared
 * ViewModel, because the alternative — a graph-scoped ViewModel holding the
 * address and subtotal — survives process death only if every field is
 * saved-state-backed, and a half-restored checkout is the kind of thing that
 * places an order against the wrong address.
 */

// ─── MStore ──────────────────────────────────────────────────────────

/** The buyer app, as a graph. */
@Serializable
data object MStoreGraph

/** MStore's landing page. */
@Serializable
data object MStoreRoute

@Serializable
data class ProductRoute(val productId: String)

/**
 * Virtual try-on, full screen.
 *
 * Carries the product id and the variant the buyer had chosen, and NOTHING
 * else — no descriptor and no effect slug. The screen re-reads the product for
 * the same reason the seller's edit-price route does: a descriptor carried
 * through navigation is one from whenever the previous screen loaded, and the
 * effect slug is the join to a file on disk, where stale is the one thing it
 * must not be.
 *
 * [variantId] defaults to empty rather than being nullable so the route stays a
 * plain two-string type-safe destination; empty means "no choice travelled" and
 * the screen falls back to the descriptor's first shade.
 */
@Serializable
data class TryOnRoute(val productId: String, val variantId: String = "")

/**
 * The bag.
 *
 * There is no "cart" anywhere in the product any more — the word, the glyph
 * and the route are all "bag". The SERVER paths still say cart, because
 * renaming those is a migration rather than a rename.
 */
@Serializable
data object BagRoute

/**
 * Results: a search, a category, or both.
 *
 * [title] travels with the route so the bar has something to say before the
 * first page arrives — a bar reading "Search" over a spinner tells the buyer
 * nothing about which category they opened.
 */
@Serializable
data class StoreBrowseRoute(
    val categoryId: String = "",
    val title: String = "",
    val query: String = "",
)

@Serializable
data object FavouritesRoute

/** The address book, opened from the profile menu. Manages rather than picks. */
@Serializable
data object AddressBookRoute

/** The address PICKER, inside checkout. */
@Serializable
data object AddressRoute

@Serializable
data object AddAddressRoute

/**
 * Checkout.
 *
 * C3-LB-2: it carries NO money. It used to carry a `cartSubtotalMinor`, which
 * the address step had no way to fill and therefore passed as zero; the
 * checkout screen then displayed `0 + shipping` as the total and submitted
 * that as `expected_total_minor`, so every non-empty bag failed with
 * PRICE_CHANGED. The server prices the bag and states the total, and there is
 * no client-side figure left for a route to carry.
 */
@Serializable
data class CheckoutRoute(
    val addressId: String,
    val addressSummary: String,
)

/** The order list. [scope] is "all" or "past" — see [OrderScope]. */
@Serializable
data class OrdersRoute(val scope: String = "all")

@Serializable
data class OrderDetailRoute(val orderId: String)

/** What has been charged and how each charge stands. Not a wallet. */
@Serializable
data object PaymentsRoute

// ─── Graphs ──────────────────────────────────────────────────────────

/**
 * Registers MStore.
 *
 * The feature exposes `NavGraphBuilder` extensions rather than its route
 * table, so `:app` composes the graph without importing the feature's screens
 * or ViewModels. Navigation OUT of commerce arrives as callbacks:
 *
 *  * [onOpenPaymentSheet] / [onAbandonPaymentSheet] — the PSP sheet opens onto
 *    the Activity, which only `:app` holds; `:app` calls CheckoutPaymentOpener,
 *    and the provider itself stays hidden behind `:core:payments`.
 *  * [onOpenSettings] — the app's settings belong to `:feature:profile`, and a
 *    `:feature:*` → `:feature:*` edge is forbidden by `checkFeatureGraph`.
 *  * [onOpenSeller] — the switch into the OTHER mini-app, resolved by `:app`
 *    so neither graph has to know the other's route type.
 */
@Suppress("LongMethod")
fun NavGraphBuilder.mStoreScreens(
    navController: NavController,
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit,
    onAbandonPaymentSheet: (attempt: PaymentAttempt) -> Unit,
    onOpenSettings: () -> Unit,
    onOpenSeller: () -> Unit,
) {
    navigation<MStoreGraph>(startDestination = MStoreRoute) {
        composable<MStoreRoute> { entry ->
            // One profile read for the bar's avatar and the menu's rows, held
            // by this destination: two instances would show the person's name
            // arriving twice and could disagree about whether they sell.
            val profile: StoreProfileViewModel = hiltViewModel(entry)
            val person by profile.state.collectAsStateWithLifecycle()
            var menuOpen by rememberSaveable { mutableStateOf(false) }

            val destinations = remember(navController) {
                StoreHomeDestinations(
                    onOpenProduct = { navController.navigate(ProductRoute(it)) },
                    onOpenCategory = { id, name ->
                        navController.navigate(StoreBrowseRoute(categoryId = id, title = name))
                    },
                    onOpenSearch = { navController.navigate(StoreBrowseRoute(title = "Search")) },
                    onOpenFavourites = { navController.navigate(FavouritesRoute) },
                    onOpenBag = { navController.navigate(BagRoute) },
                    onOpenProfile = { menuOpen = true },
                )
            }

            StoreHomeScreen(person = person.person, destinations = destinations)

            // Re-read on every open: someone can open a shop, or have one
            // approved, while the app is running — and a menu rendered once at
            // first composition would offer "Start selling" to a seller for
            // the rest of the process.
            LaunchedEffect(menuOpen) { if (menuOpen) profile.refresh() }

            if (menuOpen) {
                StoreProfileSheet(
                    destinations = StoreMenuDestinations(
                        onOrders = { navController.navigate(OrdersRoute(OrderScope.ALL.wire)) },
                        onFavourites = { navController.navigate(FavouritesRoute) },
                        onAddresses = { navController.navigate(AddressBookRoute) },
                        onPayments = { navController.navigate(PaymentsRoute) },
                        onPurchaseHistory = {
                            navController.navigate(OrdersRoute(OrderScope.PAST.wire))
                        },
                        onSettings = onOpenSettings,
                        onSeller = onOpenSeller,
                    ),
                    onDismiss = { menuOpen = false },
                    viewModel = profile,
                )
            }
        }

        composable<StoreBrowseRoute> {
            StoreBrowseScreen(
                onBack = navController::popBackStack,
                onOpenProduct = { navController.navigate(ProductRoute(it)) },
            )
        }

        composable<FavouritesRoute> {
            FavouritesScreen(
                onBack = navController::popBackStack,
                onOpenProduct = { navController.navigate(ProductRoute(it)) },
            )
        }

        composable<ProductRoute> {
            ProductScreen(
                onBack = navController::popBackStack,
                onOpenBag = { navController.navigate(BagRoute) },
                onTryOn = { productId, variantId ->
                    navController.navigate(TryOnRoute(productId, variantId))
                },
            )
        }

        composable<TryOnRoute> {
            // Back returns to the product, which is where the buyer decides.
            // A try-on is a look, not a step in the purchase.
            TryOnScreen(onBack = navController::popBackStack)
        }

        composable<BagRoute> {
            BagScreen(
                onBack = navController::popBackStack,
                onOpenProduct = { navController.navigate(ProductRoute(it)) },
                onCheckout = { navController.navigate(AddressRoute) },
                onContinueShopping = {
                    navController.popBackStack(MStoreRoute, inclusive = false)
                },
            )
        }

        composable<AddressRoute> {
            // C3-LB-2: nothing about money travels through this step. The
            // checkout screen asks the server to price the bag.
            AddressScreen(
                onBack = navController::popBackStack,
                onAddAddress = { navController.navigate(AddAddressRoute) },
                onContinue = { addressId, summary ->
                    navController.navigate(
                        CheckoutRoute(addressId = addressId, addressSummary = summary),
                    )
                },
            )
        }

        composable<AddressBookRoute> {
            // The same screen with no "Deliver here": opened from the profile
            // menu there is nothing to deliver.
            AddressScreen(
                onBack = navController::popBackStack,
                onAddAddress = { navController.navigate(AddAddressRoute) },
                onContinue = null,
            )
        }

        composable<AddAddressRoute> {
            AddAddressScreen(
                onBack = navController::popBackStack,
                onSaved = navController::popBackStack,
            )
        }

        composable<CheckoutRoute> { entry ->
            val route = entry.toRoute<CheckoutRoute>()
            CheckoutScreen(
                addressId = route.addressId,
                addressSummary = route.addressSummary,
                onBack = navController::popBackStack,
                onOpenPaymentSheet = onOpenPaymentSheet,
                onAbandonPaymentSheet = onAbandonPaymentSheet,
                onEditBag = { navController.popBackStack(BagRoute, inclusive = false) },
                onChangeAddress = {
                    navController.popBackStack(AddressRoute, inclusive = false)
                },
                onViewOrder = { orderId ->
                    // Replace the checkout entry: returning to a completed
                    // checkout would re-run `prepare` against a consumed quote.
                    navController.navigate(OrderDetailRoute(orderId)) {
                        popUpTo(MStoreRoute) { inclusive = false }
                    }
                },
            )
        }

        composable<OrdersRoute> {
            OrdersScreen(
                onBack = navController::popBackStack,
                onOpenOrder = { navController.navigate(OrderDetailRoute(it)) },
                onStartShopping = {
                    navController.popBackStack(MStoreRoute, inclusive = false)
                },
            )
        }

        composable<PaymentsRoute> {
            PaymentsScreen(
                onBack = navController::popBackStack,
                onOpenOrder = { navController.navigate(OrderDetailRoute(it)) },
            )
        }

        composable<OrderDetailRoute> {
            OrderDetailScreen(
                onBack = navController::popBackStack,
                onOpenProduct = { navController.navigate(ProductRoute(it)) },
                // Retrying payment on an existing order is a NEW attempt,
                // minted by the order's ViewModel (2026-09-30) so it can
                // match the sheet's ending against it and poll the server —
                // an attempt minted here had nobody listening for it.
                onOpenPaymentSheet = onOpenPaymentSheet,
            )
        }
    }
}

/** Opens MStore, the buyer app. */
fun NavController.navigateToMStore() = navigate(MStoreGraph)
