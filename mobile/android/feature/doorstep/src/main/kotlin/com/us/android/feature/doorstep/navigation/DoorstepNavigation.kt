// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph extension that uses them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.doorstep.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import androidx.navigation.toRoute
import com.us.android.feature.doorstep.address.AddAddressScreen
import com.us.android.feature.doorstep.address.AddressesScreen
import com.us.android.feature.doorstep.booking.SlotPickerScreen
import com.us.android.feature.doorstep.bookings.BookingDetailScreen
import com.us.android.feature.doorstep.bookings.BookingsScreen
import com.us.android.feature.doorstep.catalogue.CatalogueScreen
import com.us.android.feature.doorstep.catalogue.CategoryScreen
import com.us.android.feature.doorstep.checkout.CheckoutScreen
import com.us.android.feature.doorstep.outstanding.OutstandingScreen
import com.us.android.feature.doorstep.payment.DoorstepPaymentRequest
import com.us.android.feature.doorstep.service.ServiceDetailScreen
import kotlinx.serialization.Serializable

/** Doorstep, as one graph with its own back stack: Back from its root leaves Doorstep. */
@Serializable
data object DoorstepGraph

@Serializable
data object DoorstepHomeRoute

@Serializable
data class DoorstepCategoryRoute(val slug: String)

@Serializable
data class DoorstepServiceRoute(val serviceId: String)

/** [forBooking] true when the next step is the slot picker; false when only managing addresses. */
@Serializable
data class DoorstepAddressesRoute(val forBooking: Boolean = false)

@Serializable
data object DoorstepAddAddressRoute

/** A new booking for [addressId] (the service pick rides the session), or a reschedule of [rescheduleBookingId]. */
@Serializable
data class DoorstepSlotsRoute(val addressId: String? = null, val rescheduleBookingId: String? = null)

/** Everything checkout needs to rebuild itself from the server after process death. */
@Serializable
data class DoorstepCheckoutRoute(
    val quoteId: String,
    val addressId: String,
    val slotStart: String,
    val requireFemalePro: Boolean,
)

@Serializable
data object DoorstepBookingsRoute

@Serializable
data class DoorstepBookingRoute(val bookingId: String)

@Serializable
data object DoorstepOutstandingRoute

/**
 * Registers Doorstep.
 *
 * [onOpenPayment] and [onAbandonPayment] are supplied by `:app`, whose
 * Activity the payment sheet opens onto; this module never names the provider.
 */
@Suppress("LongMethod")
fun NavGraphBuilder.doorstepScreens(
    navController: NavController,
    onOpenPayment: (DoorstepPaymentRequest) -> Unit,
    onAbandonPayment: (DoorstepPaymentRequest) -> Unit,
) {
    navigation<DoorstepGraph>(startDestination = DoorstepHomeRoute) {
        composable<DoorstepHomeRoute> {
            CatalogueScreen(
                onBack = { navController.popBackStack<DoorstepGraph>(inclusive = true) },
                onOpenCategory = { navController.navigate(DoorstepCategoryRoute(it)) },
                onOpenBookings = { navController.navigate(DoorstepBookingsRoute) },
                onOpenOutstanding = { navController.navigate(DoorstepOutstandingRoute) },
            )
        }

        composable<DoorstepCategoryRoute> {
            CategoryScreen(
                onBack = navController::popBackStack,
                onOpenService = { navController.navigate(DoorstepServiceRoute(it)) },
            )
        }

        composable<DoorstepServiceRoute> {
            ServiceDetailScreen(
                onBack = navController::popBackStack,
                onContinue = { navController.navigate(DoorstepAddressesRoute(forBooking = true)) },
                onOpenOutstanding = { navController.navigate(DoorstepOutstandingRoute) },
            )
        }

        composable<DoorstepAddressesRoute> { entry ->
            val forBooking = entry.toRoute<DoorstepAddressesRoute>().forBooking
            AddressesScreen(
                onBack = navController::popBackStack,
                onAdd = { navController.navigate(DoorstepAddAddressRoute) },
                onPicked = { addressId ->
                    if (forBooking) navController.navigate(DoorstepSlotsRoute(addressId = addressId)) else navController.popBackStack()
                },
            )
        }

        composable<DoorstepAddAddressRoute> {
            AddAddressScreen(
                onBack = navController::popBackStack,
                onSaved = { navController.popBackStack() },
            )
        }

        composable<DoorstepSlotsRoute> {
            SlotPickerScreen(
                onBack = navController::popBackStack,
                onCheckout = { outcome ->
                    navController.navigate(
                        DoorstepCheckoutRoute(outcome.quoteId, outcome.addressId, outcome.slotStart, outcome.requireFemalePro),
                    )
                },
                onRescheduled = { navController.popBackStack() },
                onOpenOutstanding = { navController.navigate(DoorstepOutstandingRoute) },
                onStartAgain = { navController.popBackStack<DoorstepHomeRoute>(inclusive = false) },
            )
        }

        composable<DoorstepCheckoutRoute> {
            CheckoutScreen(
                onBack = navController::popBackStack,
                onOpenPayment = onOpenPayment,
                onAbandonPayment = onAbandonPayment,
                onPickAnotherSlot = {
                    // Back to the slot step (a fresh quote and fresh slots); home when it is gone.
                    if (!navController.popBackStack<DoorstepSlotsRoute>(inclusive = false)) {
                        navController.navigate(DoorstepHomeRoute)
                    }
                },
                onOpenBooking = { bookingId ->
                    navController.navigate(DoorstepBookingRoute(bookingId)) {
                        // Back from the booking returns to Doorstep home, never to a paid checkout.
                        popUpTo<DoorstepHomeRoute> { inclusive = false }
                    }
                },
                onOpenBookings = {
                    navController.navigate(DoorstepBookingsRoute) { popUpTo<DoorstepHomeRoute> { inclusive = false } }
                },
                onOpenOutstanding = { navController.navigate(DoorstepOutstandingRoute) },
            )
        }

        composable<DoorstepBookingsRoute> {
            BookingsScreen(
                onBack = navController::popBackStack,
                onOpenBooking = { navController.navigate(DoorstepBookingRoute(it)) },
                onBrowse = {
                    if (!navController.popBackStack<DoorstepHomeRoute>(inclusive = false)) navController.navigate(DoorstepHomeRoute)
                },
            )
        }

        composable<DoorstepBookingRoute> {
            BookingDetailScreen(
                onBack = navController::popBackStack,
                onReschedule = { navController.navigate(DoorstepSlotsRoute(rescheduleBookingId = it)) },
                onOpenPayment = onOpenPayment,
                onAbandonPayment = onAbandonPayment,
            )
        }

        composable<DoorstepOutstandingRoute> {
            OutstandingScreen(
                onBack = navController::popBackStack,
                onOpenPayment = onOpenPayment,
                onAbandonPayment = onAbandonPayment,
                onOpenBooking = { navController.navigate(DoorstepBookingRoute(it)) },
            )
        }
    }
}

/** The Explore launcher's Doorstep tile. */
fun NavController.navigateToDoorstep() = navigate(DoorstepGraph)

/** A Doorstep push: the booking screen, which reads the real state from the server. */
fun NavController.navigateToDoorstepBooking(bookingId: String) = navigate(DoorstepBookingRoute(bookingId))

/** The `doorstep.outstanding.due` push: the dues screen. */
fun NavController.navigateToDoorstepOutstanding() = navigate(DoorstepOutstandingRoute)
