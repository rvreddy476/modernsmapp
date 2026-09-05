// MatchingDeclarationName: this file is the feature's navigation contract â
// the route types plus the graph extension that uses them. Naming it after one
// route would hide the extension callers actually import.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.commerce.navigation

import androidx.hilt.navigation.compose.hiltViewModel
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.feature.commerce.address.AddAddressScreen
import com.us.android.feature.commerce.address.AddressScreen
import com.us.android.feature.commerce.cart.CartScreen
import com.us.android.feature.commerce.catalog.CatalogScreen
import com.us.android.feature.commerce.checkout.CheckoutScreen
import com.us.android.feature.commerce.orders.OrderDetailScreen
import com.us.android.feature.commerce.orders.OrdersScreen
import com.us.android.feature.commerce.product.ProductScreen
import com.us.android.feature.commerce.seller.DocumentScreen
import com.us.android.feature.commerce.seller.EditPriceScreen
import com.us.android.feature.commerce.seller.NewProductScreen
import com.us.android.feature.commerce.seller.PayoutScreen
import com.us.android.feature.commerce.seller.PickupAddressScreen
import com.us.android.feature.commerce.seller.SellerHubActions
import com.us.android.feature.commerce.seller.SellerScreen
import com.us.android.feature.commerce.seller.StartSellingScreen
import com.us.android.feature.commerce.seller.StockScreen
import com.us.android.feature.commerce.seller.SubmitProductViewModel
import com.us.android.feature.commerce.seller.SubmitShopScreen
import kotlinx.serialization.Serializable
import java.util.UUID

/**
 * The commerce buyer journey.
 *
 * Every destination is a type-safe route. The two that carry checkout state
 * ([CheckoutRoute]) do so as ARGUMENTS rather than through a shared
 * ViewModel, because the alternative â a graph-scoped ViewModel holding the
 * address and subtotal â survives process death only if every field is
 * saved-state-backed, and a half-restored checkout is the kind of thing that
 * places an order against the wrong address.
 */

/** The catalogue, as a tab root. */
@Serializable
data object CommerceRoute

@Serializable
data class ProductRoute(val productId: String)

@Serializable
data object CartRoute

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
 * that as `expected_total_minor`, so every non-empty cart failed with
 * PRICE_CHANGED. The server prices the cart and states the total, and there is
 * no client-side figure left for a route to carry.
 */
@Serializable
data class CheckoutRoute(
    val addressId: String,
    val addressSummary: String,
)

@Serializable
data object OrdersRoute

@Serializable
data class OrderDetailRoute(val orderId: String)

/**
 * The seller hub.
 *
 * A separate destination rather than a tab: most people using this app are
 * buyers, and the seller surface answers a different question from every other
 * commerce screen. Nothing about it is reachable without a seller account, and
 * the screen says so plainly rather than erroring.
 */
@Serializable
data object SellerRoute

/**
 * Stock for one variant.
 *
 * Carries the title so the screen has something to show in its top bar before
 * the stock figures arrive â a bar reading "Stock" over a spinner tells the
 * seller nothing about which product they opened.
 */
@Serializable
data class SellerStockRoute(val variantId: String, val title: String)

/** The pickup point â the origin of every shipment this seller sends. */
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
 * Carries the title for the top bar and NOT the price: the screen reads the
 * current price from the catalogue, because a figure carried through
 * navigation is a figure from whenever the previous screen loaded.
 */
@Serializable
data class SellerEditPriceRoute(val variantId: String, val title: String)

/**
 * Registers the commerce destinations.
 *
 * The feature exposes a `NavGraphBuilder` extension rather than its route
 * table, so `:app` composes the graph without importing the feature's screens
 * or ViewModels. Navigation OUT of commerce â and the PSP handoff, which
 * lives in `:app` because a feature module must not know which provider is
 * wired â arrives as callbacks.
 *
 * There is deliberately no `:feature:*` â `:feature:*` edge here. The rule is
 * enforced by the `checkFeatureGraph` task, not by convention.
 */
@Suppress("LongMethod")
fun NavGraphBuilder.commerceScreens(
    navController: NavController,
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit,
    onAbandonPaymentSheet: (attempt: PaymentAttempt) -> Unit,
) {
    composable<CommerceRoute> {
        CatalogScreen(
            onOpenProduct = { navController.navigate(ProductRoute(it)) },
            onOpenCart = { navController.navigate(CartRoute) },
            onOpenOrders = { navController.navigate(OrdersRoute) },
            onOpenSeller = { navController.navigate(SellerRoute) },
        )
    }

    composable<ProductRoute> {
        ProductScreen(
            onBack = navController::popBackStack,
            onOpenCart = { navController.navigate(CartRoute) },
        )
    }

    composable<CartRoute> {
        CartScreen(
            onBack = navController::popBackStack,
            onOpenProduct = { navController.navigate(ProductRoute(it)) },
            onCheckout = { navController.navigate(AddressRoute) },
            onContinueShopping = {
                navController.popBackStack(CommerceRoute, inclusive = false)
            },
        )
    }

    composable<AddressRoute> { entry ->
        // C3-LB-2: nothing about money travels through this step. The
        // checkout screen asks the server to price the cart.
        AddressScreen(
            onBack = navController::popBackStack,
            onAddAddress = { navController.navigate(AddAddressRoute) },
            onContinue = { addressId, summary ->
                navController.navigate(
                    CheckoutRoute(addressId = addressId, addressSummary = summary)
                )
            },
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
            onEditCart = {
                navController.popBackStack(CartRoute, inclusive = false)
            },
            onChangeAddress = {
                navController.popBackStack(AddressRoute, inclusive = false)
            },
            onViewOrder = { orderId ->
                // Replace the checkout entry: returning to a completed
                // checkout would re-run `prepare` against a consumed quote.
                navController.navigate(OrderDetailRoute(orderId)) {
                    popUpTo(CommerceRoute) { inclusive = false }
                }
            },
        )
    }

    composable<OrdersRoute> {
        OrdersScreen(
            onBack = navController::popBackStack,
            onOpenOrder = { navController.navigate(OrderDetailRoute(it)) },
            onStartShopping = {
                navController.popBackStack(CommerceRoute, inclusive = false)
            },
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

    composable<SellerRoute> { entry ->
        // Scoped to this destination, so a submit in flight survives a
        // recomposition but does not outlive the screen that started it.
        val submitter: SubmitProductViewModel = hiltViewModel(entry)
        val submitProduct: (String) -> Unit = { productId ->
            submitter.submit(productId) { }
        }
        SellerScreen(
            onBack = navController::popBackStack,
            actions = SellerHubActions(
                openStock = { variantId, title ->
                    navController.navigate(SellerStockRoute(variantId, title))
                },
                openPickupAddress = { navController.navigate(SellerPickupAddressRoute) },
                listProduct = { navController.navigate(NewProductRoute) },
                submitShop = { navController.navigate(SubmitShopRoute) },
                submitProduct = submitProduct,
            ),
            // Onboarding lives outside commerce, so the feature asks :app to
            // take it from here rather than importing a route it must not know.
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

    composable<NewProductRoute> {
        NewProductScreen(
            onBack = navController::popBackStack,
            // Straight to the new listing's stock screen: the seller has just
            // stated an opening quantity and the next thing they usually want
            // is to check or correct it. popUpTo keeps the create form off the
            // back stack, so Back from there returns to the hub rather than to
            // a filled-in form that would list a second product if resubmitted.
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
            // Back to the checklist, which re-reads readiness on resume and
            // will now show this requirement met.
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
            // settings edit, and leaving the seller somewhere new afterwards
            // is a navigation surprise.
            onSaved = navController::popBackStack,
        )
    }
}

/** Opens the catalogue. */
fun NavController.navigateToCommerce() = navigate(CommerceRoute)

/** Opens the buyer's order list. */
fun NavController.navigateToOrders() = navigate(OrdersRoute)

/** Opens the seller hub. */
fun NavController.navigateToSeller() = navigate(SellerRoute)
