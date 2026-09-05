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
import com.us.android.feature.commerce.seller.DocumentScreen
import com.us.android.feature.commerce.seller.EditPriceScreen
import com.us.android.feature.commerce.seller.NewProductScreen
import com.us.android.feature.commerce.seller.PayoutScreen
import com.us.android.feature.commerce.seller.PickupAddressScreen
import com.us.android.feature.commerce.seller.ProductImagesScreen
import com.us.android.feature.commerce.seller.SellerHubActions
import com.us.android.feature.commerce.seller.SellerScreen
import com.us.android.feature.commerce.seller.StartSellingScreen
import com.us.android.feature.commerce.seller.StockScreen
import com.us.android.feature.commerce.seller.SubmitProductViewModel
import com.us.android.feature.commerce.seller.SubmitShopScreen
import kotlinx.serialization.Serializable
import java.util.UUID

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

// ─── MSeller ─────────────────────────────────────────────────────────

/** The seller app, as a graph. */
@Serializable
data object MSellerGraph

/**
 * MSeller's hub.
 *
 * Nothing about it is reachable without a seller account, and the screen says
 * so plainly rather than erroring — most people using this app are buyers.
 */
@Serializable
data object MSellerRoute

/**
 * Stock for one variant.
 *
 * Carries the title so the screen has something to show in its bar before the
 * stock figures arrive.
 */
@Serializable
data class SellerStockRoute(val variantId: String, val title: String)

/** A listing's photos. */
@Serializable
data class SellerImagesRoute(val productId: String, val title: String)

/** The pickup point — the origin of every shipment this seller sends. */
@Serializable
data object SellerPickupAddressRoute

/** Opening a shop. */
@Serializable
data object StartSellingRoute

/** Where the seller is paid. */
@Serializable
data object SellerPayoutRoute

/** Sending the shop for review. */
@Serializable
data object SubmitShopRoute

/** Sending an identity document for review. */
@Serializable
data object SellerDocumentRoute

/** Listing a product. */
@Serializable
data object NewProductRoute

/**
 * Changing a listing's price.
 *
 * Carries the title for the bar and NOT the price: the screen reads the
 * current price from the catalogue, because a figure carried through
 * navigation is a figure from whenever the previous screen loaded.
 */
@Serializable
data class SellerEditPriceRoute(val variantId: String, val title: String)

// ─── Graphs ──────────────────────────────────────────────────────────

/**
 * Registers MStore.
 *
 * The feature exposes `NavGraphBuilder` extensions rather than its route
 * table, so `:app` composes the graph without importing the feature's screens
 * or ViewModels. Navigation OUT of commerce arrives as callbacks:
 *
 *  * [onOpenPaymentSheet] / [onAbandonPaymentSheet] — the PSP handoff lives in
 *    `:app`, because a feature module must not know which provider is wired.
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
            )
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
                // Retrying payment on an existing order is a NEW attempt: the
                // previous one's late callback must not be able to settle this
                // opening. Minted here because this screen has no checkout
                // ViewModel to own one.
                onPayNow = { orderId, orderNumber ->
                    onOpenPaymentSheet(
                        PaymentAttempt(orderId = orderId, id = UUID.randomUUID().toString()),
                        orderNumber,
                    )
                },
            )
        }
    }
}

/**
 * Registers MSeller.
 *
 * Its own graph, so Back inside the seller app never lands halfway up a buyer
 * journey. Entered from the Explore launcher's MSeller tile, and from
 * MStore's profile menu — the same destination either way, because one person
 * can be both and the shop is a switch rather than a second account.
 */
@Suppress("LongMethod")
fun NavGraphBuilder.mSellerScreens(navController: NavController) {
    navigation<MSellerGraph>(startDestination = MSellerRoute) {
        composable<MSellerRoute> { entry ->
            // Scoped to this destination, so a submit in flight survives a
            // recomposition but does not outlive the screen that started it.
            val submitter: SubmitProductViewModel = hiltViewModel(entry)
            SellerScreen(
                onBack = navController::popBackStack,
                actions = SellerHubActions(
                    openStock = { variantId, title ->
                        navController.navigate(SellerStockRoute(variantId, title))
                    },
                    openImages = { productId, title ->
                        navController.navigate(SellerImagesRoute(productId, title))
                    },
                    openPickupAddress = { navController.navigate(SellerPickupAddressRoute) },
                    listProduct = { navController.navigate(NewProductRoute) },
                    submitShop = { navController.navigate(SubmitShopRoute) },
                    submitProduct = { productId -> submitter.submit(productId) { } },
                ),
                onStartSelling = { navController.navigate(StartSellingRoute) },
            )
        }

        composable<SellerStockRoute> { entry ->
            val route = entry.toRoute<SellerStockRoute>()
            StockScreen(
                title = route.title,
                onBack = navController::popBackStack,
                onEditPrice = {
                    navController.navigate(SellerEditPriceRoute(route.variantId, route.title))
                },
            )
        }

        composable<SellerImagesRoute> { entry ->
            val route = entry.toRoute<SellerImagesRoute>()
            ProductImagesScreen(
                productId = route.productId,
                title = route.title,
                onBack = navController::popBackStack,
                // Back to the hub, which re-reads the catalogue and now shows
                // the new cover on the row.
                onSaved = navController::popBackStack,
            )
        }

        composable<NewProductRoute> {
            NewProductScreen(
                onBack = navController::popBackStack,
                // Straight to the new listing's stock screen: the seller has
                // just stated an opening quantity and the next thing they
                // usually want is to check or correct it. popUpTo keeps the
                // create form off the back stack, so Back from there returns
                // to the hub rather than to a filled-in form that would list a
                // second product if resubmitted.
                onCreated = { productId ->
                    navController.navigate(SellerStockRoute(productId, "New product")) {
                        popUpTo(NewProductRoute) { inclusive = true }
                    }
                },
            )
        }

        composable<SellerPayoutRoute> {
            PayoutScreen(
                onBack = navController::popBackStack,
                onSaved = navController::popBackStack,
            )
        }

        composable<SellerDocumentRoute> {
            DocumentScreen(
                onBack = navController::popBackStack,
                // Back to the checklist, which re-reads readiness on resume
                // and will now show this requirement met.
                onAttached = navController::popBackStack,
            )
        }

        composable<SubmitShopRoute> {
            SubmitShopScreen(
                onBack = navController::popBackStack,
                onOpenPickupAddress = { navController.navigate(SellerPickupAddressRoute) },
                onOpenPayout = { navController.navigate(SellerPayoutRoute) },
                onOpenDocument = { navController.navigate(SellerDocumentRoute) },
                // Back to the hub, which now shows the shop as submitted. The
                // seller has nothing further to do until a reviewer answers.
                onSubmitted = navController::popBackStack,
            )
        }

        composable<StartSellingRoute> {
            StartSellingScreen(
                onBack = navController::popBackStack,
                // Back to the hub, which now has a shop to show. popBackStack
                // rather than navigate, so "open shop" does not stack a second
                // hub on top of the one the seller came from.
                onOpened = navController::popBackStack,
            )
        }

        composable<SellerEditPriceRoute> { entry ->
            val route = entry.toRoute<SellerEditPriceRoute>()
            EditPriceScreen(
                title = route.title,
                onBack = navController::popBackStack,
            )
        }

        composable<SellerPickupAddressRoute> {
            PickupAddressScreen(
                onBack = navController::popBackStack,
                // Back to the hub, not forward: saving a pickup address is a
                // settings edit, and leaving the seller somewhere new
                // afterwards is a navigation surprise.
                onSaved = navController::popBackStack,
            )
        }
    }
}

/** Opens MStore, the buyer app. */
fun NavController.navigateToMStore() = navigate(MStoreGraph)

/** Opens MSeller, the seller app. */
fun NavController.navigateToMSeller() = navigate(MSellerGraph)
