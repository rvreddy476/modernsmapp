plugins {
    id("us.android.library")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.payments"
}

// The payment sheet, as a product-neutral module (2026-09-14).
//
// It owns the provider SDK, the one-flight launcher, the outcome mapping and
// the poll-until-the-server-says-so coordinator. A product (commerce today,
// Feast next) supplies a PaymentStatusSource over its own status endpoint and
// nothing else. The graph rules in the root build file keep this module free
// of every :feature:*, every :app* and :core:commerce, and keep it out of the
// partner apps, which take no payments.
dependencies {
    // `api`, not `implementation`, and on purpose. Razorpay delivers its result
    // to the ACTIVITY, which must itself implement PaymentResultWithDataListener
    // (Checkout checks the Activity it opened onto). ActivityPaymentHost
    // extends that listener, so the application compiling its Activity against
    // ActivityPaymentHost needs the Razorpay supertype on its compile
    // classpath. The alternative was :app keeping its own Razorpay declaration,
    // which is the thing this module exists to end.
    //
    // No consumer ProGuard rules here: the SDK's own AAR (standard-core) ships
    // `-keep class com.razorpay.** {*;}` and a keep on every `onPayment*`
    // method, and those travel with the dependency. :app never had any
    // Razorpay rules of its own.
    api(libs.razorpay.checkout)
    api(libs.kotlinx.coroutines.core)

    testImplementation(projects.core.testing)
}
