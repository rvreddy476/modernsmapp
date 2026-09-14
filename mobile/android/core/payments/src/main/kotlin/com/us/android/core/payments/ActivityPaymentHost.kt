package com.us.android.core.payments

import com.razorpay.PaymentData
import com.razorpay.PaymentResultWithDataListener

/**
 * The Activity binding: what an application's Activity implements so the
 * provider SDK's result reaches the launcher.
 *
 * Razorpay does not deliver its result to whoever opened the sheet. It calls
 * back on the ACTIVITY it opened onto, which must implement
 * [PaymentResultWithDataListener]. This interface is that listener with the
 * forwarding already written, so the application's Activity only supplies
 * [paymentResultSink] and never imports a Razorpay type:
 *
 * ```kotlin
 * @AndroidEntryPoint
 * class MainActivity : ComponentActivity(), ActivityPaymentHost {
 *     @Inject override lateinit var paymentResultSink: PaymentResultSink
 * }
 * ```
 *
 * The two callbacks deliberately interpret nothing. A1/R-3: a client callback
 * is evidence, never proof, and the product polls the server for every ending.
 * An Activity that decided "paid" from `onPaymentSuccess` would be asserting
 * something no one has verified.
 */
interface ActivityPaymentHost : PaymentResultWithDataListener {

    /**
     * Where the SDK's result goes. Bound to the launcher by [PaymentsModule].
     * Only the host should hold this: it is the one seam that can inject a
     * result into an in-flight sheet.
     */
    val paymentResultSink: PaymentResultSink

    override fun onPaymentSuccess(razorpayPaymentId: String?, paymentData: PaymentData?) {
        paymentResultSink.deliver(PaymentOutcome.Succeeded(razorpayPaymentId))
    }

    override fun onPaymentError(code: Int, response: String?, paymentData: PaymentData?) {
        // A user-cancelled sheet and a genuine provider error arrive through
        // the same callback. Both are reported as-is; every ending is treated
        // the same way, because a reported failure can still sit on top of a
        // capture that completed.
        paymentResultSink.deliver(PaymentOutcome.Failed(code, response))
    }
}
